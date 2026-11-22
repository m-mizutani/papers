package memory

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

type jobKey struct {
	ws model.WorkspaceID
	id model.JobID
}

func (r *Repository) GetJob(_ context.Context, ws model.WorkspaceID, id model.JobID) (*model.Job, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	j, ok := r.jobs[jobKey{ws, id}]
	if !ok {
		return nil, notFound("job", id)
	}
	return j.Clone(), nil
}

func (r *Repository) ListRunnableJobs(_ context.Context, ws model.WorkspaceID, t time.Time, limit int) ([]*model.Job, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*model.Job
	for k, j := range r.jobs {
		if k.ws == ws && !j.State.Finished() && j.LeaseUntil.Before(t) {
			out = append(out, j.Clone())
		}
	}
	slices.SortFunc(out, func(a, b *model.Job) int {
		return cmp.Or(a.LeaseUntil.Compare(b.LeaseUntil), cmp.Compare(a.ID, b.ID))
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *Repository) listItems(ws model.WorkspaceID, job model.JobID, keep func(*model.JobItem) bool, limit int) []*model.JobItem {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*model.JobItem
	for _, it := range r.jobItems[jobKey{ws, job}] {
		if keep(it) {
			out = append(out, it.Clone())
		}
	}
	slices.SortFunc(out, func(a, b *model.JobItem) int {
		return cmp.Or(cmp.Compare(a.Depth, b.Depth), cmp.Compare(a.PaperID, b.PaperID))
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (r *Repository) ListUnexpandedJobItems(_ context.Context, ws model.WorkspaceID, job model.JobID, limit int) ([]*model.JobItem, error) {
	return r.listItems(ws, job, func(it *model.JobItem) bool { return !it.Expanded }, limit), nil
}

func (r *Repository) ListJobItemsAt(_ context.Context, ws model.WorkspaceID, job model.JobID, stage, limit int) ([]*model.JobItem, error) {
	return r.listItems(ws, job, func(it *model.JobItem) bool { return it.Stage == stage }, limit), nil
}

func (r *Repository) GetJobItems(_ context.Context, ws model.WorkspaceID, job model.JobID, ids []model.PaperID) ([]*model.JobItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := r.jobItems[jobKey{ws, job}]
	var out []*model.JobItem
	for _, id := range ids {
		if it, ok := items[id]; ok {
			out = append(out, it.Clone())
		}
	}
	return out, nil
}

// --- in a transaction

func (t *memTx) GetJob(id model.JobID) (*model.Job, error) {
	if j, ok := t.jobs[id]; ok {
		return j.Clone(), nil
	}
	return t.r.GetJob(context.Background(), t.ws, id)
}

func (t *memTx) PutJob(j *model.Job) error {
	if err := j.Validate(); err != nil {
		return err
	}
	if j.WorkspaceID != t.ws {
		return goerr.Wrap(model.ErrInvalidArgument, "job belongs to another workspace")
	}
	t.jobs[j.ID] = j.Clone()
	return nil
}

func (t *memTx) GetJobItems(job model.JobID, ids []model.PaperID) ([]*model.JobItem, error) {
	stored, err := t.r.GetJobItems(context.Background(), t.ws, job, ids)
	if err != nil {
		return nil, err
	}
	byID := map[model.PaperID]*model.JobItem{}
	for _, it := range stored {
		byID[it.PaperID] = it
	}
	for id, it := range t.items[job] {
		byID[id] = it.Clone()
	}
	var out []*model.JobItem
	for _, id := range ids {
		if it, ok := byID[id]; ok {
			out = append(out, it)
		}
	}
	return out, nil
}

func (t *memTx) PutJobItem(it *model.JobItem) error {
	if err := it.Validate(); err != nil {
		return err
	}
	if it.WorkspaceID != t.ws {
		return goerr.Wrap(model.ErrInvalidArgument, "job item belongs to another workspace")
	}
	if t.items[it.JobID] == nil {
		t.items[it.JobID] = map[model.PaperID]*model.JobItem{}
	}
	t.items[it.JobID][it.PaperID] = it.Clone()
	return nil
}
