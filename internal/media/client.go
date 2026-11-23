package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/overmindv/entities/internal/domain"
)

var (
	ErrUnavailable     = errors.New("media service unavailable")
	ErrLogoNotReady    = errors.New("logo unavailable for binding")
	ErrVersionConflict = errors.New("logo binding version conflict")
	ErrInvalidBinding  = errors.New("invalid logo binding")
)

type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

func NewClient(address, token string) (*Client, error) {
	base, err := url.Parse(address)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") ||
		base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" {
		return nil, errors.New("ENTITIES_MEDIA_URL must be an absolute HTTP(S) URL without credentials, query or fragment")
	}
	if len(token) < 32 || len(token) > 256 || strings.IndexFunc(token, func(r rune) bool {
		return r <= ' ' || r >= 127
	}) >= 0 {
		return nil, errors.New("ENTITIES_MEDIA_TOKEN must be a separate Media service token")
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxConnsPerHost = 8

	return &Client{
		base:  base,
		token: token,
		http: &http.Client{
			Timeout:   3 * time.Second,
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

type bindingFiles struct {
	ActorID string   `json:"actor_user_id"`
	FileIDs []string `json:"file_ids"`
}

func (c *Client) CheckLogo(ctx context.Context, actorID, fileID string) error {
	files, err := logoFiles(actorID, &fileID)
	if err != nil {
		return err
	}

	return c.request(ctx, http.MethodPost, c.base.JoinPath("v1", "internal", "entities", "binding-files", "check"), files)
}

func (c *Client) ReplaceLogo(ctx context.Context, universityID, actorID string, fileID *string, version int64) error {
	id, ok := bindingID(universityID)
	if !ok || version <= 0 {
		return ErrInvalidBinding
	}
	files, err := logoFiles(actorID, fileID)
	if err != nil {
		return err
	}
	body := struct {
		bindingFiles
		Version int64 `json:"version"`
	}{
		bindingFiles: files,
		Version:      version,
	}

	return c.request(ctx, http.MethodPut, c.base.JoinPath("v1", "internal", "entities", "logos", id, "bindings"), body)
}

func (c *Client) LookupLogo(ctx context.Context, universityID string) (domain.LogoBindingState, error) {
	id, ok := bindingID(universityID)
	if !ok {
		return domain.LogoBindingState{}, ErrInvalidBinding
	}
	resp, err := c.do(ctx, http.MethodGet, c.base.JoinPath("v1", "internal", "entities", "logos", id, "bindings"), nil)
	if err != nil {
		return domain.LogoBindingState{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return domain.LogoBindingState{}, ErrUnavailable
	}
	var body struct {
		Version *int64  `json:"version"`
		FileID  *string `json:"file_id"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 4096))
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF || body.Version == nil || *body.Version < 0 {
		return domain.LogoBindingState{}, ErrUnavailable
	}
	if body.FileID != nil {
		fileID, ok := bindingID(*body.FileID)
		if !ok {
			return domain.LogoBindingState{}, ErrUnavailable
		}
		body.FileID = &fileID
	}

	return domain.LogoBindingState{Version: *body.Version, FileID: body.FileID}, nil
}

func logoFiles(actorID string, fileID *string) (bindingFiles, error) {
	actor, ok := bindingID(actorID)
	if !ok {
		return bindingFiles{}, ErrInvalidBinding
	}
	files := bindingFiles{
		ActorID: actor,
		FileIDs: []string{},
	}
	if fileID != nil {
		id, ok := bindingID(*fileID)
		if !ok {
			return bindingFiles{}, ErrInvalidBinding
		}
		files.FileIDs = append(files.FileIDs, id)
	}

	return files, nil
}

func bindingID(value string) (string, bool) {
	id, err := uuid.Parse(value)

	return id.String(), len(value) == 36 && err == nil && id != uuid.Nil
}

func (c *Client) request(ctx context.Context, method string, endpoint *url.URL, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return ErrInvalidBinding
	}
	resp, err := c.do(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusConflict:
		var body struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&body); err == nil {
			switch body.Code {
			case "FILE_NOT_READY":
				return ErrLogoNotReady
			case "BINDING_VERSION_CONFLICT":
				return ErrVersionConflict
			}
		}
	}

	return ErrUnavailable
}

func (c *Client) do(ctx context.Context, method string, endpoint *url.URL, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, ErrInvalidBinding
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Media-Service-Token", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}

	return resp, nil
}
