package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/overmindv/entities/internal/apperror"
	"github.com/overmindv/entities/internal/domain"
)

func decodeTopic(w http.ResponseWriter, r *http.Request) (domain.Topic, bool) {
	var input struct {
		domain.Topic
		LearningMetadata json.RawMessage `json:"learning_metadata"`
	}
	if !decode(w, r, &input) {
		return domain.Topic{}, false
	}
	if len(input.LearningMetadata) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(input.LearningMetadata))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input.Topic.LearningMetadata); err != nil {
			writeJSON(w, http.StatusBadRequest,
				apperror.New(apperror.ValidationError, "некорректный learning_metadata", http.StatusBadRequest))

			return domain.Topic{}, false
		}
	}

	return input.Topic, true
}
