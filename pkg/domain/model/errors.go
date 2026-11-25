package model

import (
	"github.com/m-mizutani/goerr/v2"
)

// Sentinel errors. Callers discriminate with errors.Is; the HTTP layer maps
// each one to a status code. Every sentinel here describes a condition the
// caller caused, so they are tagged benign (see errutil.TagBenign) at the
// HTTP boundary rather than here, to keep the domain free of logging policy.
var (
	ErrNotFound         = goerr.New("not found")
	ErrPermissionDenied = goerr.New("permission denied")
	ErrUnauthenticated  = goerr.New("unauthenticated")
	ErrInvalidArgument  = goerr.New("invalid argument")
	// ErrConflict: the write was based on an older version of a block whose
	// contents someone else has changed since. Retrying with force overwrites.
	ErrConflict      = goerr.New("conflict with a newer change")
	ErrAlreadyExists = goerr.New("already exists")
	// ErrBusy: a background job is working on the paper (a move); try again
	// when it has finished.
	ErrBusy = goerr.New("a background job is working on this paper")
)

// OAuthError is an error of the OAuth authorization flow with its error
// code from RFC 6749 / RFC 7591 (invalid_grant, invalid_redirect_uri, ...).
// It is an invalid argument, so errors.Is(err, ErrInvalidArgument) holds.
type OAuthError struct {
	Code        string
	Description string
}

func (e *OAuthError) Error() string { return e.Code + ": " + e.Description }

func (e *OAuthError) Unwrap() error { return ErrInvalidArgument }

// OAuth error codes.
const (
	OAuthInvalidRequest          = "invalid_request"
	OAuthInvalidClient           = "invalid_client"
	OAuthInvalidGrant            = "invalid_grant"
	OAuthUnsupportedGrantType    = "unsupported_grant_type"
	OAuthUnsupportedResponseType = "unsupported_response_type"
	OAuthInvalidTarget           = "invalid_target"
	OAuthInvalidRedirectURI      = "invalid_redirect_uri"
	OAuthInvalidClientMetadata   = "invalid_client_metadata"
	OAuthInvalidCodeChallenge    = "invalid_code_challenge"
	OAuthInvalidScope            = "invalid_scope"
)

func NewOAuthError(code, description string) *OAuthError {
	return &OAuthError{Code: code, Description: description}
}
