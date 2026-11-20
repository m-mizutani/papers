// Package interfaces declares the ports the use cases depend on. Concrete
// adapters live under pkg/repository and pkg/service.
package interfaces

import (
	"context"
	"time"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

// Repository is the persistence port, including the realtime change feed
// (WatchPaper), since the storage itself is the source of change events. Every Get* returns an error wrapping
// model.ErrNotFound when the entity does not exist. Every Put* validates the
// entity before writing and stores it as-is (no field-by-field copy, no
// timestamp stamping: the caller owns CreatedAt / UpdatedAt).
type Repository interface {
	UserRepository
	SessionRepository
	TokenRepository
	GroupRepository
	WorkspaceRepository
	PaperRepository
	LockRepository
	Close() error
}

type UserRepository interface {
	GetUser(ctx context.Context, id model.UserID) (*model.User, error)
	GetUserByEmail(ctx context.Context, email string) (*model.User, error)
	ListUsers(ctx context.Context) ([]*model.User, error)
	PutUser(ctx context.Context, user *model.User) error

	GetIdentity(ctx context.Context, provider model.IdentityProvider, subject string) (*model.Identity, error)
	PutIdentity(ctx context.Context, identity *model.Identity) error
}

type SessionRepository interface {
	GetSession(ctx context.Context, id model.SessionID) (*model.Session, error)
	PutSession(ctx context.Context, session *model.Session) error
	DeleteSession(ctx context.Context, id model.SessionID) error
}

type TokenRepository interface {
	GetToken(ctx context.Context, id model.TokenID) (*model.APIToken, error)
	ListTokens(ctx context.Context, userID model.UserID) ([]*model.APIToken, error)
	PutToken(ctx context.Context, token *model.APIToken) error
	DeleteToken(ctx context.Context, id model.TokenID) error
}

type GroupRepository interface {
	GetGroup(ctx context.Context, id model.GroupID) (*model.Group, error)
	ListGroups(ctx context.Context) ([]*model.Group, error)
	ListGroupsByMember(ctx context.Context, userID model.UserID) ([]*model.Group, error)
	PutGroup(ctx context.Context, group *model.Group) error
	DeleteGroup(ctx context.Context, id model.GroupID) error
}

type WorkspaceRepository interface {
	GetWorkspace(ctx context.Context, id model.WorkspaceID) (*model.Workspace, error)
	ListWorkspaces(ctx context.Context) ([]*model.Workspace, error)
	PutWorkspace(ctx context.Context, ws *model.Workspace) error
}

type PaperRepository interface {
	// CreatePaper stores a new paper with no blocks. It fails with
	// model.ErrAlreadyExists if the ID is taken.
	CreatePaper(ctx context.Context, paper *model.Paper) error
	GetPaper(ctx context.Context, ws model.WorkspaceID, id model.PaperID) (*model.Paper, error)
	ListPapers(ctx context.Context, ws model.WorkspaceID) ([]*model.Paper, error)
	// UpdatePaperAccess replaces the access settings of an existing paper.
	UpdatePaperAccess(ctx context.Context, ws model.WorkspaceID, id model.PaperID, access model.PaperAccess, updatedBy model.Actor, updatedAt time.Time) (*model.Paper, error)
	// DeletePaper removes the paper with its blocks, block versions, commits
	// and locks.
	DeletePaper(ctx context.Context, ws model.WorkspaceID, id model.PaperID) error

	GetBlock(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, id model.BlockID) (*model.Block, error)
	// ListBlocks returns every block of a paper, ordered by (ParentID, Order, ID).
	ListBlocks(ctx context.Context, ws model.WorkspaceID, paper model.PaperID) ([]*model.Block, error)
	// GetBlockVersion returns the snapshot of a block at a version. Snapshots
	// outlive the block, so a deleted block's history stays readable.
	GetBlockVersion(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, id model.BlockID, version int64) (*model.Block, error)
	// ListBlockVersions returns a block's snapshots, newest first.
	ListBlockVersions(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, id model.BlockID, limit int) ([]*model.Block, error)
	// ListCommits returns commits newest first. A non-empty before returns
	// the commits older than that commit (pagination cursor).
	ListCommits(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, before model.CommitID, limit int) ([]*model.Commit, error)

	// RunPaperTx runs fn in a transaction scoped to one paper. Writes staged
	// through tx become visible atomically when fn returns nil and are
	// discarded otherwise. Implementations may retry fn on contention, so fn
	// must not have side effects outside tx.
	RunPaperTx(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, fn func(tx PaperTx) error) error

	// WatchPaper streams the paper's changes (blocks, locks, the paper
	// record) from the moment it returns until ctx is done. The channel is
	// closed when the watch ends; it is also closed early if the watcher
	// falls behind, in which case the caller must reload the paper.
	WatchPaper(ctx context.Context, ws model.WorkspaceID, paper model.PaperID) (<-chan *model.Event, error)
}

// PaperTx is the view of one paper inside RunPaperTx. Reads observe writes
// staged earlier in the same transaction. Sibling order ties are broken by
// block ID everywhere.
type PaperTx interface {
	Paper() *model.Paper
	GetBlock(id model.BlockID) (*model.Block, error)
	// FirstAfter returns the first child of parent whose Order is strictly
	// greater than order ("" = the first child), or nil if there is none.
	FirstAfter(parent model.BlockID, order string) (*model.Block, error)
	// Last returns the last child of parent, or nil if it has none.
	Last(parent model.BlockID) (*model.Block, error)
	// Children returns all children of parent ordered by Order. Use it only
	// for containers with bounded children (table cells).
	Children(parent model.BlockID) ([]*model.Block, error)
	// PutBlock stores the block and a snapshot of it under its Version.
	PutBlock(block *model.Block) error
	DeleteBlock(id model.BlockID) error
	PutPaper(paper *model.Paper) error
	AddCommit(commit *model.Commit) error
}

type LockRepository interface {
	// AcquireLock stores lock unless another holder has an active lock on
	// the same block, in which case it returns that lock with an error
	// wrapping model.ErrBlockLocked. Re-acquiring one's own lock renews it.
	AcquireLock(ctx context.Context, lock *model.BlockLock, now time.Time) (*model.BlockLock, error)
	// ReleaseLock removes the lock if holderKey holds it and reports whether
	// a lock was removed.
	ReleaseLock(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, block model.BlockID, holderKey string) (bool, error)
	// ListLocks returns the active locks of a paper.
	ListLocks(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, now time.Time) ([]*model.BlockLock, error)
}
