package memory

import (
	"cmp"
	"context"
	"slices"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func (d *paperData) indexTitle() { d.setEntry("", model.NewTitleSearchEntry(d.paper)) }

func (d *paperData) indexBlock(b *model.Block) { d.setEntry(b.ID, model.NewBlockSearchEntry(b)) }

func (d *paperData) setEntry(id model.BlockID, e *model.SearchEntry) {
	if e == nil {
		delete(d.search, id)
		return
	}
	d.search[id] = e
}

// Search scans the workspace's search entries. The memory backend has no
// relevance model: title entries come first, then the most recently updated.
func (r *Repository) Search(_ context.Context, ws model.WorkspaceID, q model.SearchQuery, limit int) ([]*model.SearchEntry, error) {
	if limit < 1 || limit > model.MaxSearchLimit {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "search limit out of range", goerr.V("limit", limit))
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*model.SearchEntry
	for k, d := range r.papers {
		if k.ws != ws {
			continue
		}
		for _, e := range d.search {
			if q.Matches(e.Text) {
				out = append(out, clone(e))
			}
		}
	}
	slices.SortFunc(out, func(a, b *model.SearchEntry) int {
		if (a.BlockID == "") != (b.BlockID == "") {
			if a.BlockID == "" {
				return -1
			}
			return 1
		}
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		return cmp.Or(cmp.Compare(a.PaperID, b.PaperID), cmp.Compare(a.BlockID, b.BlockID))
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
