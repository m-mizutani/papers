package model

import (
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// BlockLock is a soft, expiring lock telling others that a block is being
// edited. It is held by one live connection (HolderKey): a browser tab's
// WebSocket connection, or an agent's token. Locks expire unless renewed, so
// a crashed client never leaves a block locked for long.
type BlockLock struct {
	WorkspaceID WorkspaceID `json:"workspace_id"`
	PaperID     PaperID     `json:"paper_id"`
	BlockID     BlockID     `json:"block_id"`
	HolderKey   string      `json:"holder_key"`
	Holder      Actor       `json:"holder"`
	AcquiredAt  time.Time   `json:"acquired_at"`
	ExpiresAt   time.Time   `json:"expires_at"`
}

func (l *BlockLock) Validate() error {
	if l.WorkspaceID == "" || l.PaperID == "" || l.BlockID == "" {
		return goerr.Wrap(ErrInvalidArgument, "lock target is required")
	}
	if l.HolderKey == "" {
		return goerr.Wrap(ErrInvalidArgument, "lock holder key is required")
	}
	if err := l.Holder.Validate(); err != nil {
		return goerr.Wrap(err, "invalid lock holder")
	}
	if !l.ExpiresAt.After(l.AcquiredAt) {
		return goerr.Wrap(ErrInvalidArgument, "lock must expire after it is acquired")
	}
	return nil
}

func (l *BlockLock) Active(now time.Time) bool { return now.Before(l.ExpiresAt) }
