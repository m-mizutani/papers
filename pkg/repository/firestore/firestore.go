// Package firestore implements interfaces.Repository on Cloud Firestore.
//
// Layout (see docs/design.md, "Firestore", for the rationale):
//
//	users/{userID}
//	users/{userID}/favorites/{wsID}_{paperID}
//	users/{userID}/recents/{wsID}
//	emails/{base64url(lower(email))}                 -> {UserID}
//	identities/{provider}/subjects/{subject}
//	sessions/{sessionID}                             TTL on ExpiresAt
//	tokens/{tokenID}
//	workspaces/{wsID}                                WorkspaceRecord
//	workspaces/{wsID}/papers/{paperID}
//	  .../blocks/{blockID}                           current state
//	  .../versions/{number, zero padded}             saved versions
//	  .../snapshots/{blockID}_{rev}                  block contents of versions
//	  .../events/{auto}                              change feed, TTL on ExpireAt
//	  .../attachments/{attachmentID}
//	workspaces/{wsID}/forwards/{paperID}             papers moved to another workspace
//	workspaces/{wsID}/search/{paperID}_{blockID|title}
//
// A transaction spans one workspace. Firestore transactions must perform
// every read before any write, so writes are staged and flushed when the
// transaction function returns; reads overlay the staged writes.
//
// The database must be a Firestore Enterprise edition database in Native
// mode: search uses the Search pipeline stage, and Enterprise creates no
// index automatically, so every query here must be covered by an index in
// RequiredIndexes (a query without one silently scans the collection).
//
// Models are persisted as-is (field names are the Go field names) and read
// back with DataTo. The only envelope is the feed entry, which carries its
// expiry time for the TTL policy.
package firestore

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"

	"cloud.google.com/go/firestore"
	"github.com/m-mizutani/goerr/v2"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

const (
	colUsers       = "users"
	colFavorites   = "favorites"
	colRecents     = "recents"
	colEmails      = "emails"
	colIdentities  = "identities"
	colSubjects    = "subjects"
	colSessions    = "sessions"
	colTokens      = "tokens"
	colWorkspaces  = "workspaces"
	colPapers      = "papers"
	colBlocks      = "blocks"
	colVersions    = "versions"
	colSnapshots   = "snapshots"
	colEvents      = "events"
	colAttachments = "attachments"
	colProperties  = "properties"
	colForwards    = "forwards"
	colJobs        = "jobs"
	colJobItems    = "items"
)

type Repository struct {
	client         *firestore.Client
	eventBuffer    int
	searchLanguage string
}

var _ interfaces.Repository = (*Repository)(nil)

type Option func(*Repository)

// WithEventBuffer sets how many events a watcher may lag behind before its
// watch is closed (default 256).
func WithEventBuffer(n int) Option { return func(r *Repository) { r.eventBuffer = n } }

// WithSearchLanguage sets the BCP-47 language code of search queries, such
// as "ja". Empty (the default) lets Firestore detect it.
func WithSearchLanguage(code string) Option { return func(r *Repository) { r.searchLanguage = code } }

func New(ctx context.Context, projectID, databaseID string, opts ...Option) (*Repository, error) {
	if projectID == "" {
		return nil, goerr.New("firestore project ID is required")
	}
	if databaseID == "" {
		databaseID = firestore.DefaultDatabaseID
	}
	client, err := firestore.NewClientWithDatabase(ctx, projectID, databaseID)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to create firestore client", goerr.V("project_id", projectID), goerr.V("database_id", databaseID))
	}
	r := &Repository{client: client, eventBuffer: 256}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

func (r *Repository) Close() error {
	if err := r.client.Close(); err != nil {
		return goerr.Wrap(err, "failed to close firestore client")
	}
	return nil
}

func isNotFound(err error) bool { return status.Code(err) == codes.NotFound }

func isAlreadyExists(err error) bool { return status.Code(err) == codes.AlreadyExists }

func notFound(kind string, id any) error {
	return goerr.Wrap(model.ErrNotFound, kind+" not found", goerr.V("id", id))
}

// validID rejects values Firestore cannot use as a document ID.
func validID(id string) error {
	if id == "" || id == "." || id == ".." || strings.Contains(id, "/") || (strings.HasPrefix(id, "__") && strings.HasSuffix(id, "__")) {
		return goerr.Wrap(model.ErrInvalidArgument, "invalid document id", goerr.V("id", id))
	}
	return nil
}

func decode[T any](snap *firestore.DocumentSnapshot, kind string) (*T, error) {
	var v T
	if err := snap.DataTo(&v); err != nil {
		return nil, goerr.Wrap(err, "failed to decode "+kind, goerr.V("id", snap.Ref.ID))
	}
	return &v, nil
}

// get reads one document into a new T, mapping NotFound to model.ErrNotFound.
func get[T any](ctx context.Context, doc *firestore.DocumentRef, kind string) (*T, error) {
	if err := validID(doc.ID); err != nil {
		return nil, notFound(kind, doc.ID)
	}
	snap, err := doc.Get(ctx)
	if err != nil {
		if isNotFound(err) {
			return nil, notFound(kind, doc.ID)
		}
		return nil, goerr.Wrap(err, "failed to get "+kind, goerr.V("id", doc.ID))
	}
	return decode[T](snap, kind)
}

// getAll reads many documents in one round trip, omitting missing ones.
func getAll[T any](ctx context.Context, client *firestore.Client, refs []*firestore.DocumentRef, kind string) ([]*T, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	snaps, err := client.GetAll(ctx, refs)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get "+kind)
	}
	return decodeSnaps[T](snaps, kind)
}

func decodeSnaps[T any](snaps []*firestore.DocumentSnapshot, kind string) ([]*T, error) {
	var out []*T
	for _, s := range snaps {
		if !s.Exists() {
			continue
		}
		v, err := decode[T](s, kind)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// all reads every document of an iterator.
func all[T any](it *firestore.DocumentIterator, kind string) ([]*T, error) {
	defer it.Stop()
	var out []*T
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list "+kind)
		}
		v, err := decode[T](snap, kind)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
}

func emailKey(email string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.ToLower(strings.TrimSpace(email))))
}

func (r *Repository) workspaceDoc(ws model.WorkspaceID) *firestore.DocumentRef {
	return r.client.Collection(colWorkspaces).Doc(string(ws))
}

func (r *Repository) papersCol(ws model.WorkspaceID) *firestore.CollectionRef {
	return r.workspaceDoc(ws).Collection(colPapers)
}

func (r *Repository) paperDoc(ws model.WorkspaceID, paper model.PaperID) *firestore.DocumentRef {
	return r.papersCol(ws).Doc(string(paper))
}

// validKeys reports whether every key can name a document.
func validKeys(keys ...string) bool {
	for _, k := range keys {
		if validID(k) != nil {
			return false
		}
	}
	return true
}
