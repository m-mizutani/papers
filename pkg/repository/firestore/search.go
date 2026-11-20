package firestore

import (
	"context"
	"errors"
	"strings"

	"cloud.google.com/go/firestore"
	"github.com/m-mizutani/goerr/v2"
	"google.golang.org/api/iterator"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

// Search entries live in one collection per workspace,
//
//	workspaces/{wsID}/search/{paperID}_{blockID}   block entry
//	workspaces/{wsID}/search/{paperID}_title       title entry
//
// so that a search is scoped by its collection path: the Search stage must
// be the first stage of a pipeline, so a workspace filter after it would
// run on an already truncated result. The text index covers the Text field
// of the "search" collection group (see indexes.go). Entries are written in
// the same transaction as the block or paper they describe.

const (
	colSearch        = "search"
	searchTitleEntry = "title"
)

func (r *Repository) searchCol(ws model.WorkspaceID) *firestore.CollectionRef {
	return r.workspaceDoc(ws).Collection(colSearch)
}

func (r *Repository) searchDoc(ws model.WorkspaceID, paper model.PaperID, block model.BlockID) *firestore.DocumentRef {
	suffix := string(block)
	if block == "" {
		suffix = searchTitleEntry
	}
	return r.searchCol(ws).Doc(string(paper) + "_" + suffix)
}

// stageSearchEntry writes e to ref, or deletes ref when e is nil (the block
// or title has no searchable text any more).
func stageSearchEntry(tx *firestore.Transaction, ref *firestore.DocumentRef, e *model.SearchEntry) error {
	if e == nil {
		if err := tx.Delete(ref); err != nil {
			return goerr.Wrap(err, "failed to stage search entry delete", goerr.V("path", ref.Path))
		}
		return nil
	}
	if err := tx.Set(ref, e); err != nil {
		return goerr.Wrap(err, "failed to stage search entry write", goerr.V("path", ref.Path))
	}
	return nil
}

// paperSearchRefs lists the search entries of a paper by document ID range,
// which needs no index.
func (r *Repository) paperSearchRefs(ctx context.Context, ws model.WorkspaceID, paper model.PaperID) ([]*firestore.DocumentRef, error) {
	col := r.searchCol(ws)
	prefix := string(paper) + "_"
	// '`' is the byte after '_', so the range covers exactly the prefix.
	end := string(paper) + "`"
	it := col.Where(firestore.DocumentID, ">=", col.Doc(prefix)).
		Where(firestore.DocumentID, "<", col.Doc(end)).
		Select().Documents(ctx)
	defer it.Stop()
	var refs []*firestore.DocumentRef
	for {
		snap, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return refs, nil
		}
		if err != nil {
			return nil, goerr.Wrap(err, "failed to list search entries", goerr.V("paper_id", paper))
		}
		refs = append(refs, snap.Ref)
	}
}

// searchDSL turns parsed terms into the engine's query language: every term
// is quoted, so operators typed by users ("-", "OR", "field:") are matched
// literally and all terms are required (space-separated terms are ANDed).
func searchDSL(q model.SearchQuery) string {
	parts := make([]string, 0, len(q.Terms))
	for _, t := range q.Terms {
		t = strings.ReplaceAll(t, `"`, " ")
		t = strings.ReplaceAll(t, `\`, " ")
		if t = strings.Join(strings.Fields(t), " "); t != "" {
			parts = append(parts, `"`+t+`"`)
		}
	}
	return strings.Join(parts, " ")
}

// Search runs a full-text search on the workspace's search entries with
// the Firestore Enterprise Search stage, ordered by relevance score.
func (r *Repository) Search(ctx context.Context, ws model.WorkspaceID, q model.SearchQuery, limit int) ([]*model.SearchEntry, error) {
	if limit < 1 || limit > model.MaxSearchLimit {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "search limit out of range", goerr.V("limit", limit))
	}
	if validID(string(ws)) != nil {
		return nil, nil
	}
	dsl := searchDSL(q)
	if dsl == "" {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "query is required")
	}
	opts := []firestore.SearchOption{
		firestore.WithSearchQuery(firestore.DocumentMatches(dsl)),
		firestore.WithSearchSort(firestore.Score().Descending()),
		firestore.WithSearchLimit(int64(limit)),
	}
	if r.searchLanguage != "" {
		opts = append(opts, firestore.WithSearchLanguageCode(r.searchLanguage))
	}
	path := colWorkspaces + "/" + string(ws) + "/" + colSearch
	it := r.client.Pipeline().Collection(path).Search(opts...).Execute(ctx).Results()
	defer it.Stop()

	var out []*model.SearchEntry
	for {
		res, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, goerr.Wrap(err, "failed to run search", goerr.V("workspace_id", ws))
		}
		var e model.SearchEntry
		if err := res.DataTo(&e); err != nil {
			return nil, goerr.Wrap(err, "failed to decode search entry")
		}
		out = append(out, &e)
	}
}
