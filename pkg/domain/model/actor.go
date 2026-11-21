package model

import "github.com/m-mizutani/goerr/v2"

// ActorKind distinguishes a human acting through the Web UI from an AI agent
// acting through the API / MCP on behalf of a user. Commit logs and block
// bylines show the difference so humans can tell who (or what) wrote a block.
type ActorKind string

const (
	ActorHuman ActorKind = "human"
	ActorAgent ActorKind = "agent"
)

// Actor is who performed a change. UserID is always set: an agent always acts
// on behalf of a user, whose permissions it borrows (capped by the token).
type Actor struct {
	Kind      ActorKind `json:"kind"`
	UserID    UserID    `json:"user_id"`
	Name      string    `json:"name"`
	AgentName string    `json:"agent_name,omitempty"`
	TokenID   TokenID   `json:"token_id,omitempty"`
}

func (a Actor) Validate() error {
	switch a.Kind {
	case ActorHuman, ActorAgent:
	default:
		return goerr.Wrap(ErrInvalidArgument, "invalid actor kind", goerr.V("kind", a.Kind))
	}
	if a.UserID == "" {
		return goerr.Wrap(ErrInvalidArgument, "actor user id is required")
	}
	return nil
}
