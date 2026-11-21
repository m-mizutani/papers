package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase/markdown"
)

// OpKind is the kind of a requested operation in a commit.
type OpKind string

const (
	OpInsert         OpKind = "insert"
	OpUpdate         OpKind = "update"
	OpDelete         OpKind = "delete"
	OpMove           OpKind = "move"
	OpSetTitle       OpKind = "set_title"
	OpInsertMarkdown OpKind = "insert_markdown"
	// OpRestore makes an earlier version of a block current again, as a
	// new version. It also brings back a deleted block.
	OpRestore OpKind = "restore"
)

// Op is one requested operation. Positioning (insert, move,
// insert_markdown) uses After: nil appends at the end, a pointer to ""
// inserts at the top, and a block ID inserts right after that block.
// Only top-level blocks are positioned; table cells are managed through
// their table's Rows / Cols.
type Op struct {
	Kind    OpKind            `json:"op"`
	BlockID model.BlockID     `json:"block_id,omitempty"`
	Type    *model.BlockType  `json:"type,omitempty"`
	Content *string           `json:"content,omitempty"`
	Props   *model.BlockProps `json:"props,omitempty"`
	After   *model.BlockID    `json:"after,omitempty"`
	// BaseVersion is the version of the block the writer based the op on.
	// Required for update, delete and restore of an existing block.
	BaseVersion int64 `json:"base_version,omitempty"`
	// Version is the version restore copies the contents from.
	Version  int64   `json:"version,omitempty"`
	Title    *string `json:"title,omitempty"`
	Markdown *string `json:"markdown,omitempty"`
	// Cells fills a new table (insert of type table), row by row.
	Cells [][]string `json:"cells,omitempty"`
}

type CommitInput struct {
	Ops     []Op
	Message string
	// Force writes over contents someone else changed since the op's base
	// version, instead of failing with a ConflictError. Every forced
	// overwrite is recorded in the commit.
	Force bool
}

// Conflict is one op that was based on an older version of a block whose
// contents someone else has changed since.
type Conflict struct {
	BlockID     model.BlockID `json:"block_id"`
	BaseVersion int64         `json:"base_version"`
	// Current is the block as it is now; nil when it has been deleted.
	Current *model.Block `json:"current"`
}

// ConflictError reports every conflicting op of a commit; nothing was
// written. It matches model.ErrConflict with errors.Is. Retrying with Force
// overwrites the current contents, except for deleted blocks, which have
// to be restored first.
type ConflictError struct {
	Conflicts []Conflict
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%d block(s) changed since the base version", len(e.Conflicts))
}

func (e *ConflictError) Is(target error) bool { return target == model.ErrConflict }

// errConflictRecorded marks an op skipped because its conflict was recorded
// in the applier; the commit fails with a ConflictError after all ops ran.
var errConflictRecorded = errors.New("conflict recorded")

type CommitResult struct {
	// Commit is nil when the operations changed nothing.
	Commit *model.Commit
	Paper  *model.Paper
	// Blocks are the resulting states of inserted / updated / moved blocks,
	// and Deleted the IDs of removed blocks, so the caller can apply the
	// commit without waiting for change events.
	Blocks  []*model.Block
	Deleted []model.BlockID
}

type applier struct {
	tx      interfaces.PaperTx
	paper   *model.Paper
	actor   model.Actor
	now     time.Time
	force   bool
	changes []model.Change
	// orig holds each existing block as it was before this commit, so that
	// several ops on one block are all checked against the same base.
	orig      map[model.BlockID]*model.Block
	conflicts []Conflict
	// touched tracks the final state per block for the result: nil means
	// deleted.
	touched map[model.BlockID]*model.Block
	order   []model.BlockID

	titleChanged bool
	// contentBytes is the content size of each block written so far; a
	// block written twice in one commit counts once, at its last size.
	contentBytes map[model.BlockID]int
	totalBytes   int
}

func (a *applier) record(ch model.Change, after *model.Block) error {
	if len(a.changes) >= model.MaxChangesPerCommit {
		return goerr.Wrap(model.ErrInvalidArgument, "commit changes too many blocks; split it", goerr.V("max", model.MaxChangesPerCommit))
	}
	a.changes = append(a.changes, ch)
	if _, ok := a.touched[ch.BlockID]; !ok && ch.BlockID != "" {
		a.order = append(a.order, ch.BlockID)
	}
	if ch.BlockID != "" {
		a.touched[ch.BlockID] = after
	}
	return nil
}

// orderAfter computes the order key for a top-level block placed per
// after. self is the block being moved, if any; it is skipped as a
// neighbour.
func (a *applier) orderAfter(after *model.BlockID, self model.BlockID) (string, error) {
	// next returns the first top-level block after order, skipping self.
	next := func(order string) (*model.Block, error) {
		for {
			b, err := a.tx.FirstAfter("", order)
			if err != nil {
				return nil, goerr.Wrap(err, "failed to find next block")
			}
			if b == nil || b.ID != self {
				return b, nil
			}
			order = b.Order
		}
	}
	upper := func(b *model.Block) string {
		if b == nil {
			return ""
		}
		return b.Order
	}

	switch {
	case after == nil:
		last, err := a.tx.Last("")
		if err != nil {
			return "", goerr.Wrap(err, "failed to find last block")
		}
		if last != nil && last.ID == self {
			// Moving the last block to the end: find the one before it.
			children, err := a.tx.Children("")
			if err != nil {
				return "", goerr.Wrap(err, "failed to list blocks")
			}
			last = nil
			for _, c := range children {
				if c.ID != self {
					last = c
				}
			}
		}
		lo := ""
		if last != nil {
			lo = last.Order
		}
		return model.KeyBetween(lo, "")
	case *after == "":
		first, err := next("")
		if err != nil {
			return "", err
		}
		return model.KeyBetween("", upper(first))
	default:
		anchor, err := a.tx.GetBlock(*after)
		if err != nil || anchor.ParentID != "" {
			return "", goerr.Wrap(model.ErrInvalidArgument, "anchor block not found among top-level blocks", goerr.V("after", *after))
		}
		following, err := next(anchor.Order)
		if err != nil {
			return "", err
		}
		return model.KeyBetween(anchor.Order, upper(following))
	}
}

func (a *applier) newBlock(id model.BlockID, t model.BlockType, content string, props model.BlockProps, parent model.BlockID, order string) *model.Block {
	return &model.Block{
		ID: id, WorkspaceID: a.paper.WorkspaceID, PaperID: a.paper.ID, ParentID: parent,
		Order: order, Type: t, Content: content, Props: props.Normalize(t), Version: 1,
		CreatedBy: a.actor, CreatedAt: a.now, UpdatedBy: a.actor, UpdatedAt: a.now,
	}
}

func (a *applier) put(b *model.Block) error {
	if a.contentBytes == nil {
		a.contentBytes = map[model.BlockID]int{}
	}
	total := a.totalBytes - a.contentBytes[b.ID] + len(b.Content)
	if total > model.MaxCommitContentBytes {
		return goerr.Wrap(model.ErrInvalidArgument, "commit writes too much content; split it",
			goerr.V("bytes", total), goerr.V("max", model.MaxCommitContentBytes))
	}
	a.contentBytes[b.ID], a.totalBytes = len(b.Content), total
	if err := a.tx.PutBlock(b); err != nil {
		return goerr.Wrap(err, "failed to put block", goerr.V("block_id", b.ID))
	}
	return nil
}

// cellOrder is the fixed order key of table cells; cells are addressed by
// Row / Col, so their sibling order is irrelevant.
const cellOrder = "V"

// syncCells creates missing cells and deletes out-of-range ones so that the
// table has exactly Rows x Cols cells. contents, if given, fills new cells.
func (a *applier) syncCells(table *model.Block, contents [][]string) error {
	cells, err := a.tx.Children(table.ID)
	if err != nil {
		return goerr.Wrap(err, "failed to list table cells")
	}
	type pos struct{ r, c int }
	existing := map[pos]bool{}
	for _, c := range cells {
		p := pos{c.Props.Row, c.Props.Col}
		if c.Props.Row >= table.Props.Rows || c.Props.Col >= table.Props.Cols || existing[p] {
			if err := a.tx.DeleteBlock(c.ID); err != nil {
				return goerr.Wrap(err, "failed to delete table cell")
			}
			if err := a.record(model.Change{Kind: model.ChangeDelete, BlockID: c.ID, FromVersion: c.Version}, nil); err != nil {
				return err
			}
			continue
		}
		existing[p] = true
	}
	for r := 0; r < table.Props.Rows; r++ {
		for c := 0; c < table.Props.Cols; c++ {
			if existing[pos{r, c}] {
				continue
			}
			content := ""
			if r < len(contents) && c < len(contents[r]) {
				content = contents[r][c]
			}
			cell := a.newBlock(model.NewBlockID(), model.BlockTableCell, content, model.BlockProps{Row: r, Col: c}, table.ID, cellOrder)
			if err := a.put(cell); err != nil {
				return err
			}
			if err := a.record(model.Change{Kind: model.ChangeInsert, BlockID: cell.ID, ToVersion: 1}, cell); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *applier) deleteChildren(parent *model.Block) error {
	children, err := a.tx.Children(parent.ID)
	if err != nil {
		return goerr.Wrap(err, "failed to list children")
	}
	for _, c := range children {
		if err := a.tx.DeleteBlock(c.ID); err != nil {
			return goerr.Wrap(err, "failed to delete child block")
		}
		if err := a.record(model.Change{Kind: model.ChangeDelete, BlockID: c.ID, FromVersion: c.Version}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (a *applier) insert(op Op) (model.BlockID, error) {
	if op.Type == nil {
		return "", goerr.Wrap(model.ErrInvalidArgument, "insert requires type")
	}
	if op.Type.RequiredParent() != "" {
		return "", goerr.Wrap(model.ErrInvalidArgument, "table cells are created by resizing their table", goerr.V("type", *op.Type))
	}
	id := op.BlockID
	if id == "" {
		id = model.NewBlockID()
	} else {
		// Client-chosen IDs let the UI render a block before the commit
		// returns; they must still be unguessable and well-formed.
		if _, err := uuid.Parse(string(id)); err != nil {
			return "", goerr.Wrap(model.ErrInvalidArgument, "block id must be a UUID", goerr.V("block_id", id))
		}
		if _, err := a.tx.GetBlock(id); err == nil {
			return "", goerr.Wrap(model.ErrAlreadyExists, "block already exists", goerr.V("block_id", id))
		} else if !errors.Is(err, model.ErrNotFound) {
			return "", goerr.Wrap(err, "failed to check block id")
		}
	}
	order, err := a.orderAfter(op.After, "")
	if err != nil {
		return "", err
	}
	content := ""
	if op.Content != nil {
		content = *op.Content
	}
	props := model.BlockProps{}
	if op.Props != nil {
		props = *op.Props
	}
	if *op.Type == model.BlockHeading && props.Level == 0 {
		props.Level = 1
	}
	if *op.Type == model.BlockTable {
		if props.Rows == 0 && len(op.Cells) > 0 {
			props.Rows = len(op.Cells)
			for _, row := range op.Cells {
				props.Cols = max(props.Cols, len(row))
			}
		}
	}
	b := a.newBlock(id, *op.Type, content, props, "", order)
	if err := a.put(b); err != nil {
		return "", err
	}
	if err := a.record(model.Change{Kind: model.ChangeInsert, BlockID: id, ToVersion: 1}, b.Clone()); err != nil {
		return "", err
	}
	if b.Type == model.BlockTable {
		if err := a.syncCells(b, op.Cells); err != nil {
			return "", err
		}
	}
	return id, nil
}

func (a *applier) get(id model.BlockID) (*model.Block, error) {
	if id == "" {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "block_id is required")
	}
	b, err := a.tx.GetBlock(id)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, goerr.Wrap(model.ErrInvalidArgument, "block does not exist", goerr.V("block_id", id))
		}
		return nil, goerr.Wrap(err, "failed to get block", goerr.V("block_id", id))
	}
	a.remember(b)
	return b, nil
}

// remember keeps the pre-commit state of a block read for the first time.
// Blocks inserted or already changed in this commit are not remembered.
func (a *applier) remember(b *model.Block) {
	if _, mine := a.touched[b.ID]; mine {
		return
	}
	if _, ok := a.orig[b.ID]; !ok {
		a.orig[b.ID] = b.Clone()
	}
}

func sameContents(x, y *model.Block) bool {
	return x.Type == y.Type && x.Content == y.Content && x.Props == y.Props
}

type overwrite struct {
	version int64
	by      model.Actor
}

func (o *overwrite) apply(ch *model.Change) {
	if o != nil {
		by := o.by
		ch.OverwroteVersion, ch.OverwroteBy = o.version, &by
	}
}

// checkBase decides whether an op based on version base of block id may
// replace the block's contents. Someone else's change since base is a
// conflict only if it changed the contents (type, content, props); moves
// never conflict. With force the conflict becomes a recorded overwrite;
// without it the conflict is recorded and errConflictRecorded returned.
func (a *applier) checkBase(id model.BlockID, base int64) (*overwrite, error) {
	if base <= 0 {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "base_version is required", goerr.V("block_id", id))
	}
	orig, ok := a.orig[id]
	if !ok {
		return nil, nil // created in this commit
	}
	if base > orig.Version {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "base_version is newer than the block", goerr.V("block_id", id), goerr.V("base_version", base), goerr.V("version", orig.Version))
	}
	if base == orig.Version {
		return nil, nil
	}
	snap, err := a.tx.BlockVersion(id, base)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return nil, goerr.Wrap(err, "failed to read base version", goerr.V("block_id", id))
	}
	if snap != nil && sameContents(snap, orig) {
		return nil, nil
	}
	if a.force {
		return &overwrite{version: orig.Version, by: orig.UpdatedBy}, nil
	}
	a.conflicts = append(a.conflicts, Conflict{BlockID: id, BaseVersion: base, Current: orig.Clone()})
	return nil, errConflictRecorded
}

// existing loads the block an op edits. A block that someone else deleted
// is a conflict (even with force: restore it first); a block deleted earlier
// in this commit or one that never existed is an invalid argument.
func (a *applier) existing(id model.BlockID, base int64) (*model.Block, error) {
	b, err := a.get(id)
	if err == nil {
		return b, nil
	}
	if !errors.Is(err, model.ErrInvalidArgument) || id == "" {
		return nil, err
	}
	if _, mine := a.touched[id]; mine {
		return nil, err
	}
	if _, verr := a.tx.LatestBlockVersion(id); verr != nil {
		if errors.Is(verr, model.ErrNotFound) {
			return nil, err
		}
		return nil, goerr.Wrap(verr, "failed to read block history", goerr.V("block_id", id))
	}
	a.conflicts = append(a.conflicts, Conflict{BlockID: id, BaseVersion: base})
	return nil, errConflictRecorded
}

func (a *applier) update(op Op) error {
	before, err := a.existing(op.BlockID, op.BaseVersion)
	if err != nil {
		return err
	}
	ow, err := a.checkBase(before.ID, op.BaseVersion)
	if err != nil {
		return err
	}
	b := before.Clone()
	if op.Type != nil && *op.Type != b.Type {
		if b.Type == model.BlockTableCell || op.Type.RequiredParent() != "" {
			return goerr.Wrap(model.ErrInvalidArgument, "table cells cannot change type", goerr.V("block_id", b.ID))
		}
		if b.Type.IsContainer() {
			if err := a.deleteChildren(b); err != nil {
				return err
			}
		}
		b.Type = *op.Type
		if b.Type == model.BlockHeading && b.Props.Level == 0 && (op.Props == nil || op.Props.Level == 0) {
			b.Props.Level = 1
		}
	}
	if op.Content != nil {
		b.Content = *op.Content
	}
	if op.Props != nil {
		props := *op.Props
		if b.Type == model.BlockTableCell {
			// Cells keep their position; only the table moves them.
			props.Row, props.Col = before.Props.Row, before.Props.Col
		}
		b.Props = props
	}
	b.Props = b.Props.Normalize(b.Type)
	if b.Type == model.BlockDivider || b.Type == model.BlockTable {
		b.Content = ""
	}

	if b.Type == before.Type && b.Content == before.Content && b.Props == before.Props {
		return nil // no-op
	}
	b.Version = before.Version + 1
	b.UpdatedBy = a.actor
	b.UpdatedAt = a.now
	if err := a.put(b); err != nil {
		return err
	}
	ch := model.Change{Kind: model.ChangeUpdate, BlockID: b.ID, FromVersion: before.Version, ToVersion: b.Version}
	ow.apply(&ch)
	if err := a.record(ch, b.Clone()); err != nil {
		return err
	}

	if b.Type == model.BlockTable {
		if err := a.syncCells(b, nil); err != nil {
			return err
		}
	}
	return nil
}

func (a *applier) delete(op Op) error {
	b, err := a.existing(op.BlockID, op.BaseVersion)
	if errors.Is(err, errConflictRecorded) {
		// Someone else already deleted it: the delete is done.
		a.conflicts = a.conflicts[:len(a.conflicts)-1]
		return nil
	}
	if err != nil {
		return err
	}
	if b.Type == model.BlockTableCell {
		return goerr.Wrap(model.ErrInvalidArgument, "table cells are removed by resizing their table", goerr.V("block_id", b.ID))
	}
	ow, err := a.checkBase(b.ID, op.BaseVersion)
	if err != nil {
		return err
	}
	if b.Type.IsContainer() {
		if err := a.deleteChildren(b); err != nil {
			return err
		}
	}
	if err := a.tx.DeleteBlock(b.ID); err != nil {
		return goerr.Wrap(err, "failed to delete block")
	}
	ch := model.Change{Kind: model.ChangeDelete, BlockID: b.ID, FromVersion: b.Version}
	ow.apply(&ch)
	return a.record(ch, nil)
}

// move changes only the position, so it never conflicts: the last writer
// wins on order. Moving a block someone else deleted is a no-op.
func (a *applier) move(op Op) error {
	before, err := a.existing(op.BlockID, op.BaseVersion)
	if errors.Is(err, errConflictRecorded) {
		a.conflicts = a.conflicts[:len(a.conflicts)-1]
		return nil
	}
	if err != nil {
		return err
	}
	if before.ParentID != "" {
		return goerr.Wrap(model.ErrInvalidArgument, "only top-level blocks can be moved", goerr.V("block_id", before.ID))
	}
	if op.After != nil && *op.After == before.ID {
		return goerr.Wrap(model.ErrInvalidArgument, "cannot move a block after itself")
	}
	order, err := a.orderAfter(op.After, before.ID)
	if err != nil {
		return err
	}
	if order == before.Order {
		return nil
	}
	b := before.Clone()
	b.Order = order
	b.Version = before.Version + 1
	b.UpdatedBy = a.actor
	b.UpdatedAt = a.now
	if err := a.put(b); err != nil {
		return err
	}
	return a.record(model.Change{Kind: model.ChangeMove, BlockID: b.ID, FromVersion: before.Version, ToVersion: b.Version}, b.Clone())
}

func (a *applier) setTitle(op Op) error {
	if op.Title == nil {
		return goerr.Wrap(model.ErrInvalidArgument, "set_title requires title")
	}
	title := strings.TrimSpace(*op.Title)
	if err := model.ValidateTitle(title); err != nil {
		return err
	}
	if title == a.paper.Title {
		return nil
	}
	if err := a.record(model.Change{Kind: model.ChangeSetTitle, TitleBefore: a.paper.Title, TitleAfter: title}, nil); err != nil {
		return err
	}
	a.paper.Title = title
	a.titleChanged = true
	return nil
}

func (a *applier) insertMarkdown(op Op) error {
	if op.Markdown == nil {
		return goerr.Wrap(model.ErrInvalidArgument, "insert_markdown requires markdown")
	}
	after := op.After
	for _, d := range markdown.Parse(*op.Markdown) {
		t := d.Type
		content := d.Content
		props := d.Props
		id, err := a.insert(Op{Kind: OpInsert, Type: &t, Content: &content, Props: &props, After: after, Cells: d.Cells})
		if err != nil {
			return err
		}
		next := id
		after = &next
	}
	return nil
}

func (a *applier) restore(op Op) error {
	if op.Version <= 0 {
		return goerr.Wrap(model.ErrInvalidArgument, "restore requires version", goerr.V("block_id", op.BlockID))
	}
	if op.BlockID == "" {
		return goerr.Wrap(model.ErrInvalidArgument, "block_id is required")
	}
	snap, err := a.tx.BlockVersion(op.BlockID, op.Version)
	if errors.Is(err, model.ErrNotFound) {
		return goerr.Wrap(model.ErrInvalidArgument, "block version does not exist", goerr.V("block_id", op.BlockID), goerr.V("version", op.Version))
	}
	if err != nil {
		return goerr.Wrap(err, "failed to read block version")
	}

	cur, err := a.tx.GetBlock(op.BlockID)
	if errors.Is(err, model.ErrNotFound) {
		return a.restoreDeleted(op, snap)
	}
	if err != nil {
		return goerr.Wrap(err, "failed to get block", goerr.V("block_id", op.BlockID))
	}
	a.remember(cur)
	ow, err := a.checkBase(cur.ID, op.BaseVersion)
	if err != nil {
		return err
	}
	if sameContents(snap, cur) {
		return nil
	}
	if (cur.Type == model.BlockTableCell) != (snap.Type == model.BlockTableCell) {
		return goerr.Wrap(model.ErrInvalidArgument, "a table cell can only be restored to a cell version", goerr.V("block_id", cur.ID))
	}
	b := cur.Clone()
	if cur.Type.IsContainer() && snap.Type != cur.Type {
		if err := a.deleteChildren(cur); err != nil {
			return err
		}
	}
	b.Type, b.Content, b.Props = snap.Type, snap.Content, snap.Props
	if b.Type == model.BlockTableCell {
		b.Props.Row, b.Props.Col = cur.Props.Row, cur.Props.Col
	}
	b.Version = cur.Version + 1
	b.UpdatedBy, b.UpdatedAt = a.actor, a.now
	if err := a.put(b); err != nil {
		return err
	}
	ch := model.Change{Kind: model.ChangeRestore, BlockID: b.ID, FromVersion: cur.Version, ToVersion: b.Version, RestoredVersion: op.Version}
	ow.apply(&ch)
	if err := a.record(ch, b.Clone()); err != nil {
		return err
	}
	if b.Type == model.BlockTable {
		return a.syncCells(b, nil)
	}
	return nil
}

// restoreDeleted brings a deleted top-level block back at its last position
// with the contents of snap. Tables and cells are not restored this way:
// their cells are separate blocks with their own histories.
func (a *applier) restoreDeleted(op Op, snap *model.Block) error {
	if _, mine := a.touched[op.BlockID]; mine {
		return goerr.Wrap(model.ErrInvalidArgument, "block was deleted in this commit", goerr.V("block_id", op.BlockID))
	}
	last, err := a.tx.LatestBlockVersion(op.BlockID)
	if err != nil {
		return goerr.Wrap(err, "failed to read block history", goerr.V("block_id", op.BlockID))
	}
	if last.ParentID != "" || snap.Type.IsContainer() || snap.Type == model.BlockTableCell {
		return goerr.Wrap(model.ErrInvalidArgument, "deleted tables and table cells cannot be restored", goerr.V("block_id", op.BlockID))
	}
	b := snap.Clone()
	b.ParentID, b.Order = "", last.Order
	b.Version = last.Version + 1
	b.UpdatedBy, b.UpdatedAt = a.actor, a.now
	if err := a.put(b); err != nil {
		return err
	}
	return a.record(model.Change{Kind: model.ChangeRestore, BlockID: b.ID, FromVersion: last.Version, ToVersion: b.Version, RestoredVersion: op.Version}, b.Clone())
}

func (a *applier) apply(op Op) error {
	switch op.Kind {
	case OpInsert:
		_, err := a.insert(op)
		return err
	case OpUpdate:
		return a.update(op)
	case OpDelete:
		return a.delete(op)
	case OpMove:
		return a.move(op)
	case OpSetTitle:
		return a.setTitle(op)
	case OpInsertMarkdown:
		return a.insertMarkdown(op)
	case OpRestore:
		return a.restore(op)
	default:
		return goerr.Wrap(model.ErrInvalidArgument, "unknown op", goerr.V("op", op.Kind))
	}
}

// Commit applies operations to a paper atomically and records them as one
// commit. Each changed block gets a new version (with a stored snapshot).
// An op based on an older version of a block whose contents someone else
// has changed since fails the whole commit with a *ConflictError listing
// every such op, unless in.Force is set: then the last writer wins and the
// overwrite is recorded in the change. Watchers learn about the result from
// the repository's change feed.
func (u *UseCases) Commit(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, paperID model.PaperID, in CommitInput) (*CommitResult, error) {
	if _, _, _, err := u.paperFor(ctx, p, wsID, paperID, model.RoleEditor); err != nil {
		return nil, err
	}
	if len(in.Ops) == 0 {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "no operations")
	}
	if len(in.Ops) > u.cfg.MaxOpsPerCommit {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "too many operations", goerr.V("count", len(in.Ops)), goerr.V("max", u.cfg.MaxOpsPerCommit))
	}
	if len(in.Message) > model.MaxCommitMessageLength {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "commit message is too long")
	}

	now := u.now()
	var result CommitResult
	err := u.repo.RunPaperTx(ctx, wsID, paperID, func(tx interfaces.PaperTx) error {
		a := &applier{tx: tx, paper: tx.Paper(), actor: p.Actor(), now: now, force: in.Force,
			touched: map[model.BlockID]*model.Block{}, orig: map[model.BlockID]*model.Block{}}
		for i, op := range in.Ops {
			if err := a.apply(op); errors.Is(err, errConflictRecorded) {
				continue
			} else if err != nil {
				return goerr.Wrap(err, fmt.Sprintf("op #%d (%s) failed", i, op.Kind), goerr.V("index", i))
			}
		}
		if len(a.conflicts) > 0 {
			return &ConflictError{Conflicts: a.conflicts}
		}
		result = CommitResult{Paper: a.paper}
		if len(a.changes) == 0 {
			return nil
		}
		// The paper record is written only when its title changes or its
		// UpdatedAt has gone stale, so busy papers do not contend on it.
		if a.titleChanged || now.Sub(a.paper.UpdatedAt) >= u.cfg.PaperTouchInterval {
			a.paper.UpdatedBy = a.actor
			a.paper.UpdatedAt = now
			if err := tx.PutPaper(a.paper); err != nil {
				return goerr.Wrap(err, "failed to update paper")
			}
		}
		c := &model.Commit{
			ID: model.NewCommitID(), WorkspaceID: wsID, PaperID: paperID,
			Actor: a.actor, Message: strings.TrimSpace(in.Message), Changes: a.changes, CreatedAt: now,
		}
		if err := tx.AddCommit(c); err != nil {
			return goerr.Wrap(err, "failed to add commit")
		}
		result.Commit = c
		for _, id := range a.order {
			if b := a.touched[id]; b != nil {
				result.Blocks = append(result.Blocks, b)
			} else {
				result.Deleted = append(result.Deleted, id)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}
