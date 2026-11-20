package firestore

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

func (r *Repository) blocksCol(ws model.WorkspaceID, paper model.PaperID) *firestore.CollectionRef {
	return r.paperDoc(ws, paper).Collection(colBlocks)
}

func (r *Repository) versionDoc(ws model.WorkspaceID, paper model.PaperID, block model.BlockID, version int64) *firestore.DocumentRef {
	return r.blocksCol(ws, paper).Doc(string(block)).Collection(colVersions).Doc(strconv.FormatInt(version, 10))
}

func (r *Repository) commitsCol(ws model.WorkspaceID, paper model.PaperID) *firestore.CollectionRef {
	return r.paperDoc(ws, paper).Collection(colCommits)
}

func (r *Repository) locksCol(ws model.WorkspaceID, paper model.PaperID) *firestore.CollectionRef {
	return r.paperDoc(ws, paper).Collection(colLocks)
}

func (r *Repository) CreatePaper(ctx context.Context, paper *model.Paper) error {
	if err := paper.Validate(); err != nil {
		return err
	}
	if err := validID(string(paper.ID)); err != nil {
		return err
	}
	if err := validID(string(paper.WorkspaceID)); err != nil {
		return err
	}
	err := r.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		if err := tx.Create(r.paperDoc(paper.WorkspaceID, paper.ID), paper); err != nil {
			return err
		}
		return stageSearchEntry(tx, r.searchDoc(paper.WorkspaceID, paper.ID, ""), model.NewTitleSearchEntry(paper))
	}, firestore.MaxAttempts(1))
	if err != nil {
		if isAlreadyExists(err) {
			return goerr.Wrap(model.ErrAlreadyExists, "paper already exists", goerr.V("id", paper.ID))
		}
		return goerr.Wrap(err, "failed to create paper")
	}
	return nil
}

func (r *Repository) GetPaper(ctx context.Context, ws model.WorkspaceID, id model.PaperID) (*model.Paper, error) {
	if validID(string(ws)) != nil {
		return nil, notFound("paper", id)
	}
	return get[model.Paper](ctx, r.paperDoc(ws, id), "paper")
}

func (r *Repository) ListPapers(ctx context.Context, ws model.WorkspaceID) ([]*model.Paper, error) {
	if validID(string(ws)) != nil {
		return nil, nil
	}
	return all[model.Paper](r.workspaceDoc(ws).Collection(colPapers).OrderBy(firestore.DocumentID, firestore.Asc).Documents(ctx), "papers")
}

func (r *Repository) UpdatePaperAccess(ctx context.Context, ws model.WorkspaceID, id model.PaperID, access model.PaperAccess, updatedBy model.Actor, updatedAt time.Time) (*model.Paper, error) {
	if validID(string(ws)) != nil || validID(string(id)) != nil {
		return nil, notFound("paper", id)
	}
	doc := r.paperDoc(ws, id)
	var out *model.Paper
	err := r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(doc)
		if err != nil {
			if isNotFound(err) {
				return notFound("paper", id)
			}
			return goerr.Wrap(err, "failed to get paper")
		}
		var p model.Paper
		if err := snap.DataTo(&p); err != nil {
			return goerr.Wrap(err, "failed to decode paper")
		}
		p.Access = access
		p.UpdatedBy = updatedBy
		p.UpdatedAt = updatedAt
		if err := p.Validate(); err != nil {
			return err
		}
		out = &p
		return tx.Set(doc, &p)
	})
	if err != nil {
		return nil, goerr.Wrap(err, "failed to update paper access", goerr.V("paper_id", id))
	}
	return out, nil
}

// DeletePaper removes the paper document first, so readers and watchers
// see the paper gone at once, then sweeps its subcollections.
func (r *Repository) DeletePaper(ctx context.Context, ws model.WorkspaceID, id model.PaperID) error {
	if _, err := r.GetPaper(ctx, ws, id); err != nil {
		return err
	}
	doc := r.paperDoc(ws, id)
	if _, err := doc.Delete(ctx); err != nil {
		return goerr.Wrap(err, "failed to delete paper")
	}
	searchRefs, err := r.paperSearchRefs(ctx, ws, id)
	if err != nil {
		return err
	}

	bw := r.client.BulkWriter(ctx)
	var jobs []*firestore.BulkWriterJob
	del := func(d *firestore.DocumentRef) error {
		job, err := bw.Delete(d)
		if err != nil {
			return goerr.Wrap(err, "failed to enqueue delete", goerr.V("path", d.Path))
		}
		jobs = append(jobs, job)
		return nil
	}
	sweep := func(col *firestore.CollectionRef, nested func(*firestore.DocumentRef) error) error {
		refs, err := col.DocumentRefs(ctx).GetAll()
		if err != nil {
			return goerr.Wrap(err, "failed to list documents", goerr.V("collection", col.Path))
		}
		for _, ref := range refs {
			if nested != nil {
				if err := nested(ref); err != nil {
					return err
				}
			}
			if err := del(ref); err != nil {
				return err
			}
		}
		return nil
	}
	for _, ref := range searchRefs {
		if err = del(ref); err != nil {
			break
		}
	}
	if err == nil {
		err = sweep(doc.Collection(colBlocks), func(block *firestore.DocumentRef) error {
			return sweep(block.Collection(colVersions), nil)
		})
	}
	if err == nil {
		err = sweep(doc.Collection(colCommits), nil)
	}
	if err == nil {
		err = sweep(doc.Collection(colLocks), nil)
	}
	bw.End()
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if _, err := job.Results(); err != nil && !isNotFound(err) {
			return goerr.Wrap(err, "failed to delete paper contents")
		}
	}
	return nil
}

func (r *Repository) GetBlock(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, id model.BlockID) (*model.Block, error) {
	if validID(string(ws)) != nil || validID(string(paper)) != nil {
		return nil, notFound("block", id)
	}
	return get[model.Block](ctx, r.blocksCol(ws, paper).Doc(string(id)), "block")
}

func compareBlocks(a, b *model.Block) int {
	if c := cmp.Compare(a.ParentID, b.ParentID); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Order, b.Order); c != 0 {
		return c
	}
	return cmp.Compare(a.ID, b.ID)
}

func (r *Repository) ListBlocks(ctx context.Context, ws model.WorkspaceID, paper model.PaperID) ([]*model.Block, error) {
	if _, err := r.GetPaper(ctx, ws, paper); err != nil {
		return nil, err
	}
	blocks, err := all[model.Block](r.blocksCol(ws, paper).Documents(ctx), "blocks")
	if err != nil {
		return nil, err
	}
	slices.SortFunc(blocks, compareBlocks)
	return blocks, nil
}

func (r *Repository) GetBlockVersion(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, id model.BlockID, version int64) (*model.Block, error) {
	if validID(string(ws)) != nil || validID(string(paper)) != nil || validID(string(id)) != nil {
		return nil, notFound("block version", id)
	}
	return get[model.Block](ctx, r.versionDoc(ws, paper, id, version), "block version")
}

func (r *Repository) ListBlockVersions(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, id model.BlockID, limit int) ([]*model.Block, error) {
	if limit <= 0 {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "limit must be positive")
	}
	if validID(string(ws)) != nil || validID(string(paper)) != nil || validID(string(id)) != nil {
		return nil, nil
	}
	q := r.blocksCol(ws, paper).Doc(string(id)).Collection(colVersions).OrderBy("Version", firestore.Desc).Limit(limit)
	return all[model.Block](q.Documents(ctx), "block versions")
}

func (r *Repository) ListCommits(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, before model.CommitID, limit int) ([]*model.Commit, error) {
	if limit <= 0 {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "limit must be positive")
	}
	if _, err := r.GetPaper(ctx, ws, paper); err != nil {
		return nil, err
	}
	// Ordering by CreatedAt descending also orders ties by document ID
	// descending, which matches the memory implementation.
	q := r.commitsCol(ws, paper).OrderBy("CreatedAt", firestore.Desc).Limit(limit)
	if before != "" {
		if validID(string(before)) != nil {
			return nil, notFound("commit", before)
		}
		snap, err := r.commitsCol(ws, paper).Doc(string(before)).Get(ctx)
		if err != nil {
			if isNotFound(err) {
				return nil, notFound("commit", before)
			}
			return nil, goerr.Wrap(err, "failed to get cursor commit")
		}
		q = q.StartAfter(snap)
	}
	return all[model.Commit](q.Documents(ctx), "commits")
}

// --- transaction -------------------------------------------------------------

// fsTx stages every write and flushes them when fn returns, because
// Firestore transactions must perform all reads before any write. Reads
// overlay the staged writes so fn sees its own changes.
type fsTx struct {
	ctx     context.Context
	tx      *firestore.Transaction
	r       *Repository
	ws      model.WorkspaceID
	paperID model.PaperID
	paper   *model.Paper
	paperW  bool
	puts    map[model.BlockID]*model.Block
	deletes map[model.BlockID]struct{}
	cache   map[model.BlockID]*model.Block
	commits []*model.Commit
}

func (t *fsTx) Paper() *model.Paper {
	p := *t.paper
	p.Access.Bindings = slices.Clone(t.paper.Access.Bindings)
	return &p
}

func (t *fsTx) GetBlock(id model.BlockID) (*model.Block, error) {
	if _, ok := t.deletes[id]; ok {
		return nil, notFound("block", id)
	}
	if b, ok := t.puts[id]; ok {
		return b.Clone(), nil
	}
	if b, ok := t.cache[id]; ok {
		return b.Clone(), nil
	}
	if validID(string(id)) != nil {
		return nil, notFound("block", id)
	}
	snap, err := t.tx.Get(t.r.blocksCol(t.ws, t.paperID).Doc(string(id)))
	if err != nil {
		if isNotFound(err) {
			return nil, notFound("block", id)
		}
		return nil, goerr.Wrap(err, "failed to get block", goerr.V("block_id", id))
	}
	var b model.Block
	if err := snap.DataTo(&b); err != nil {
		return nil, goerr.Wrap(err, "failed to decode block")
	}
	t.cache[id] = &b
	return b.Clone(), nil
}

// query runs q in the transaction and merges staged writes for parent.
// Stored blocks that were staged (put or deleted) are replaced by their
// staged state.
func (t *fsTx) query(q firestore.Query, parent model.BlockID, keep func(*model.Block) bool) ([]*model.Block, error) {
	stored, err := all[model.Block](t.tx.Documents(q), "blocks")
	if err != nil {
		return nil, err
	}
	var out []*model.Block
	for _, b := range stored {
		t.cache[b.ID] = b
		if _, ok := t.puts[b.ID]; ok {
			continue
		}
		if _, ok := t.deletes[b.ID]; ok {
			continue
		}
		out = append(out, b.Clone())
	}
	for _, b := range t.puts {
		if b.ParentID == parent && keep(b) {
			out = append(out, b.Clone())
		}
	}
	slices.SortFunc(out, compareBlocks)
	return out, nil
}

// slack is how many extra rows to read so that staged writes hiding stored
// rows cannot leave the result short.
func (t *fsTx) slack() int { return 1 + len(t.puts) + len(t.deletes) }

func (t *fsTx) FirstAfter(parent model.BlockID, order string) (*model.Block, error) {
	q := t.r.blocksCol(t.ws, t.paperID).Where("ParentID", "==", string(parent))
	if order != "" {
		q = q.Where("Order", ">", order)
	}
	q = q.OrderBy("Order", firestore.Asc).Limit(t.slack())
	bs, err := t.query(q, parent, func(b *model.Block) bool { return b.Order > order })
	if err != nil || len(bs) == 0 {
		return nil, err
	}
	return bs[0], nil
}

func (t *fsTx) Last(parent model.BlockID) (*model.Block, error) {
	q := t.r.blocksCol(t.ws, t.paperID).Where("ParentID", "==", string(parent)).OrderBy("Order", firestore.Desc).Limit(t.slack())
	bs, err := t.query(q, parent, func(*model.Block) bool { return true })
	if err != nil || len(bs) == 0 {
		return nil, err
	}
	return bs[len(bs)-1], nil
}

func (t *fsTx) Children(parent model.BlockID) ([]*model.Block, error) {
	q := t.r.blocksCol(t.ws, t.paperID).Where("ParentID", "==", string(parent))
	return t.query(q, parent, func(*model.Block) bool { return true })
}

func (t *fsTx) PutBlock(block *model.Block) error {
	if err := block.Validate(); err != nil {
		return err
	}
	if err := validID(string(block.ID)); err != nil {
		return err
	}
	if block.WorkspaceID != t.ws || block.PaperID != t.paperID {
		return goerr.Wrap(model.ErrInvalidArgument, "block belongs to another paper", goerr.V("block_id", block.ID))
	}
	delete(t.deletes, block.ID)
	t.puts[block.ID] = block.Clone()
	return nil
}

func (t *fsTx) DeleteBlock(id model.BlockID) error {
	delete(t.puts, id)
	t.deletes[id] = struct{}{}
	return nil
}

func (t *fsTx) PutPaper(paper *model.Paper) error {
	if err := paper.Validate(); err != nil {
		return err
	}
	if paper.ID != t.paperID || paper.WorkspaceID != t.ws {
		return goerr.Wrap(model.ErrInvalidArgument, "cannot replace another paper in a paper transaction")
	}
	p := *paper
	t.paper = &p
	t.paperW = true
	return nil
}

func (t *fsTx) AddCommit(commit *model.Commit) error {
	if err := commit.Validate(); err != nil {
		return err
	}
	if commit.PaperID != t.paperID || commit.WorkspaceID != t.ws {
		return goerr.Wrap(model.ErrInvalidArgument, "commit belongs to another paper")
	}
	if err := validID(string(commit.ID)); err != nil {
		return err
	}
	t.commits = append(t.commits, commit)
	return nil
}

func (t *fsTx) flush() error {
	blocks := t.r.blocksCol(t.ws, t.paperID)
	for id := range t.deletes {
		if err := t.tx.Delete(blocks.Doc(string(id))); err != nil {
			return goerr.Wrap(err, "failed to stage block delete")
		}
		if err := stageSearchEntry(t.tx, t.r.searchDoc(t.ws, t.paperID, id), nil); err != nil {
			return err
		}
	}
	for id, b := range t.puts {
		if err := t.tx.Set(blocks.Doc(string(id)), b); err != nil {
			return goerr.Wrap(err, "failed to stage block write")
		}
		// Create (not Set): a version is written once, so a concurrent
		// writer that computed the same version aborts instead of
		// rewriting history.
		if err := t.tx.Create(t.r.versionDoc(t.ws, t.paperID, id, b.Version), b); err != nil {
			return goerr.Wrap(err, "failed to stage block version")
		}
		if err := stageSearchEntry(t.tx, t.r.searchDoc(t.ws, t.paperID, id), model.NewBlockSearchEntry(b)); err != nil {
			return err
		}
	}
	for _, c := range t.commits {
		if err := t.tx.Create(t.r.commitsCol(t.ws, t.paperID).Doc(string(c.ID)), c); err != nil {
			return goerr.Wrap(err, "failed to stage commit")
		}
	}
	if t.paperW {
		if err := t.tx.Set(t.r.paperDoc(t.ws, t.paperID), t.paper); err != nil {
			return goerr.Wrap(err, "failed to stage paper write")
		}
		if err := stageSearchEntry(t.tx, t.r.searchDoc(t.ws, t.paperID, ""), model.NewTitleSearchEntry(t.paper)); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) RunPaperTx(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, fn func(tx interfaces.PaperTx) error) error {
	if validID(string(ws)) != nil || validID(string(paper)) != nil {
		return notFound("paper", paper)
	}
	err := r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(r.paperDoc(ws, paper))
		if err != nil {
			if isNotFound(err) {
				return notFound("paper", paper)
			}
			return goerr.Wrap(err, "failed to get paper")
		}
		var p model.Paper
		if err := snap.DataTo(&p); err != nil {
			return goerr.Wrap(err, "failed to decode paper")
		}
		t := &fsTx{
			ctx: ctx, tx: tx, r: r, ws: ws, paperID: paper, paper: &p,
			puts: map[model.BlockID]*model.Block{}, deletes: map[model.BlockID]struct{}{}, cache: map[model.BlockID]*model.Block{},
		}
		if err := fn(t); err != nil {
			return err
		}
		return t.flush()
	})
	if err != nil {
		if isAlreadyExists(err) {
			return goerr.Wrap(model.ErrAlreadyExists, "a concurrent write created the same record", goerr.V("paper_id", paper))
		}
		return err
	}
	return nil
}
