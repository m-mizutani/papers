package firestore

import (
	"cmp"
	"context"
	"fmt"
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

func (r *Repository) versionsCol(ws model.WorkspaceID, paper model.PaperID) *firestore.CollectionRef {
	return r.paperDoc(ws, paper).Collection(colVersions)
}

func versionKey(n int) string { return fmt.Sprintf("%010d", n) }

func (r *Repository) snapshotDoc(ws model.WorkspaceID, paper model.PaperID, ref model.SnapshotRef) *firestore.DocumentRef {
	return r.paperDoc(ws, paper).Collection(colSnapshots).Doc(string(ref.BlockID) + "_" + strconv.FormatInt(ref.Rev, 10))
}

func (r *Repository) eventsCol(ws model.WorkspaceID, paper model.PaperID) *firestore.CollectionRef {
	return r.paperDoc(ws, paper).Collection(colEvents)
}

// eventDoc is a feed entry with the expiry time the TTL policy reads.
type eventDoc struct {
	Event    model.Event
	ExpireAt time.Time
}

func newEventDoc(ev *model.Event) *eventDoc {
	return &eventDoc{Event: *ev, ExpireAt: ev.At.Add(model.EventTTL)}
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

func comparePapers(a, b *model.Paper) int {
	return cmp.Or(cmp.Compare(a.Title, b.Title), cmp.Compare(a.ID, b.ID))
}

func (r *Repository) GetPaper(ctx context.Context, ws model.WorkspaceID, id model.PaperID) (*model.Paper, error) {
	if !validKeys(string(ws), string(id)) {
		return nil, notFound("paper", id)
	}
	return get[model.Paper](ctx, r.paperDoc(ws, id), "paper")
}

func (r *Repository) GetPapers(ctx context.Context, ws model.WorkspaceID, ids []model.PaperID) ([]*model.Paper, error) {
	if !validKeys(string(ws)) {
		return nil, nil
	}
	var refs []*firestore.DocumentRef
	for _, id := range ids {
		if validKeys(string(id)) {
			refs = append(refs, r.paperDoc(ws, id))
		}
	}
	return getAll[model.Paper](ctx, r.client, refs, "papers")
}

func (r *Repository) ListChildren(ctx context.Context, ws model.WorkspaceID, parent model.PaperID) ([]*model.Paper, error) {
	if !validKeys(string(ws), string(parent)) {
		return nil, nil
	}
	q := r.papersCol(ws).Where("ParentID", "==", string(parent)).OrderBy("Title", firestore.Asc)
	out, err := all[model.Paper](q.Documents(ctx), "papers")
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, comparePapers)
	return out, nil
}

// sortField is the paper field a list sorted by s orders on.
func sortField(s model.PaperSort) string {
	if s == model.SortCreated {
		return "CreatedAt"
	}
	return "UpdatedAt"
}

// ListPapers filters only on what the papers-list indexes cover (ParentID
// and CreatedBy.UserID, see RequiredIndexes) and reads one more paper than
// the limit to know whether a next page exists.
func (r *Repository) ListPapers(ctx context.Context, ws model.WorkspaceID, q model.PaperQuery) ([]*model.Paper, string, error) {
	if err := q.Validate(); err != nil {
		return nil, "", err
	}
	if !validKeys(string(ws)) {
		return nil, "", nil
	}
	col := r.papersCol(ws)
	query := col.Query
	if q.Unfiled {
		query = query.Where("ParentID", "==", "")
	}
	if q.Creator != "" {
		query = query.Where("CreatedBy.UserID", "==", string(q.Creator))
	}
	query = query.OrderBy(sortField(q.Sort), firestore.Desc).OrderBy(firestore.DocumentID, firestore.Desc)
	if q.Cursor != "" {
		c, err := model.ParsePaperCursor(q.Cursor)
		if err != nil {
			return nil, "", err
		}
		if !validKeys(string(c.ID)) {
			return nil, "", goerr.Wrap(model.ErrInvalidArgument, "invalid cursor")
		}
		query = query.StartAfter(c.At, col.Doc(string(c.ID)))
	}
	got, err := all[model.Paper](query.Limit(q.Limit+1).Documents(ctx), "papers")
	if err != nil {
		return nil, "", err
	}
	if len(got) <= q.Limit {
		return got, "", nil
	}
	page := got[:q.Limit]
	return page, model.CursorOf(q.Sort, page[len(page)-1]), nil
}

// GetPapersIn reads papers of several workspaces in one round trip.
func (r *Repository) GetPapersIn(ctx context.Context, keys []model.PaperKey) ([]*model.Paper, error) {
	var refs []*firestore.DocumentRef
	for _, k := range keys {
		if validKeys(string(k.WorkspaceID), string(k.PaperID)) {
			refs = append(refs, r.paperDoc(k.WorkspaceID, k.PaperID))
		}
	}
	return getAll[model.Paper](ctx, r.client, refs, "papers")
}

// epoch is before every DeletedAt; comparing with it skips papers whose
// DeletedAt is null.
var epoch = time.Unix(0, 0).UTC()

func (r *Repository) ListTrash(ctx context.Context, ws model.WorkspaceID, owner model.UserID) ([]*model.Paper, error) {
	if !validKeys(string(ws)) {
		return nil, nil
	}
	q := r.papersCol(ws).Where("CreatedBy.UserID", "==", string(owner)).Where("DeletedAt", ">", epoch)
	out, err := all[model.Paper](q.Documents(ctx), "papers")
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, comparePapers)
	return out, nil
}

func (r *Repository) ListTrashedBefore(ctx context.Context, ws model.WorkspaceID, t time.Time) ([]*model.Paper, error) {
	if !validKeys(string(ws)) {
		return nil, nil
	}
	q := r.papersCol(ws).Where("DeletedAt", ">", epoch).Where("DeletedAt", "<", t)
	out, err := all[model.Paper](q.Documents(ctx), "papers")
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, comparePapers)
	return out, nil
}

// PurgePaper removes the paper document first, so readers and watchers
// see the paper gone at once, then sweeps its subcollections and search
// entries.
func (r *Repository) PurgePaper(ctx context.Context, ws model.WorkspaceID, id model.PaperID) error {
	if _, err := r.GetPaper(ctx, ws, id); err != nil {
		return err
	}
	doc := r.paperDoc(ws, id)
	if _, err := doc.Delete(ctx); err != nil {
		return goerr.Wrap(err, "failed to delete paper")
	}
	refs, err := r.paperSearchRefs(ctx, ws, id)
	if err != nil {
		return err
	}
	for _, col := range []string{colBlocks, colVersions, colSnapshots, colEvents, colAttachments} {
		got, err := doc.Collection(col).DocumentRefs(ctx).GetAll()
		if err != nil {
			return goerr.Wrap(err, "failed to list documents", goerr.V("collection", col))
		}
		refs = append(refs, got...)
	}
	bw := r.client.BulkWriter(ctx)
	jobs := make([]*firestore.BulkWriterJob, 0, len(refs))
	for _, ref := range refs {
		job, err := bw.Delete(ref)
		if err != nil {
			bw.End()
			return goerr.Wrap(err, "failed to enqueue delete", goerr.V("path", ref.Path))
		}
		jobs = append(jobs, job)
	}
	bw.End()
	for _, job := range jobs {
		if _, err := job.Results(); err != nil && !isNotFound(err) {
			return goerr.Wrap(err, "failed to delete paper contents", goerr.V("paper_id", id))
		}
	}
	return nil
}

func (r *Repository) GetBlocks(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, ids []model.BlockID) ([]*model.Block, error) {
	if !validKeys(string(ws), string(paper)) {
		return nil, notFound("paper", paper)
	}
	var refs []*firestore.DocumentRef
	for _, id := range ids {
		if validKeys(string(id)) {
			refs = append(refs, r.blocksCol(ws, paper).Doc(string(id)))
		}
	}
	return getAll[model.Block](ctx, r.client, refs, "blocks")
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

func (r *Repository) ListPaperBlocks(ctx context.Context, ws model.WorkspaceID, paper model.PaperID) ([]*model.Block, error) {
	if _, err := r.GetPaper(ctx, ws, paper); err != nil {
		return nil, err
	}
	q := r.blocksCol(ws, paper).Where("Type", "==", string(model.BlockPaper)).OrderBy("Order", firestore.Asc)
	blocks, err := all[model.Block](q.Documents(ctx), "paper blocks")
	if err != nil {
		return nil, err
	}
	slices.SortFunc(blocks, compareBlocks)
	return blocks, nil
}

// maxIn is the most values Firestore accepts in one "in" filter.
const maxIn = 30

func (r *Repository) ListBlocksRange(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, after string, limit int) ([]*model.Block, string, error) {
	if limit < 1 {
		return nil, "", goerr.Wrap(model.ErrInvalidArgument, "limit must be positive")
	}
	if _, err := r.GetPaper(ctx, ws, paper); err != nil {
		return nil, "", err
	}
	col := r.blocksCol(ws, paper)
	q := col.Where("ParentID", "==", "")
	if after != "" {
		q = q.Where("Order", ">", after)
	}
	top, err := all[model.Block](q.OrderBy("Order", firestore.Asc).Limit(limit+1).Documents(ctx), "blocks")
	if err != nil {
		return nil, "", err
	}
	slices.SortFunc(top, compareBlocks)
	next := ""
	if len(top) > limit {
		top = top[:limit]
		next = top[len(top)-1].Order
	}
	var tables []string
	for _, b := range top {
		if b.Type == model.BlockTable {
			tables = append(tables, string(b.ID))
		}
	}
	out := top
	for chunk := range slices.Chunk(tables, maxIn) {
		cells, err := all[model.Block](col.Where("ParentID", "in", chunk).Documents(ctx), "cells")
		if err != nil {
			return nil, "", err
		}
		out = append(out, cells...)
	}
	slices.SortFunc(out, compareBlocks)
	return out, next, nil
}

func (r *Repository) ListVersions(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, before, limit int) ([]*model.Version, error) {
	if limit < 1 {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "limit must be positive")
	}
	if _, err := r.GetPaper(ctx, ws, paper); err != nil {
		return nil, err
	}
	q := r.versionsCol(ws, paper).Query
	if before > 0 {
		q = q.Where("Number", "<", before)
	}
	return all[model.Version](q.OrderBy("Number", firestore.Desc).Limit(limit).Documents(ctx), "versions")
}

func (r *Repository) GetVersion(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, number int) (*model.Version, error) {
	if !validKeys(string(ws), string(paper)) || number < 1 {
		return nil, notFound("version", number)
	}
	return get[model.Version](ctx, r.versionsCol(ws, paper).Doc(versionKey(number)), "version")
}

func (r *Repository) GetSnapshots(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, refs []model.SnapshotRef) ([]*model.BlockSnapshot, error) {
	if !validKeys(string(ws), string(paper)) {
		return nil, notFound("paper", paper)
	}
	docs := make([]*firestore.DocumentRef, 0, len(refs))
	for _, ref := range refs {
		if validKeys(string(ref.BlockID)) {
			docs = append(docs, r.snapshotDoc(ws, paper, ref))
		}
	}
	return getAll[model.BlockSnapshot](ctx, r.client, docs, "snapshots")
}

// --- transaction

type blockKey struct {
	paper model.PaperID
	id    model.BlockID
}

// fsTx stages every write and flushes them when fn returns, because
// Firestore transactions must perform all reads before any write. Reads
// overlay the staged writes so fn sees its own changes.
type fsTx struct {
	tx          *firestore.Transaction
	r           *Repository
	ws          model.WorkspaceID
	papers      map[model.PaperID]*model.Paper
	paperRead   map[model.PaperID]*model.Paper // nil value: known missing
	puts        map[blockKey]*model.Block
	deletes     map[blockKey]bool
	versions    []*model.Version
	snapshots   map[model.PaperID]map[model.SnapshotRef]*model.BlockSnapshot
	forwards    []*model.Forward
	attachments []*model.Attachment
	events      []*model.Event
	jobs        map[model.JobID]*model.Job
	items       map[model.JobID]map[model.PaperID]*model.JobItem
}

func (t *fsTx) GetPaper(id model.PaperID) (*model.Paper, error) {
	if p, ok := t.papers[id]; ok {
		return p.Clone(), nil
	}
	if p, ok := t.paperRead[id]; ok {
		if p == nil {
			return nil, notFound("paper", id)
		}
		return p.Clone(), nil
	}
	if !validKeys(string(id)) {
		return nil, notFound("paper", id)
	}
	snap, err := t.tx.Get(t.r.paperDoc(t.ws, id))
	if err != nil {
		if isNotFound(err) {
			t.paperRead[id] = nil
			return nil, notFound("paper", id)
		}
		return nil, goerr.Wrap(err, "failed to get paper", goerr.V("paper_id", id))
	}
	p, err := decode[model.Paper](snap, "paper")
	if err != nil {
		return nil, err
	}
	t.paperRead[id] = p
	return p.Clone(), nil
}

func (t *fsTx) GetPapers(ids []model.PaperID) ([]*model.Paper, error) {
	var refs []*firestore.DocumentRef
	var out []*model.Paper
	for _, id := range ids {
		if p, ok := t.papers[id]; ok {
			out = append(out, p.Clone())
		} else if p, ok := t.paperRead[id]; ok {
			if p != nil {
				out = append(out, p.Clone())
			}
		} else if validKeys(string(id)) {
			refs = append(refs, t.r.paperDoc(t.ws, id))
		}
	}
	if len(refs) > 0 {
		snaps, err := t.tx.GetAll(refs)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to get papers")
		}
		for _, s := range snaps {
			id := model.PaperID(s.Ref.ID)
			if !s.Exists() {
				t.paperRead[id] = nil
				continue
			}
			p, err := decode[model.Paper](s, "paper")
			if err != nil {
				return nil, err
			}
			t.paperRead[id] = p
			out = append(out, p.Clone())
		}
	}
	return out, nil
}

func (t *fsTx) PutPaper(paper *model.Paper) error {
	if err := paper.Validate(); err != nil {
		return err
	}
	if paper.WorkspaceID != t.ws || !validKeys(string(paper.ID)) {
		return goerr.Wrap(model.ErrInvalidArgument, "paper belongs to another workspace")
	}
	t.papers[paper.ID] = paper.Clone()
	return nil
}

func (t *fsTx) GetBlock(paper model.PaperID, id model.BlockID) (*model.Block, error) {
	k := blockKey{paper, id}
	if t.deletes[k] {
		return nil, notFound("block", id)
	}
	if b, ok := t.puts[k]; ok {
		return b.Clone(), nil
	}
	if !validKeys(string(paper), string(id)) {
		return nil, notFound("block", id)
	}
	snap, err := t.tx.Get(t.r.blocksCol(t.ws, paper).Doc(string(id)))
	if err != nil {
		if isNotFound(err) {
			return nil, notFound("block", id)
		}
		return nil, goerr.Wrap(err, "failed to get block", goerr.V("block_id", id))
	}
	return decode[model.Block](snap, "block")
}

func (t *fsTx) GetBlocks(paper model.PaperID, ids []model.BlockID) ([]*model.Block, error) {
	var refs []*firestore.DocumentRef
	var out []*model.Block
	for _, id := range ids {
		k := blockKey{paper, id}
		switch {
		case t.deletes[k]:
		case t.puts[k] != nil:
			out = append(out, t.puts[k].Clone())
		case validKeys(string(paper), string(id)):
			refs = append(refs, t.r.blocksCol(t.ws, paper).Doc(string(id)))
		}
	}
	if len(refs) > 0 {
		snaps, err := t.tx.GetAll(refs)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to get blocks")
		}
		got, err := decodeSnaps[model.Block](snaps, "block")
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

// query runs q in the transaction and merges the staged writes of the
// paper that keep accepts.
func (t *fsTx) query(paper model.PaperID, q firestore.Query, keep func(*model.Block) bool) ([]*model.Block, error) {
	stored, err := all[model.Block](t.tx.Documents(q), "blocks")
	if err != nil {
		return nil, err
	}
	var out []*model.Block
	for _, b := range stored {
		k := blockKey{paper, b.ID}
		if t.puts[k] != nil || t.deletes[k] {
			continue
		}
		out = append(out, b)
	}
	for k, b := range t.puts {
		if k.paper == paper && keep(b) {
			out = append(out, b.Clone())
		}
	}
	slices.SortFunc(out, compareBlocks)
	return out, nil
}

// slack is how many extra rows to read so that staged writes hiding stored
// rows cannot leave the result short.
func (t *fsTx) slack() int { return 1 + len(t.puts) + len(t.deletes) }

func (t *fsTx) FirstAfter(paper model.PaperID, parent model.BlockID, order string) (*model.Block, error) {
	if !validKeys(string(paper)) {
		return nil, nil
	}
	q := t.r.blocksCol(t.ws, paper).Where("ParentID", "==", string(parent))
	if order != "" {
		q = q.Where("Order", ">", order)
	}
	q = q.OrderBy("Order", firestore.Asc).Limit(t.slack())
	bs, err := t.query(paper, q, func(b *model.Block) bool { return b.ParentID == parent && b.Order > order })
	if err != nil || len(bs) == 0 {
		return nil, err
	}
	return bs[0], nil
}

func (t *fsTx) Last(paper model.PaperID, parent model.BlockID) (*model.Block, error) {
	if !validKeys(string(paper)) {
		return nil, nil
	}
	q := t.r.blocksCol(t.ws, paper).Where("ParentID", "==", string(parent)).OrderBy("Order", firestore.Desc).Limit(t.slack())
	bs, err := t.query(paper, q, func(b *model.Block) bool { return b.ParentID == parent })
	if err != nil || len(bs) == 0 {
		return nil, err
	}
	return bs[len(bs)-1], nil
}

func (t *fsTx) Children(paper model.PaperID, parent model.BlockID) ([]*model.Block, error) {
	if !validKeys(string(paper)) {
		return nil, nil
	}
	q := t.r.blocksCol(t.ws, paper).Where("ParentID", "==", string(parent))
	return t.query(paper, q, func(b *model.Block) bool { return b.ParentID == parent })
}

func (t *fsTx) ListBlocks(paper model.PaperID) ([]*model.Block, error) {
	if !validKeys(string(paper)) {
		return nil, nil
	}
	return t.query(paper, t.r.blocksCol(t.ws, paper).Query, func(*model.Block) bool { return true })
}

func (t *fsTx) CountBlocks(paper model.PaperID) (int, error) {
	if !validKeys(string(paper)) {
		return 0, nil
	}
	col := t.r.blocksCol(t.ws, paper)
	res, err := t.tx.Documents(col.Select()).GetAll()
	if err != nil {
		return 0, goerr.Wrap(err, "failed to count blocks")
	}
	n := 0
	stored := map[model.BlockID]bool{}
	for _, s := range res {
		id := model.BlockID(s.Ref.ID)
		stored[id] = true
		if !t.deletes[blockKey{paper, id}] {
			n++
		}
	}
	for k := range t.puts {
		if k.paper == paper && !stored[k.id] {
			n++
		}
	}
	return n, nil
}

func (t *fsTx) PutBlock(block *model.Block) error {
	if err := block.Validate(); err != nil {
		return err
	}
	if block.WorkspaceID != t.ws || !validKeys(string(block.ID)) {
		return goerr.Wrap(model.ErrInvalidArgument, "block belongs to another workspace", goerr.V("block_id", block.ID))
	}
	if _, err := t.GetPaper(block.PaperID); err != nil {
		return goerr.Wrap(model.ErrInvalidArgument, "block belongs to an unknown paper", goerr.V("paper_id", block.PaperID))
	}
	k := blockKey{block.PaperID, block.ID}
	delete(t.deletes, k)
	t.puts[k] = block.Clone()
	return nil
}

func (t *fsTx) DeleteBlock(paper model.PaperID, id model.BlockID) error {
	k := blockKey{paper, id}
	delete(t.puts, k)
	t.deletes[k] = true
	return nil
}

func (t *fsTx) GetVersion(paper model.PaperID, number int) (*model.Version, error) {
	for _, v := range t.versions {
		if v.PaperID == paper && v.Number == number {
			c := *v
			return &c, nil
		}
	}
	if !validKeys(string(paper)) || number < 1 {
		return nil, notFound("version", number)
	}
	snap, err := t.tx.Get(t.r.versionsCol(t.ws, paper).Doc(versionKey(number)))
	if err != nil {
		if isNotFound(err) {
			return nil, notFound("version", number)
		}
		return nil, goerr.Wrap(err, "failed to get version")
	}
	return decode[model.Version](snap, "version")
}

func (t *fsTx) PutVersion(v *model.Version) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if v.WorkspaceID != t.ws {
		return goerr.Wrap(model.ErrInvalidArgument, "version belongs to another workspace")
	}
	if _, err := t.GetVersion(v.PaperID, v.Number); err == nil {
		return goerr.Wrap(model.ErrAlreadyExists, "version already exists", goerr.V("number", v.Number))
	}
	c := *v
	t.versions = append(t.versions, &c)
	return nil
}

func (t *fsTx) GetSnapshots(paper model.PaperID, refs []model.SnapshotRef) ([]*model.BlockSnapshot, error) {
	var docs []*firestore.DocumentRef
	var out []*model.BlockSnapshot
	for _, ref := range refs {
		if s, ok := t.snapshots[paper][ref]; ok {
			c := *s
			out = append(out, &c)
		} else if validKeys(string(paper), string(ref.BlockID)) {
			docs = append(docs, t.r.snapshotDoc(t.ws, paper, ref))
		}
	}
	if len(docs) > 0 {
		snaps, err := t.tx.GetAll(docs)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to get snapshots")
		}
		got, err := decodeSnaps[model.BlockSnapshot](snaps, "snapshot")
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

func (t *fsTx) PutSnapshot(paper model.PaperID, s *model.BlockSnapshot) error {
	if !validKeys(string(paper), string(s.BlockID)) {
		return goerr.Wrap(model.ErrInvalidArgument, "invalid snapshot")
	}
	if t.snapshots[paper] == nil {
		t.snapshots[paper] = map[model.SnapshotRef]*model.BlockSnapshot{}
	}
	c := *s
	t.snapshots[paper][model.SnapshotRef{BlockID: s.BlockID, Rev: s.Rev}] = &c
	return nil
}

func (t *fsTx) PutForward(f *model.Forward) error {
	if f.WorkspaceID != t.ws || !validKeys(string(f.PaperID)) {
		return goerr.Wrap(model.ErrInvalidArgument, "invalid forward")
	}
	c := *f
	t.forwards = append(t.forwards, &c)
	return nil
}

func (t *fsTx) PutAttachment(a *model.Attachment) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if a.WorkspaceID != t.ws || !validKeys(string(a.PaperID), string(a.ID)) {
		return goerr.Wrap(model.ErrInvalidArgument, "invalid attachment")
	}
	if _, err := t.GetPaper(a.PaperID); err != nil {
		return goerr.Wrap(model.ErrInvalidArgument, "attachment belongs to an unknown paper", goerr.V("paper_id", a.PaperID))
	}
	c := *a
	t.attachments = append(t.attachments, &c)
	return nil
}

func (t *fsTx) AddEvent(ev *model.Event) error {
	if ev.WorkspaceID != t.ws || !validKeys(string(ev.PaperID)) {
		return goerr.Wrap(model.ErrInvalidArgument, "invalid event")
	}
	c := *ev
	t.events = append(t.events, &c)
	return nil
}

var _ interfaces.Tx = (*fsTx)(nil)

func (t *fsTx) flush() error {
	tx, ws := t.tx, t.ws
	for k := range t.deletes {
		if err := tx.Delete(t.r.blocksCol(ws, k.paper).Doc(string(k.id))); err != nil {
			return goerr.Wrap(err, "failed to stage block delete")
		}
		if err := stageSearchEntry(tx, t.r.searchDoc(ws, k.paper, k.id), nil); err != nil {
			return err
		}
	}
	for k, b := range t.puts {
		if err := tx.Set(t.r.blocksCol(ws, k.paper).Doc(string(k.id)), b); err != nil {
			return goerr.Wrap(err, "failed to stage block write")
		}
		if err := stageSearchEntry(tx, t.r.searchDoc(ws, k.paper, k.id), model.NewBlockSearchEntry(b)); err != nil {
			return err
		}
	}
	for paper, snaps := range t.snapshots {
		for ref, s := range snaps {
			// Set: a snapshot of a (block, rev) never changes, so writing
			// it again is harmless.
			if err := tx.Set(t.r.snapshotDoc(ws, paper, ref), s); err != nil {
				return goerr.Wrap(err, "failed to stage snapshot")
			}
		}
	}
	for _, v := range t.versions {
		// Create: a concurrent writer that took the same number aborts.
		if err := tx.Create(t.r.versionsCol(ws, v.PaperID).Doc(versionKey(v.Number)), v); err != nil {
			return goerr.Wrap(err, "failed to stage version")
		}
	}
	for id, p := range t.papers {
		if err := tx.Set(t.r.paperDoc(ws, id), p); err != nil {
			return goerr.Wrap(err, "failed to stage paper write")
		}
		if err := stageSearchEntry(tx, t.r.searchDoc(ws, id, ""), model.NewTitleSearchEntry(p)); err != nil {
			return err
		}
	}
	for _, f := range t.forwards {
		if err := tx.Set(t.r.forwardDoc(ws, f.PaperID), f); err != nil {
			return goerr.Wrap(err, "failed to stage forward")
		}
	}
	for _, a := range t.attachments {
		// Set: an attachment never changes, so writing it again is harmless.
		if err := tx.Set(t.r.attachmentsCol(ws, a.PaperID).Doc(string(a.ID)), a); err != nil {
			return goerr.Wrap(err, "failed to stage attachment")
		}
	}
	for _, ev := range t.events {
		if err := tx.Create(t.r.eventsCol(ws, ev.PaperID).NewDoc(), newEventDoc(ev)); err != nil {
			return goerr.Wrap(err, "failed to stage event")
		}
	}
	return t.flushJobs()
}

func (r *Repository) RunTx(ctx context.Context, ws model.WorkspaceID, fn func(tx interfaces.Tx) error) error {
	if !validKeys(string(ws)) {
		return goerr.Wrap(model.ErrInvalidArgument, "invalid workspace", goerr.V("workspace_id", ws))
	}
	err := r.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		t := &fsTx{
			tx: tx, r: r, ws: ws,
			papers: map[model.PaperID]*model.Paper{}, paperRead: map[model.PaperID]*model.Paper{},
			puts: map[blockKey]*model.Block{}, deletes: map[blockKey]bool{},
			snapshots: map[model.PaperID]map[model.SnapshotRef]*model.BlockSnapshot{},
			jobs:      map[model.JobID]*model.Job{},
			items:     map[model.JobID]map[model.PaperID]*model.JobItem{},
		}
		if err := fn(t); err != nil {
			return err
		}
		return t.flush()
	})
	if err != nil {
		if isAlreadyExists(err) {
			return goerr.Wrap(model.ErrAlreadyExists, "a concurrent write created the same record", goerr.V("workspace_id", ws))
		}
		return err
	}
	return nil
}
