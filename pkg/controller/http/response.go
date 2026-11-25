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

// maxHTMLRequestBody bounds the JSON of requests that may carry an HTML
// paper's document (creating a paper, a batch): the document is at most
// model.MaxHTMLSize bytes, and escaping in JSON may make it up to six times
// longer (\u003c for <). Cloud Run accepts requests up to 32 MiB.
const maxHTMLRequestBody = 32 << 20

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
	// MovedTo is set for "moved": the paper now lives in another workspace.
	MovedTo *model.Forward `json:"moved_to,omitempty"`
	// Candidates is set when a path names more than one paper.
	Candidates []*usecase.PaperRef `json:"candidates,omitempty"`
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
	case errors.Is(err, model.ErrBusy):
		return http.StatusConflict, "busy"
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
	if f, ok := usecase.IsMoved(err); ok {
		detail.Code, detail.MovedTo = "moved", f
	}
	// An OAuth error keeps its code as the message, so the consent screen
	// can tell the person what is wrong with the request.
	var oe *model.OAuthError
	if errors.As(err, &oe) {
		detail.Message = oe.Code
	}
	var amb *usecase.AmbiguousError
	if errors.As(err, &amb) {
		detail.Code, detail.Candidates = "ambiguous", amb.Candidates
	}
	writeJSON(ctx, w, status, errorBody{Error: detail})
}

func decodeJSON(r *http.Request, v any) error {
	return decodeJSONLimit(r, v, maxRequestBody)
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// decodeJSONLimit decodes a JSON body of at most limit bytes; a longer one
// is refused as too large.
func decodeJSONLimit(r *http.Request, v any, limit int64) error {
	counted := &countingReader{r: io.LimitReader(r.Body, limit+1)}
	dec := json.NewDecoder(counted)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if counted.n > limit {
			return goerr.Wrap(model.ErrInvalidArgument, "the request body is too large", goerr.V("max_bytes", limit))
		}
		return goerr.Wrap(model.ErrInvalidArgument, "invalid JSON body", goerr.V("cause", err.Error()))
	}
	return nil
}
