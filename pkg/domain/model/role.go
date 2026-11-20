package model

import (
	"github.com/m-mizutani/goerr/v2"
)

// Role is an ordered permission level. A higher role includes every
// permission of the lower ones.
type Role string

const (
	RoleNone   Role = ""
	RoleViewer Role = "viewer"
	RoleEditor Role = "editor"
	RoleOwner  Role = "owner"
)

func (r Role) rank() int {
	switch r {
	case RoleViewer:
		return 1
	case RoleEditor:
		return 2
	case RoleOwner:
		return 3
	default:
		return 0
	}
}

// AtLeast reports whether r grants at least the permissions of other.
func (r Role) AtLeast(other Role) bool { return r.rank() >= other.rank() }

// Max returns the higher of r and other.
func (r Role) Max(other Role) Role {
	if other.rank() > r.rank() {
		return other
	}
	return r
}

// Min returns the lower of r and other.
func (r Role) Min(other Role) Role {
	if other.rank() < r.rank() {
		return other
	}
	return r
}

func (r Role) Validate() error {
	switch r {
	case RoleViewer, RoleEditor, RoleOwner:
		return nil
	default:
		return goerr.Wrap(ErrInvalidArgument, "invalid role", goerr.V("role", r))
	}
}

// SubjectKind is the kind of principal a RoleBinding grants a role to.
type SubjectKind string

const (
	SubjectUser  SubjectKind = "user"
	SubjectGroup SubjectKind = "group"
	// SubjectDomain matches every user whose email is in the given domain.
	SubjectDomain SubjectKind = "domain"
	// SubjectAuthenticated matches every signed-in user. ID must be empty.
	SubjectAuthenticated SubjectKind = "authenticated"
)

// Subject identifies who a RoleBinding applies to.
type Subject struct {
	Kind SubjectKind `json:"kind"`
	ID   string      `json:"id,omitempty"`
}

func (s Subject) Validate() error {
	switch s.Kind {
	case SubjectUser, SubjectGroup, SubjectDomain:
		if s.ID == "" {
			return goerr.Wrap(ErrInvalidArgument, "subject id is required", goerr.V("kind", s.Kind))
		}
	case SubjectAuthenticated:
		if s.ID != "" {
			return goerr.Wrap(ErrInvalidArgument, "authenticated subject must not have an id")
		}
	default:
		return goerr.Wrap(ErrInvalidArgument, "invalid subject kind", goerr.V("kind", s.Kind))
	}
	return nil
}

// RoleBinding grants Role to Subject.
type RoleBinding struct {
	Subject Subject `json:"subject"`
	Role    Role    `json:"role"`
}

func (b RoleBinding) Validate() error {
	if err := b.Subject.Validate(); err != nil {
		return err
	}
	return b.Role.Validate()
}

func validateBindings(bs []RoleBinding) error {
	seen := make(map[Subject]struct{}, len(bs))
	for i, b := range bs {
		if err := b.Validate(); err != nil {
			return goerr.Wrap(err, "invalid role binding", goerr.V("index", i))
		}
		if _, ok := seen[b.Subject]; ok {
			return goerr.Wrap(ErrInvalidArgument, "duplicated subject in role bindings", goerr.V("subject", b.Subject))
		}
		seen[b.Subject] = struct{}{}
	}
	return nil
}
