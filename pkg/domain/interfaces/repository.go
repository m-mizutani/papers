package interfaces

import (
	"context"
	"io"
	"time"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

// Repository is the storage behind papers. Every implementation must pass
// the contract tests in pkg/repository.
type Repository interface {
	UserRepository
	SessionRepository
	TokenRepository
	WorkspaceRepository
	PaperRepository
	SearchRepository
	FavoriteRepository
	AttachmentRepository
	JobRepository
	Close() error
}

type UserRepository interface {
	GetUser(ctx context.Context, id model.UserID) (*model.User, error)
	GetUsers(ctx context.Context, ids []model.UserID) ([]*model.User, error)
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

// WorkspaceRepository keeps the little that is stored about configured
// workspaces.
type WorkspaceRepository interface {
	GetWorkspaceRecord(ctx context.Context, ws model.WorkspaceID) (*model.WorkspaceRecord, error)
	// EnsureHome creates the workspace record and its home paper if the
	// record does not exist yet, atomically. Calling it again returns the
	// existing record.
	EnsureHome(ctx context.Context, ws model.WorkspaceID, home *model.Paper) (*model.WorkspaceRecord, error)
	// ListWorkspaceRecords lists every workspace that has stored data,
	// including ones no longer in the configuration.
	ListWorkspaceRecords(ctx context.Context) ([]*model.WorkspaceRecord, error)
	GetForward(ctx context.Context, ws model.WorkspaceID, paper model.PaperID) (*model.Forward, error)
}

// PaperRepository stores papers, blocks, versions and the change feed.
type PaperRepository interface {
	GetPaper(ctx context.Context, ws model.WorkspaceID, id model.PaperID) (*model.Paper, error)
	// GetPapers reads many papers in one round trip. Missing ones are
	// omitted.
	GetPapers(ctx context.Context, ws model.WorkspaceID, ids []model.PaperID) ([]*model.Paper, error)
	// ListChildren lists the papers whose ParentID is parent.
	ListChildren(ctx context.Context, ws model.WorkspaceID, parent model.PaperID) ([]*model.Paper, error)
	// ListChildrenOf lists the papers whose ParentID is any of parents.
	ListChildrenOf(ctx context.Context, ws model.WorkspaceID, parents []model.PaperID) ([]*model.Paper, error)
	// ListPapers reads a page of the workspace's papers: only those
	// without a parent when q.Unfiled, only q.Creator's when set, ordered
	// by q.Sort descending and then by ID descending, after q.Cursor. It
	// returns the cursor of the next page, or "" when there is none.
	ListPapers(ctx context.Context, ws model.WorkspaceID, q model.PaperQuery) ([]*model.Paper, string, error)
	// GetPapersIn reads papers of several workspaces in one round trip.
	// Missing ones are omitted.
	GetPapersIn(ctx context.Context, keys []model.PaperKey) ([]*model.Paper, error)
	// ListTrash lists the owner's papers that are in the trash.
	ListTrash(ctx context.Context, ws model.WorkspaceID, owner model.UserID) ([]*model.Paper, error)
	// ListTrashedBefore lists papers whose DeletedAt is before t.
	ListTrashedBefore(ctx context.Context, ws model.WorkspaceID, t time.Time) ([]*model.Paper, error)
	// PurgePaper removes a paper and everything under it: blocks, versions,
	// snapshots, events, property values, search entries and attachment
	// metadata.
	PurgePaper(ctx context.Context, ws model.WorkspaceID, id model.PaperID) error

	GetBlocks(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, ids []model.BlockID) ([]*model.Block, error)
	ListBlocks(ctx context.Context, ws model.WorkspaceID, paper model.PaperID) ([]*model.Block, error)
	// ListPaperBlocks lists the paper blocks of a paper's body in order:
	// its children as they are placed.
	ListPaperBlocks(ctx context.Context, ws model.WorkspaceID, paper model.PaperID) ([]*model.Block, error)
	// ListBlocksRange reads up to limit top-level blocks whose order is
	// after the cursor, with the cells of any tables among them. It returns
	// the cursor for the next range, or "" at the end.
	ListBlocksRange(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, after string, limit int) ([]*model.Block, string, error)

	ListVersions(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, before, limit int) ([]*model.Version, error)
	GetVersion(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, number int) (*model.Version, error)
	GetSnapshots(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, refs []model.SnapshotRef) ([]*model.BlockSnapshot, error)

	// GetPropertyValues reads property values by (item, property) in one
	// round trip. Missing ones are omitted.
	GetPropertyValues(ctx context.Context, ws model.WorkspaceID, refs []model.PropertyValueRef) ([]*model.PropertyValue, error)
	// ListItems reads the items of a database (papers whose ParentID is
	// db) in the order q asks for. It returns the cursor of the next read,
	// or "" at the end.
	ListItems(ctx context.Context, ws model.WorkspaceID, db model.PaperID, q model.ItemQuery) ([]*model.Paper, string, error)
	// QueryPropertyValues reads the values of one property of a database
	// per q. It returns the cursor of the next read, or "" at the end.
	QueryPropertyValues(ctx context.Context, ws model.WorkspaceID, db model.PaperID, q model.PropertyValueQuery) ([]*model.PropertyValue, string, error)

	// RunTx runs fn in one transaction over the workspace. Reads see the
	// transaction's own writes. Writes are applied together or not at all.
	RunTx(ctx context.Context, ws model.WorkspaceID, fn func(tx Tx) error) error

	// PutEvent appends a feed entry outside a transaction (presence).
	PutEvent(ctx context.Context, ev *model.Event) error
	// ListEvents lists feed entries of a paper at or after since.
	ListEvents(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, since time.Time) ([]*model.Event, error)
	// Watch delivers feed entries of a paper written after the call.
	Watch(ctx context.Context, ws model.WorkspaceID, paper model.PaperID) (<-chan *model.Event, error)
}

// Tx is a transaction over one workspace.
type Tx interface {
	GetPaper(id model.PaperID) (*model.Paper, error)
	GetPapers(ids []model.PaperID) ([]*model.Paper, error)
	PutPaper(paper *model.Paper) error

	GetBlock(paper model.PaperID, id model.BlockID) (*model.Block, error)
	GetBlocks(paper model.PaperID, ids []model.BlockID) ([]*model.Block, error)
	FirstAfter(paper model.PaperID, parent model.BlockID, order string) (*model.Block, error)
	Last(paper model.PaperID, parent model.BlockID) (*model.Block, error)
	Children(paper model.PaperID, parent model.BlockID) ([]*model.Block, error)
	ListBlocks(paper model.PaperID) ([]*model.Block, error)
	CountBlocks(paper model.PaperID) (int, error)
	PutBlock(block *model.Block) error
	DeleteBlock(paper model.PaperID, id model.BlockID) error

	GetVersion(paper model.PaperID, number int) (*model.Version, error)
	PutVersion(v *model.Version) error
	GetSnapshots(paper model.PaperID, refs []model.SnapshotRef) ([]*model.BlockSnapshot, error)
	PutSnapshot(paper model.PaperID, s *model.BlockSnapshot) error

	// GetPropertyValues reads property values by (item, property).
	GetPropertyValues(refs []model.PropertyValueRef) ([]*model.PropertyValue, error)
	// ListItemPropertyValues lists one item's property values.
	ListItemPropertyValues(item model.PaperID) ([]*model.PropertyValue, error)
	PutPropertyValue(v *model.PropertyValue) error
	DeletePropertyValue(item model.PaperID, property model.PropertyID) error

	PutForward(f *model.Forward) error
	// PutAttachment stores an attachment's metadata with the rest of the
	// transaction (its bytes are put in the AttachmentStore beforehand).
	PutAttachment(a *model.Attachment) error
	AddEvent(ev *model.Event) error

	GetJob(id model.JobID) (*model.Job, error)
	PutJob(j *model.Job) error
	GetJobItems(job model.JobID, ids []model.PaperID) ([]*model.JobItem, error)
	PutJobItem(it *model.JobItem) error
}

// JobRepository stores background jobs and the papers each works on.
type JobRepository interface {
	GetJob(ctx context.Context, ws model.WorkspaceID, id model.JobID) (*model.Job, error)
	// ListRunnableJobs lists unfinished jobs whose lease ended before t.
	ListRunnableJobs(ctx context.Context, ws model.WorkspaceID, t time.Time, limit int) ([]*model.Job, error)
	// ListUnexpandedJobItems lists items whose children are not collected
	// yet, shallowest first.
	ListUnexpandedJobItems(ctx context.Context, ws model.WorkspaceID, job model.JobID, limit int) ([]*model.JobItem, error)
	// ListJobItemsAt lists items at the stage, shallowest first.
	ListJobItemsAt(ctx context.Context, ws model.WorkspaceID, job model.JobID, stage, limit int) ([]*model.JobItem, error)
	GetJobItems(ctx context.Context, ws model.WorkspaceID, job model.JobID, ids []model.PaperID) ([]*model.JobItem, error)
}

type SearchRepository interface {
	Search(ctx context.Context, ws model.WorkspaceID, q model.SearchQuery, limit int) ([]*model.SearchEntry, error)
}

// FavoriteRepository keeps what each user keeps at hand: favorites and
// recently opened papers.
type FavoriteRepository interface {
	// ListFavorites lists the user's favorites in the workspace by order
	// key.
	ListFavorites(ctx context.Context, user model.UserID, ws model.WorkspaceID) ([]*model.Favorite, error)
	PutFavorite(ctx context.Context, f *model.Favorite) error
	DeleteFavorite(ctx context.Context, user model.UserID, ws model.WorkspaceID, paper model.PaperID) error
	GetRecent(ctx context.Context, user model.UserID, ws model.WorkspaceID) (*model.Recent, error)
	// GetRecents reads the user's recent lists of several workspaces in
	// one round trip. Workspaces without one are omitted.
	GetRecents(ctx context.Context, user model.UserID, wss []model.WorkspaceID) ([]*model.Recent, error)
	PutRecent(ctx context.Context, r *model.Recent) error
}

type AttachmentRepository interface {
	GetAttachment(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, id model.AttachmentID) (*model.Attachment, error)
	FindAttachmentByHash(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, sha256 string) (*model.Attachment, error)
	PutAttachment(ctx context.Context, a *model.Attachment) error
}

// AttachmentStore keeps attachment bytes: Cloud Storage in production, the
// memory or a local directory in development.
type AttachmentStore interface {
	Put(ctx context.Context, key, contentType string, r io.Reader) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	// DeletePrefix deletes every object whose key starts with prefix.
	DeletePrefix(ctx context.Context, prefix string) error
	// SignedURL returns a short-lived URL to read the object, or "" when
	// the store cannot sign; the server then streams the bytes itself.
	SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// Workspaces is the configured set of workspaces and their access rules.
type Workspaces interface {
	// List returns the workspaces in display order.
	List() []*model.Workspace
	Get(id model.WorkspaceID) (*model.Workspace, bool)
	// Groups returns the display groups in order. Workspaces without a
	// group are not listed in any.
	Groups() []model.WorkspaceGroup
	// Allow reports whether the user may use the workspace. An error means
	// the decision could not be made; callers deny.
	Allow(ctx context.Context, user *model.User, ws model.WorkspaceID) (bool, error)
}
