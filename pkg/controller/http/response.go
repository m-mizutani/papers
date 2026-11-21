package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
	"github.com/m-mizutani/papers/pkg/utils/errutil"
)

const maxRequestBody = 4 << 20

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Conflicts is set for "conflict" errors: each op that was based on an
	// older version, with the block as it is now. Resend with force to
	// overwrite.
	Conflicts []usecase.Conflict `json:"conflicts,omitempty"`
}

// statusOf maps a domain error to an HTTP status and a stable error code.
func statusOf(err error) (int, string) {
	switch {
	case errors.Is(err, model.ErrUnauthenticated):
		return http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, model.ErrPermissionDenied):
		return http.StatusForbidden, "permission_denied"
	case errors.Is(err, model.ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, model.ErrConflict):
		return http.StatusConflict, "conflict"
	case errors.Is(err, model.ErrAlreadyExists):
		return http.StatusConflict, "already_exists"
	case errors.Is(err, model.ErrInvalidArgument):
		return http.StatusBadRequest, "invalid_argument"
	default:
		return http.StatusInternalServerError, "internal"
	}
}

func writeJSON(ctx context.Context, w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		errutil.Handle(ctx, goerr.Wrap(err, "failed to encode response"), "failed to encode response")
	}
}

// writeError reports err and writes the JSON error body. Client errors are
// tagged benign so they do not page anyone; internal errors keep their
// message out of the response.
func writeError(ctx context.Context, w http.ResponseWriter, err error) {
	status, code := statusOf(err)
	msg := err.Error()
	if status >= 500 {
		errutil.Handle(ctx, err, "request failed")
		msg = "internal server error"
	} else {
		errutil.Handle(ctx, goerr.Wrap(err, "request rejected", goerr.T(errutil.TagBenign)), "request rejected")
	}
	detail := errorDetail{Code: code, Message: msg}
	var conflict *usecase.ConflictError
	if errors.As(err, &conflict) {
		detail.Conflicts = conflict.Conflicts
	}
	writeJSON(ctx, w, status, errorBody{Error: detail})
}

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxRequestBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return goerr.Wrap(model.ErrInvalidArgument, "invalid JSON body", goerr.V("cause", err.Error()))
	}
	return nil
}
