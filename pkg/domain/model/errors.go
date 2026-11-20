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
	ErrBlockLocked      = goerr.New("block is being edited by someone else")
	ErrAlreadyExists    = goerr.New("already exists")
)
