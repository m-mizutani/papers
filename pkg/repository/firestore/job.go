package firestore

import (
	"context"
	"slices"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func (r *Repository) jobsCol(ws model.WorkspaceID) *firestore.CollectionRef {
	return r.workspaceDoc(ws).Collection(colJobs)
}

func (r *Repository) itemsCol(ws model.WorkspaceID, job model.JobID) *firestore.CollectionRef {
	return r.jobsCol(ws).Doc(string(job)).Collection(colJobItems)
}

func (r *Repository) GetJob(ctx context.Context, ws model.WorkspaceID, id model.JobID) (*model.Job, error) {
	if !validKeys(string(ws), string(id)) {
		return nil, notFound("job", id)
	}
	return get[model.Job](ctx, r.jobsCol(ws).Doc(string(id)), "job")
}

func (r *Repository) ListRunnableJobs(ctx context.Context, ws model.WorkspaceID, t time.Time, limit int) ([]*model.Job, error) {
	if !validKeys(string(ws)) {
		return nil, nil
	}
	q := r.jobsCol(ws).Where("State", "in", []string{string(model.JobQueued), string(model.JobRunning)}).
		Where("LeaseUntil", "<", t).OrderBy("LeaseUntil", firestore.Asc)
	if limit > 0 {
		q = q.Limit(limit)
	}
	return all[model.Job](q.Documents(ctx), "jobs")
}

func (r *Repository) ListUnexpandedJobItems(ctx context.Context, ws model.WorkspaceID, job model.JobID, limit int) ([]*model.JobItem, error) {
	if !validKeys(string(ws), string(job)) {
		return nil, nil
	}
	q := r.itemsCol(ws, job).Where("Expanded", "==", false).OrderBy("Depth", firestore.Asc)
	if limit > 0 {
		q = q.Limit(limit)
	}
	return all[model.JobItem](q.Documents(ctx), "job items")
}

func (r *Repository) ListJobItemsAt(ctx context.Context, ws model.WorkspaceID, job model.JobID, stage, limit int) ([]*model.JobItem, error) {
	if !validKeys(string(ws), string(job)) {
		return nil, nil
	}
	q := r.itemsCol(ws, job).Where("Stage", "==", stage).OrderBy("Depth", firestore.Asc)
	if limit > 0 {
		q = q.Limit(limit)
	}
	return all[model.JobItem](q.Documents(ctx), "job items")
}

func (r *Repository) GetJobItems(ctx context.Context, ws model.WorkspaceID, job model.JobID, ids []model.PaperID) ([]*model.JobItem, error) {
	if !validKeys(string(ws), string(job)) {
		return nil, nil
	}
	var refs []*firestore.DocumentRef
	for _, id := range ids {
		if validKeys(string(id)) {
			refs = append(refs, r.itemsCol(ws, job).Doc(string(id)))
		}
	}
	return getAll[model.JobItem](ctx, r.client, refs, "job items")
}

func (r *Repository) ListChildrenOf(ctx context.Context, ws model.WorkspaceID, parents []model.PaperID) ([]*model.Paper, error) {
	if !validKeys(string(ws)) {
		return nil, nil
	}
	var ids []string
	for _, p := range parents {
		if validKeys(string(p)) && !slices.Contains(ids, string(p)) {
			ids = append(ids, string(p))
		}
	}
	var out []*model.Paper
	for chunk := range slices.Chunk(ids, maxIn) {
		q := r.papersCol(ws).Where("ParentID", "in", chunk).OrderBy("Title", firestore.Asc)
		got, err := all[model.Paper](q.Documents(ctx), "papers")
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	slices.SortFunc(out, comparePapers)
	return out, nil
}

// --- in a transaction

func (t *fsTx) GetJob(id model.JobID) (*model.Job, error) {
	if j, ok := t.jobs[id]; ok {
		return j.Clone(), nil
	}
	if !validKeys(string(id)) {
		return nil, notFound("job", id)
	}
	snap, err := t.tx.Get(t.r.jobsCol(t.ws).Doc(string(id)))
	if err != nil {
		if isNotFound(err) {
			return nil, notFound("job", id)
		}
		return nil, goerr.Wrap(err, "failed to get job", goerr.V("job_id", id))
	}
	return decode[model.Job](snap, "job")
}

func (t *fsTx) PutJob(j *model.Job) error {
	if err := j.Validate(); err != nil {
		return err
	}
	if j.WorkspaceID != t.ws {
		return goerr.Wrap(model.ErrInvalidArgument, "job belongs to another workspace")
	}
	t.jobs[j.ID] = j.Clone()
	return nil
}

func (t *fsTx) GetJobItems(job model.JobID, ids []model.PaperID) ([]*model.JobItem, error) {
	var refs []*firestore.DocumentRef
	byID := map[model.PaperID]*model.JobItem{}
	for _, id := range ids {
		if it, ok := t.items[job][id]; ok {
			byID[id] = it.Clone()
		} else if validKeys(string(job), string(id)) {
			refs = append(refs, t.r.itemsCol(t.ws, job).Doc(string(id)))
		}
	}
	if len(refs) > 0 {
		snaps, err := t.tx.GetAll(refs)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to get job items")
		}
		got, err := decodeSnaps[model.JobItem](snaps, "job item")
		if err != nil {
			return nil, err
		}
		for _, it := range got {
			byID[it.PaperID] = it
		}
	}
	var out []*model.JobItem
	for _, id := range ids {
		if it, ok := byID[id]; ok {
			out = append(out, it)
		}
	}
	return out, nil
}

func (t *fsTx) PutJobItem(it *model.JobItem) error {
	if err := it.Validate(); err != nil {
		return err
	}
	if it.WorkspaceID != t.ws || !validKeys(string(it.JobID), string(it.PaperID)) {
		return goerr.Wrap(model.ErrInvalidArgument, "job item belongs to another workspace")
	}
	if t.items[it.JobID] == nil {
		t.items[it.JobID] = map[model.PaperID]*model.JobItem{}
	}
	t.items[it.JobID][it.PaperID] = it.Clone()
	return nil
}

func (t *fsTx) flushJobs() error {
	for id, j := range t.jobs {
		if err := t.tx.Set(t.r.jobsCol(t.ws).Doc(string(id)), j); err != nil {
			return goerr.Wrap(err, "failed to stage job")
		}
	}
	for job, items := range t.items {
		for id, it := range items {
			if err := t.tx.Set(t.r.itemsCol(t.ws, job).Doc(string(id)), it); err != nil {
				return goerr.Wrap(err, "failed to stage job item")
			}
		}
	}
	return nil
}
