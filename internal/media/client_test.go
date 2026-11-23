package media

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func TestLogoBindingContract(t *testing.T) {
	t.Parallel()

	actor, file, university := uuid.NewString(), uuid.NewString(), uuid.NewString()
	token := strings.Repeat("t", 40)
	for _, test := range []struct {
		name    string
		method  string
		path    string
		files   []string
		version int64
		fileID  *string
	}{
		{
			name:   "check",
			method: http.MethodPost,
			path:   "/media/v1/internal/entities/binding-files/check",
			files:  []string{file},
		},
		{
			name:    "replace",
			method:  http.MethodPut,
			path:    "/media/v1/internal/entities/logos/" + university + "/bindings",
			files:   []string{file},
			version: 7,
			fileID:  &file,
		},
		{
			name:    "detach",
			method:  http.MethodPut,
			path:    "/media/v1/internal/entities/logos/" + university + "/bindings",
			files:   []string{},
			version: 8,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var seen atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				seen.Add(1)
				if req.Method != test.method || req.URL.Path != test.path {
					t.Error("unexpected method or path", req.Method, req.URL.Path)
				}
				if req.Header.Get("X-Media-Service-Token") != token || req.Header.Get("Content-Type") != "application/json" {
					t.Error("invalid Media request headers")
				}
				var payload struct {
					bindingFiles
					Version *int64 `json:"version"`
				}
				if err := json.NewDecoder(req.Body).Decode(&payload); err != nil || payload.ActorID != actor ||
					payload.FileIDs == nil || !slices.Equal(payload.FileIDs, test.files) {
					t.Error("invalid binding request", err)
				}
				if (test.version == 0 && payload.Version != nil) ||
					(test.version != 0 && (payload.Version == nil || *payload.Version != test.version)) {
					t.Error("unexpected binding version")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(server.Close)
			client := testClient(t, server.URL+"/media")
			var err error
			if test.version == 0 {
				err = client.CheckLogo(t.Context(), strings.ToUpper(actor), file)
			} else {
				err = client.ReplaceLogo(t.Context(), university, actor, test.fileID, test.version)
			}
			if err != nil || seen.Load() != 1 {
				t.Error("expected one successful request", err, seen.Load())
			}
		})
	}
}

func TestLogoBindingErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		status int
		code   string
		want   error
	}{
		{"not ready", http.StatusConflict, "FILE_NOT_READY", ErrLogoNotReady},
		{"version conflict", http.StatusConflict, "BINDING_VERSION_CONFLICT", ErrVersionConflict},
		{"authorization", http.StatusForbidden, "PERMISSION_DENIED", ErrUnavailable},
		{"service error", http.StatusServiceUnavailable, "", ErrUnavailable},
		{"unexpected conflict", http.StatusConflict, "OTHER", ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": test.code, "message": "private upstream details"})
			}))
			t.Cleanup(server.Close)
			client := testClient(t, server.URL)
			err := client.CheckLogo(t.Context(), uuid.NewString(), uuid.NewString())
			if !errors.Is(err, test.want) || strings.Contains(err.Error(), "private upstream details") {
				t.Error("unexpected Media error", err)
			}
		})
	}
}

func TestLogoBindingRejectsRedirectAndInvalidIDs(t *testing.T) {
	t.Parallel()

	var sent, redirected atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(destination.Close)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		sent.Add(1)
		http.Redirect(w, req, destination.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)

	client := testClient(t, source.URL)
	ctx, actor := t.Context(), uuid.NewString()
	if err := client.ReplaceLogo(ctx, "../private", actor, nil, 1); !errors.Is(err, ErrInvalidBinding) {
		t.Error("invalid university ID was not rejected", err)
	}
	if err := client.CheckLogo(ctx, actor, uuid.Nil.String()); !errors.Is(err, ErrInvalidBinding) {
		t.Error("empty file ID was not rejected", err)
	}
	if err := client.ReplaceLogo(ctx, uuid.NewString(), actor, nil, 0); !errors.Is(err, ErrInvalidBinding) {
		t.Error("invalid binding version was not rejected", err)
	}
	if err := client.CheckLogo(ctx, actor, uuid.NewString()); !errors.Is(err, ErrUnavailable) {
		t.Error("redirect was not treated as an upstream failure", err)
	}
	if sent.Load() != 1 || redirected.Load() != 0 {
		t.Error("invalid input or redirect reached the network", sent.Load(), redirected.Load())
	}
}

func TestLogoClientRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()

	for _, address := range []string{"", "file:///tmp/media", "http://user@media", "http://media?key=value", "http://media#api"} {
		if _, err := NewClient(address, strings.Repeat("t", 40)); err == nil {
			t.Error("invalid Media URL was accepted")
		}
	}
	for _, token := range []string{"short", strings.Repeat(" ", 40), strings.Repeat("t", 40) + "\n"} {
		if _, err := NewClient("http://media:8080", token); err == nil {
			t.Error("invalid Media token was accepted")
		}
	}
}

func TestLookupLogoBinding(t *testing.T) {
	t.Parallel()

	university, file := uuid.NewString(), uuid.NewString()
	for _, test := range []struct {
		name    string
		body    string
		version int64
		bound   bool
		err     error
	}{
		{name: "bound", body: `{"version":7,"file_id":"` + file + `"}`, version: 7, bound: true},
		{name: "lost row", body: `{"version":7,"file_id":null}`, version: 7},
		{name: "missing", body: `{"version":0,"file_id":null}`},
		{name: "no version", body: `{"file_id":null}`, err: ErrUnavailable},
		{name: "invalid file ID", body: `{"version":7,"file_id":"not-a-uuid"}`, err: ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != http.MethodGet || req.URL.Path != "/v1/internal/entities/logos/"+university+"/bindings" ||
					req.Header.Get("X-Media-Service-Token") != strings.Repeat("t", 40) {
					t.Error("unexpected binding lookup request")
				}
				_, _ = w.Write([]byte(test.body))
			}))
			t.Cleanup(server.Close)
			state, err := testClient(t, server.URL).LookupLogo(t.Context(), university)
			if !errors.Is(err, test.err) || state.Version != test.version ||
				(test.bound && (state.FileID == nil || *state.FileID != file)) ||
				(!test.bound && state.FileID != nil) {
				t.Error("unexpected binding state", state, err)
			}
		})
	}
}

func testClient(t *testing.T, address string) *Client {
	t.Helper()

	client, err := NewClient(address, strings.Repeat("t", 40))
	if err != nil {
		t.Fatal(err)
	}

	return client
}
