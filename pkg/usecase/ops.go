package usecase

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/markup"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase/markdown"
)

// OpKind is the kind of a requested operation.
type OpKind string

const (
	OpInsert   OpKind = "insert"
	OpUpdate   OpKind = "update"
	OpDelete   OpKind = "delete"
	OpMove     OpKind = "move"
	OpSetTitle OpKind = "set_title"
	// OpSetIcon sets or (with "") removes the paper's icon. Like a title
	// it has no revision: the last write wins.
	OpSetIcon        OpKind = "set_icon"
	OpInsertMarkdown OpKind = "insert_markdown"
	// OpSetHTML replaces the document of an HTML paper (its html_page
	// block), based on BaseRev like an update.
	OpSetHTML OpKind = "set_html"

	OpInsertRow OpKind = "insert_row"
	OpDeleteRow OpKind = "delete_row"
	OpMoveRow   OpKind = "move_row"
	OpInsertCol OpKind = "insert_col"
	OpDeleteCol OpKind = "delete_col"
	OpMoveCol   OpKind = "move_col"

	// Paper-level operations. Each must be the only operation of its
	// batch.
	OpMovePaper      OpKind = "move_paper"
	OpSetVisibility  OpKind = "set_visibility"
	OpSaveVersion    OpKind = "save_version"
	OpRestoreVersion OpKind = "restore_version"
	OpArchive        OpKind = "archive"
	OpUnarchive      OpKind = "unarchive"
)

func (k OpKind) paperLevel() bool {
	switch k {
	case OpMovePaper, OpSetVisibility, OpSaveVersion, OpRestoreVersion, OpArchive, OpUnarchive:
		return true
	}
	return false
}

// RefPrefix marks a reference to a block or table line created earlier in
// the same batch: after: "ref:n1" points at the insert with ref "n1".
const RefPrefix = "ref:"

// Op is one requested operation.
//
// Positioning (insert, move, insert_markdown, and the row / column ops)
// uses After: nil appends at the end, "" inserts at the top, and an ID or
// "ref:<name>" inserts right after that block or line.
type Op struct {
	Kind    OpKind            `json:"op"`
	BlockID model.BlockID     `json:"block_id,omitempty"`
	Ref     string            `json:"ref,omitempty"`
	Type    *model.BlockType  `json:"type,omitempty"`
	Content *string           `json:"content,omitempty"`
	Props   *model.BlockProps `json:"props,omitempty"`
	After   *string           `json:"after,omitempty"`
	// BaseRev is the revision of the block the writer based the op on.
	// Required for update and delete of an existing block.
	BaseRev int64   `json:"base_rev,omitempty"`
	Title   *string `json:"title,omitempty"`
	// Icon is the icon of set_icon, and of the paper an insert of a paper
	// block (or insert_item) creates: one emoji, or "" for none.
	Icon     *string `json:"icon,omitempty"`
	Markdown *string `json:"markdown,omitempty"`
	// HTML is the document of set_html, and of the child an insert of a
	// paper block creates with PaperKind html (required there).
	HTML *string `json:"html,omitempty"`
	// PaperKind is the kind of the child paper an insert of a paper block
	// creates (blocks when absent; html or database).
	PaperKind *model.PaperKind `json:"paper_kind,omitempty"`
	// Databases are the database connections of the html_page block that
	// set_html writes (nil keeps the current ones, [] removes them) or that
	// an insert of an HTML paper creates.
	Databases *[]model.DatabaseLink `json:"databases,omitempty"`

	// Cells fills a new table, row by row. Without cells, Rows and Cols
	// give its size (default 2 x 2).
	Cells [][]string `json:"cells,omitempty"`
	Rows  int        `json:"rows,omitempty"`
	Cols  int        `json:"cols,omitempty"`
	// LineID is the row or column of a row / column op; Values fill the
	// cells of a new row or column.
	LineID model.LineID `json:"line_id,omitempty"`
	Values []string     `json:"values,omitempty"`

	// Parent is the new parent of move_paper and set_visibility (public):
	// a paper ID or a path of titles from the home ("Runbooks/DB").
	// set_visibility without a parent leaves the paper unfiled.
	Parent string `json:"parent,omitempty"`
	// Workspace moves the paper to another workspace (move_paper).
	Workspace model.WorkspaceID `json:"workspace,omitempty"`
	// Unfiled takes the paper out of the tree (move_paper, without parent
	// and workspace; only for a paper without children).
	Unfiled    bool             `json:"unfiled,omitempty"`
	Visibility model.Visibility `json:"visibility,omitempty"`
	Note       string           `json:"note,omitempty"`
	Version    int              `json:"version,omitempty"`
	// Subtree applies archive / unarchive to every paper under this one.
	Subtree bool `json:"subtree,omitempty"`

	// Database ops (see database.go). Property, Element and Item name what
	// the op acts on, by ID or "ref:<name>".
	Property     string             `json:"property,omitempty"`
	Element      string             `json:"element,omitempty"`
	Item         string             `json:"item,omitempty"`
	Name         *string            `json:"name,omitempty"`
	PropertyType model.PropertyType `json:"property_type,omitempty"`
	// Value is an element's value (the text people see).
	Value *string `json:"value,omitempty"`
	Color *string `json:"color,omitempty"`
	// Set maps property IDs (or refs) to property values: a string, a list
	// of element IDs, or null to clear.
	Set map[string]any `json:"set,omitempty"`
}

type BatchInput struct {
	Ops []Op
	// Force writes over contents someone else changed since the op's base
	// revision, instead of failing with a ConflictError.
	Force bool
}

// Conflict is one op based on an older revision of a block whose contents
// someone else has changed since.
type Conflict struct {
	BlockID model.BlockID `json:"block_id"`
	BaseRev int64         `json:"base_rev"`
	// Current is the block as it is now; nil when it has been deleted.
	Current *model.Block `json:"current"`
}

// ConflictError reports every conflicting op of a batch; nothing was
// written. It matches model.ErrConflict.
type ConflictError struct {
	Conflicts []Conflict
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%d block(s) changed since the base revision", len(e.Conflicts))
}

func (e *ConflictError) Is(target error) bool { return target == model.ErrConflict }

var errConflictRecorded = errors.New("conflict recorded")

// Editing tells the writer that someone was editing a block the batch
// removed (a cell of a deleted row or column, D3).
type Editing struct {
	BlockID model.BlockID `json:"block_id"`
	Actor   model.Actor   `json:"actor"`
}

// NotRestored is a child paper a version restore did not bring back.
type NotRestored struct {
	PaperID model.PaperID `json:"paper_id"`
	// Reason is "moved" (it lives under another parent now), "trashed"
	// (in the trash and not the caller's) or "missing".
	Reason string `json:"reason"`
}

type BatchResult struct {
	Paper *model.Paper `json:"paper"`
	// Blocks are the resulting states of inserted / updated / moved blocks,
	// and Deleted the IDs of removed blocks.
	Blocks  []*model.Block  `json:"blocks"`
	Deleted []model.BlockID `json:"deleted"`
	// Refs maps each ref of the batch to the ID issued for it.
	Refs    map[string]string `json:"refs"`
	Editing []Editing         `json:"editing,omitempty"`
	// Version is the version a save_version created.
	Version     *model.Version `json:"version,omitempty"`
	NotRestored []NotRestored  `json:"not_restored,omitempty"`
	// Items are the items a database batch created, and Values the
	// property values it wrote.
	Items  []*model.Paper         `json:"items,omitempty"`
	Values []*model.PropertyValue `json:"values,omitempty"`
}

type applier struct {
	u     *UseCases
	ctx   context.Context
	tx    interfaces.Tx
	p     *model.Principal
	paper *model.Paper
	actor model.Actor
	now   time.Time
	force bool

	// orig holds each pre-existing block as it was before this batch, so
	// that several ops on one block are all checked against the same base.
	orig    map[model.BlockID]*model.Block
	created map[model.BlockID]bool
	// touched is the final state per block for the result: nil = deleted.
	touched map[model.BlockID]*model.Block
	order   []model.BlockID
	refs    map[string]string

	conflicts []Conflict
	result    *BatchResult

	paperChanged bool
	titleChanged bool
	// parentable caches, for this batch, whether the paper may have child
	// papers (reading the workspace record once, not once per insert).
	parentable *bool
	// others are other papers this batch writes (children).
	others map[model.PaperID]*model.Paper
	// jobs are background jobs the batch stores; they run after it.
	jobs []*model.Job

	bytes      map[model.BlockID]int
	totalBytes int
	inserted   bool
	presence   []*model.Event
	// unbounded lifts the per-batch limits (version restore).
	unbounded bool

	// html is the document of the batch's set_html, stored beforehand.
	html *stagedHTML
	// children are the stored documents and issued IDs of HTML papers the
	// batch's inserts create, by op index; idx is the op being applied.
	children map[int]*stagedChild
	idx      int
}

// stagedChild is an HTML paper an insert creates: its ID is issued and its
// document stored before the transaction.
type stagedChild struct {
	id   model.PaperID
	html *stagedHTML
}

func (a *applier) touch(b *model.Block) {
	if _, seen := a.touched[b.ID]; !seen {
		a.order = append(a.order, b.ID)
	}
	a.touched[b.ID] = b.Clone()
}

func (a *applier) put(b *model.Block) error {
	if a.unbounded {
		if err := a.tx.PutBlock(b); err != nil {
			return goerr.Wrap(err, "failed to put block", goerr.V("block_id", b.ID))
		}
		a.touch(b)
		return nil
	}
	if _, seen := a.touched[b.ID]; !seen && len(a.touched) >= MaxBlocksPerBatch {
		return goerr.Wrap(model.ErrInvalidArgument, "the batch changes too many blocks; split it", goerr.V("max", MaxBlocksPerBatch))
	}
	total := a.totalBytes - a.bytes[b.ID] + len(b.Content)
	if total > MaxBatchBytes {
		return goerr.Wrap(model.ErrInvalidArgument, "the batch writes too much content; split it", goerr.V("bytes", total), goerr.V("max", MaxBatchBytes))
	}
	a.bytes[b.ID], a.totalBytes = len(b.Content), total
	if err := a.tx.PutBlock(b); err != nil {
		return goerr.Wrap(err, "failed to put block", goerr.V("block_id", b.ID))
	}
	a.touch(b)
	return nil
}

func (a *applier) remove(b *model.Block) error {
	if _, seen := a.touched[b.ID]; !seen && !a.unbounded && len(a.touched) >= MaxBlocksPerBatch {
		return goerr.Wrap(model.ErrInvalidArgument, "the batch changes too many blocks; split it", goerr.V("max", MaxBlocksPerBatch))
	}
	if err := a.tx.DeleteBlock(a.paper.ID, b.ID); err != nil {
		return goerr.Wrap(err, "failed to delete block", goerr.V("block_id", b.ID))
	}
	if _, seen := a.touched[b.ID]; !seen {
		a.order = append(a.order, b.ID)
	}
	a.touched[b.ID] = nil
	return nil
}

// resolve turns an After value into a block ID ("" stays "").
func (a *applier) resolve(after string) (string, error) {
	if name, ok := strings.CutPrefix(after, RefPrefix); ok {
		id, ok := a.refs[name]
		if !ok {
			return "", goerr.Wrap(model.ErrInvalidArgument, "unknown ref", goerr.V("ref", name))
		}
		return id, nil
	}
	return after, nil
}

func (a *applier) addRef(name, id string) error {
	if name == "" {
		return nil
	}
	if strings.HasPrefix(name, RefPrefix) || len(name) > 64 {
		return goerr.Wrap(model.ErrInvalidArgument, "invalid ref", goerr.V("ref", name))
	}
	if _, dup := a.refs[name]; dup {
		return goerr.Wrap(model.ErrInvalidArgument, "ref is used twice", goerr.V("ref", name))
	}
	a.refs[name] = id
	return nil
}

// orderAfter computes the order key for a top-level block placed per after.
// self is the block being moved, if any; it is skipped as a neighbour.
func (a *applier) orderAfter(after *string, self model.BlockID) (string, error) {
	pid := a.paper.ID
	next := func(order string) (*model.Block, error) {
		for {
			b, err := a.tx.FirstAfter(pid, "", order)
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
	if after == nil {
		last, err := a.tx.Last(pid, "")
		if err != nil {
			return "", goerr.Wrap(err, "failed to find last block")
		}
		if last != nil && last.ID == self {
			children, err := a.tx.Children(pid, "")
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
		return model.KeyBetween(upper(last), "")
	}
	target, err := a.resolve(*after)
	if err != nil {
		return "", err
	}
	if target == "" {
		first, err := next("")
		if err != nil {
			return "", err
		}
		return model.KeyBetween("", upper(first))
	}
	if target == string(self) {
		return "", goerr.Wrap(model.ErrInvalidArgument, "cannot place a block after itself")
	}
	anchor, err := a.tx.GetBlock(pid, model.BlockID(target))
	if err != nil || anchor.ParentID != "" {
		return "", goerr.Wrap(model.ErrInvalidArgument, "anchor block not found among top-level blocks", goerr.V("after", *after))
	}
	following, err := next(anchor.Order)
	if err != nil {
		return "", err
	}
	return model.KeyBetween(anchor.Order, upper(following))
}

func (a *applier) newBlock(t model.BlockType, content string, props model.BlockProps, parent model.BlockID, order string) *model.Block {
	return &model.Block{
		ID: model.NewBlockID(), WorkspaceID: a.paper.WorkspaceID, PaperID: a.paper.ID, ParentID: parent,
		Order: order, Type: t, Content: content, Props: props.Normalize(t), Rev: 1,
		CreatedBy: a.actor, CreatedAt: a.now, UpdatedBy: a.actor, UpdatedAt: a.now,
	}
}

// normalizeContent canonicalises inline markup (D10) and rejects what the
// markup does not allow.
func normalizeContent(t model.BlockType, content string) (string, error) {
	if !t.UsesMarkup() {
		return content, nil
	}
	out, err := markup.Normalize(content)
	if err != nil {
		return "", goerr.Wrap(model.ErrInvalidArgument, "invalid inline markup", goerr.V("cause", err.Error()))
	}
	return out, nil
}

// checkImage requires an attached image to be an image of this paper.
func (a *applier) checkImage(props model.BlockProps) error {
	if props.AttachmentID == "" {
		return nil
	}
	if !model.ValidID(string(props.AttachmentID)) {
		return goerr.Wrap(model.ErrInvalidArgument, "invalid attachment id")
	}
	att, err := a.u.repo.GetAttachment(a.ctx, a.paper.WorkspaceID, a.paper.ID, props.AttachmentID)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return goerr.Wrap(model.ErrInvalidArgument, "the attachment does not belong to this paper", goerr.V("attachment_id", props.AttachmentID))
		}
		return goerr.Wrap(err, "failed to check attachment")
	}
	if !att.IsImage() {
		return goerr.Wrap(model.ErrInvalidArgument, "an image block shows only image attachments", goerr.V("attachment_id", props.AttachmentID))
	}
	return nil
}

func ptr[T any](v T) *T { return &v }

func (a *applier) insert(op Op) (model.BlockID, error) {
	if op.Type == nil {
		return "", goerr.Wrap(model.ErrInvalidArgument, "insert requires type")
	}
	t := *op.Type
	if err := t.Validate(); err != nil {
		return "", err
	}
	if t.RequiredParent() != "" {
		return "", goerr.Wrap(model.ErrInvalidArgument, "table cells are created with rows and columns", goerr.V("type", t))
	}
	if t == model.BlockHTMLPage {
		return "", goerr.Wrap(model.ErrInvalidArgument, "html_page blocks are made only with an HTML paper; create one with paper_kind html")
	}
	if (op.PaperKind != nil || op.HTML != nil) && t != model.BlockPaper {
		return "", goerr.Wrap(model.ErrInvalidArgument, "paper_kind and html are only for inserting a paper block", goerr.V("type", t))
	}
	order, err := a.orderAfter(op.After, "")
	if err != nil {
		return "", err
	}
	content := ""
	if op.Content != nil {
		content = *op.Content
	}
	if content, err = normalizeContent(t, content); err != nil {
		return "", err
	}
	props := model.BlockProps{}
	if op.Props != nil {
		props = *op.Props
	}
	if t == model.BlockHeading && props.Level == 0 {
		props.Level = 1
	}
	// Server-issued identities only: lines, child papers and attachments
	// the client names are ignored or checked.
	props.Rows, props.Cols, props.PaperID = nil, nil, ""
	if t == model.BlockImage {
		if err := a.checkImage(props); err != nil {
			return "", err
		}
	}
	if t == model.BlockHTML {
		if err := a.u.checkDatabaseLinks(a.ctx, a.paper.WorkspaceID, props.Databases, nil); err != nil {
			return "", err
		}
	}

	var child *model.Paper
	if t == model.BlockPaper {
		if a.paper.IsPrivate() {
			return "", goerr.Wrap(model.ErrInvalidArgument, "a private paper has no child papers")
		}
		if a.parentable == nil {
			ok, err := a.u.canHaveChildren(a.ctx, a.paper)
			if err != nil {
				return "", err
			}
			a.parentable = &ok
		}
		if !*a.parentable {
			return "", goerr.Wrap(model.ErrInvalidArgument, "an unfiled paper has no child papers; move it under a parent first", goerr.V("paper_id", a.paper.ID))
		}
		title := ""
		if op.Title != nil {
			title = strings.TrimSpace(*op.Title)
		}
		if err := model.ValidateTitle(title); err != nil {
			return "", err
		}
		icon, err := opIcon(op)
		if err != nil {
			return "", err
		}
		kind := model.PaperKindBlocks
		if op.PaperKind != nil && *op.PaperKind == model.PaperKindDatabase {
			kind = model.PaperKindDatabase
		}
		child = &model.Paper{
			ID: model.NewPaperID(), WorkspaceID: a.paper.WorkspaceID, Kind: kind, Title: title, Icon: icon, ParentID: a.paper.ID,
			Visibility: model.VisibilityPublic,
			CreatedBy:  a.actor, CreatedAt: a.now, UpdatedBy: a.actor, UpdatedAt: a.now,
		}
		if sc := a.children[a.idx]; sc != nil {
			child.ID, child.Kind = sc.id, model.PaperKindHTML
		}
		props.PaperID = child.ID
	}

	var grid [][]string
	if t == model.BlockTable {
		grid, err = tableGrid(op)
		if err != nil {
			return "", err
		}
		key := ""
		for range grid[0] {
			if key, err = model.KeyBetween(key, ""); err != nil {
				return "", err
			}
			props.Cols = append(props.Cols, model.TableLine{ID: model.NewLineID(), Order: key})
		}
		key = ""
		for range grid {
			if key, err = model.KeyBetween(key, ""); err != nil {
				return "", err
			}
			props.Rows = append(props.Rows, model.TableLine{ID: model.NewLineID(), Order: key})
		}
	}

	b := a.newBlock(t, content, props, "", order)
	if err := a.addRef(op.Ref, string(b.ID)); err != nil {
		return "", err
	}
	if child != nil {
		if err := a.tx.PutPaper(child); err != nil {
			return "", goerr.Wrap(err, "failed to create child paper")
		}
		if sc := a.children[a.idx]; sc != nil {
			if _, err := newHTMLPage(a.tx, child, sc.html, a.actor); err != nil {
				return "", err
			}
		}
		a.others[child.ID] = child
	}
	if err := a.put(b); err != nil {
		return "", err
	}
	a.created[b.ID] = true
	a.inserted = true
	for r, row := range props.Rows {
		for c, col := range props.Cols {
			if err := a.newCell(b, row.ID, col.ID, grid[r][c]); err != nil {
				return "", err
			}
		}
	}
	return b.ID, nil
}

func tableGrid(op Op) ([][]string, error) {
	grid := op.Cells
	if len(grid) == 0 {
		rows, cols := op.Rows, op.Cols
		if rows == 0 {
			rows = 2
		}
		if cols == 0 {
			cols = 2
		}
		grid = make([][]string, rows)
		for i := range grid {
			grid[i] = make([]string, cols)
		}
	}
	cols := 0
	for _, r := range grid {
		cols = max(cols, len(r))
	}
	if len(grid) > model.MaxTableRows || cols > model.MaxTableCols || cols == 0 {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "table size out of range", goerr.V("rows", len(grid)), goerr.V("cols", cols))
	}
	out := make([][]string, len(grid))
	for i, r := range grid {
		out[i] = make([]string, cols)
		copy(out[i], r)
	}
	return out, nil
}

// cellOrder is the fixed order key of table cells; cells are placed by
// their row and column, so their sibling order is irrelevant.
const cellOrder = "V"

func (a *applier) newCell(table *model.Block, row, col model.LineID, content string) error {
	content, err := normalizeContent(model.BlockTableCell, content)
	if err != nil {
		return err
	}
	cell := a.newBlock(model.BlockTableCell, content, model.BlockProps{RowID: row, ColID: col}, table.ID, cellOrder)
	if err := a.put(cell); err != nil {
		return err
	}
	a.created[cell.ID] = true
	a.inserted = true
	return nil
}

// get reads a block as it is now in the batch, remembering the pre-batch
// state of blocks read for the first time.
func (a *applier) get(id model.BlockID) (*model.Block, error) {
	if !model.ValidID(string(id)) {
		// A malformed ID is treated like one that does not exist.
		return nil, goerr.Wrap(model.ErrNotFound, "block not found", goerr.V("block_id", id))
	}
	b, err := a.tx.GetBlock(a.paper.ID, id)
	if err != nil {
		return nil, err
	}
	if !a.created[id] {
		if _, ok := a.orig[id]; !ok {
			a.orig[id] = b.Clone()
		}
	}
	return b, nil
}

// existing loads the block an op edits. A block that is not there was
// deleted by someone else (a conflict for update, nothing to do for delete
// and move), unless this batch deleted it, which is an invalid argument.
func (a *applier) existing(id model.BlockID, base int64) (*model.Block, error) {
	if id == "" {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "block_id is required")
	}
	b, err := a.get(id)
	if err == nil {
		return b, nil
	}
	if !errors.Is(err, model.ErrNotFound) {
		return nil, err
	}
	if _, mine := a.touched[id]; mine {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "block was deleted earlier in this batch", goerr.V("block_id", id))
	}
	a.conflicts = append(a.conflicts, Conflict{BlockID: id, BaseRev: base})
	return nil, errConflictRecorded
}

// checkBase applies the revision rules (docs/model.md, "Base revisions and conflicts").
func (a *applier) checkBase(id model.BlockID, base int64) error {
	if a.created[id] {
		return nil
	}
	orig := a.orig[id]
	if base <= 0 || base > orig.Rev {
		return goerr.Wrap(model.ErrInvalidArgument, "base_rev is missing or newer than the block", goerr.V("block_id", id), goerr.V("base_rev", base), goerr.V("rev", orig.Rev))
	}
	if base == orig.Rev || a.force {
		return nil
	}
	a.conflicts = append(a.conflicts, Conflict{BlockID: id, BaseRev: base, Current: orig.Clone()})
	return errConflictRecorded
}

// bump records a content change: the revision goes one past the pre-batch
// revision (a block created in this batch stays at 1).
func (a *applier) bump(b *model.Block) {
	if !a.created[b.ID] {
		b.Rev = a.orig[b.ID].Rev + 1
	}
	b.UpdatedBy, b.UpdatedAt = a.actor, a.now
}

func (a *applier) update(op Op) error {
	before, err := a.existing(op.BlockID, op.BaseRev)
	if err != nil {
		return err
	}
	if err := a.checkBase(before.ID, op.BaseRev); err != nil {
		return err
	}
	if before.Type == model.BlockPaper {
		return goerr.Wrap(model.ErrInvalidArgument, "a paper block has nothing to update; move or delete it", goerr.V("block_id", before.ID))
	}
	b := before.Clone()
	if op.Type != nil && *op.Type != b.Type {
		nt := *op.Type
		if err := nt.Validate(); err != nil {
			return err
		}
		if b.Type == model.BlockTableCell || nt.RequiredParent() != "" || nt == model.BlockTable || nt == model.BlockPaper || nt == model.BlockHTMLPage {
			return goerr.Wrap(model.ErrInvalidArgument, "this type change is not possible; insert a new block instead", goerr.V("block_id", b.ID), goerr.V("type", nt))
		}
		if b.Type == model.BlockTable {
			if err := a.deleteCells(b, nil); err != nil {
				return err
			}
		}
		b.Type = nt
		if b.Type == model.BlockHeading && b.Props.Level == 0 && (op.Props == nil || op.Props.Level == 0) {
			b.Props.Level = 1
		}
	}
	if op.Content != nil {
		b.Content = *op.Content
	}
	if b.Content, err = normalizeContent(b.Type, b.Content); err != nil {
		return err
	}
	if op.Props != nil {
		props := *op.Props
		// Rows, columns and cell positions change only through the row
		// and column ops.
		props.Rows, props.Cols = before.Props.Rows, before.Props.Cols
		props.RowID, props.ColID = before.Props.RowID, before.Props.ColID
		b.Props = props
	}
	if b.Type == model.BlockTable && before.Type == model.BlockTable {
		b.Props.Rows, b.Props.Cols = before.Props.Rows, before.Props.Cols
	}
	b.Props = b.Props.Normalize(b.Type)
	if b.Type == model.BlockDivider || b.Type == model.BlockTable {
		b.Content = ""
	}
	if b.Type == model.BlockImage && b.Props.AttachmentID != before.Props.AttachmentID {
		if err := a.checkImage(b.Props); err != nil {
			return err
		}
	}
	if b.Type == model.BlockHTML && !slices.Equal(b.Props.Databases, before.Props.Databases) {
		if err := a.u.checkDatabaseLinks(a.ctx, a.paper.WorkspaceID, b.Props.Databases, before.Props.Databases); err != nil {
			return err
		}
	}
	if model.SameContents(b, before) {
		return nil
	}
	a.bump(b)
	return a.put(b)
}

func (a *applier) delete(op Op) error {
	b, err := a.existing(op.BlockID, op.BaseRev)
	if errors.Is(err, errConflictRecorded) {
		// Someone else already deleted it: the delete is done.
		a.conflicts = a.conflicts[:len(a.conflicts)-1]
		return nil
	}
	if err != nil {
		return err
	}
	if b.Type == model.BlockTableCell {
		return goerr.Wrap(model.ErrInvalidArgument, "table cells are removed with their row or column", goerr.V("block_id", b.ID))
	}
	if err := a.checkBase(b.ID, op.BaseRev); err != nil {
		return err
	}
	if b.Type == model.BlockTable {
		if err := a.deleteCells(b, nil); err != nil {
			return err
		}
	}
	if b.Type == model.BlockPaper {
		gone, err := a.dropChild(b)
		if err != nil {
			return err
		}
		if !gone {
			return nil // a drop job removes the block or archives the child
		}
	}
	return a.remove(b)
}

// dropChild handles deleting the paper block of a child. A block whose
// child is gone or lives elsewhere is simply removed (true). Otherwise a
// drop job takes over (false): it sends the child to the trash with
// everything under it and removes the block when all of it is the caller's,
// and archives the child with everything under it otherwise.
func (a *applier) dropChild(b *model.Block) (bool, error) {
	child, err := a.tx.GetPaper(b.Props.PaperID)
	if errors.Is(err, model.ErrNotFound) {
		return true, nil // a dangling block
	}
	if err != nil {
		return false, goerr.Wrap(err, "failed to get child paper", goerr.V("paper_id", b.Props.PaperID))
	}
	if child.ParentID != a.paper.ID || child.IsTrashed() {
		return true, nil // it lives elsewhere now
	}
	if child.IsBusy() {
		return false, busy(child)
	}
	j := a.u.newJob(a.actor, a.paper.WorkspaceID, model.JobDrop, child.ID, a.now)
	j.BlockID = b.ID
	if err := a.tx.PutJob(j); err != nil {
		return false, goerr.Wrap(err, "failed to store job")
	}
	a.jobs = append(a.jobs, j)
	return false, nil
}

// move changes only the position, so it never conflicts: the last writer
// wins on order. Moving a block someone else deleted is a no-op.
func (a *applier) move(op Op) error {
	before, err := a.existing(op.BlockID, op.BaseRev)
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
	order, err := a.orderAfter(op.After, before.ID)
	if err != nil {
		return err
	}
	if order == before.Order {
		return nil
	}
	b := before.Clone()
	b.Order = order
	return a.put(b)
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
	a.paper.Title = title
	a.titleChanged, a.paperChanged = true, true
	return nil
}

func (a *applier) setIcon(op Op) error {
	icon, err := opIcon(op)
	if err != nil {
		return err
	}
	if icon == a.paper.Icon {
		return nil
	}
	a.paper.Icon = icon
	a.paperChanged = true
	return nil
}

// htmlPaperOp reports whether the op works on an HTML paper, whose body is
// written only with set_html.
func htmlPaperOp(k OpKind) bool {
	return k == OpSetTitle || k == OpSetIcon || k == OpSetHTML
}

// opIcon is the icon set_icon carries (required) or a new paper's icon
// (empty when absent), trimmed and checked.
func opIcon(op Op) (string, error) {
	if op.Icon == nil {
		if op.Kind == OpSetIcon {
			return "", goerr.Wrap(model.ErrInvalidArgument, "set_icon requires icon")
		}
		return "", nil
	}
	icon := model.NormalizeIcon(*op.Icon)
	if err := model.ValidateIcon(icon); err != nil {
		return "", err
	}
	return icon, nil
}

func (a *applier) insertMarkdown(op Op) error {
	if op.Markdown == nil {
		return goerr.Wrap(model.ErrInvalidArgument, "insert_markdown requires markdown")
	}
	after := op.After
	for _, d := range markdown.Parse(*op.Markdown) {
		in := Op{Kind: OpInsert, Type: ptr(d.Type), Content: ptr(d.Content), Props: ptr(d.Props), After: after, Cells: d.Cells}
		if d.Type == model.BlockImage && strings.HasPrefix(d.Props.URL, "attachment:") {
			in.Props.AttachmentID = model.AttachmentID(strings.TrimPrefix(d.Props.URL, "attachment:"))
			in.Props.URL = ""
		}
		id, err := a.insert(in)
		if err != nil {
			return err
		}
		after = ptr(string(id))
	}
	return nil
}

// --- tables

func (a *applier) table(op Op) (*model.Block, error) {
	if op.BlockID == "" {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "block_id is required")
	}
	b, err := a.get(op.BlockID)
	if errors.Is(err, model.ErrNotFound) {
		if _, mine := a.touched[op.BlockID]; mine {
			return nil, goerr.Wrap(model.ErrInvalidArgument, "table was deleted earlier in this batch", goerr.V("block_id", op.BlockID))
		}
		a.conflicts = append(a.conflicts, Conflict{BlockID: op.BlockID})
		return nil, errConflictRecorded
	}
	if err != nil {
		return nil, err
	}
	if b.Type != model.BlockTable {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "the block is not a table", goerr.V("block_id", b.ID))
	}
	return b, nil
}

func sortLines(lines []model.TableLine) {
	slices.SortFunc(lines, func(x, y model.TableLine) int {
		if c := strings.Compare(x.Order, y.Order); c != 0 {
			return c
		}
		return strings.Compare(string(x.ID), string(y.ID))
	})
}

func lineKeyAfter(lines []model.TableLine, after *string, self model.LineID, resolve func(string) (string, error)) (string, error) {
	sorted := slices.Clone(lines)
	sorted = slices.DeleteFunc(sorted, func(l model.TableLine) bool { return l.ID == self })
	slices.SortFunc(sorted, func(x, y model.TableLine) int { return strings.Compare(x.Order+string(x.ID), y.Order+string(y.ID)) })
	if after == nil {
		lo := ""
		if len(sorted) > 0 {
			lo = sorted[len(sorted)-1].Order
		}
		return model.KeyBetween(lo, "")
	}
	target, err := resolve(*after)
	if err != nil {
		return "", err
	}
	if target == "" {
		hi := ""
		if len(sorted) > 0 {
			hi = sorted[0].Order
		}
		return model.KeyBetween("", hi)
	}
	for i, l := range sorted {
		if string(l.ID) == target {
			hi := ""
			if i+1 < len(sorted) {
				hi = sorted[i+1].Order
			}
			if hi == l.Order {
				// equal keys (ordered by ID): make room after this one
				hi = ""
			}
			return model.KeyBetween(l.Order, hi)
		}
	}
	return "", goerr.Wrap(model.ErrInvalidArgument, "anchor row or column not found", goerr.V("after", *after))
}

func (a *applier) insertLine(op Op, isRow bool) error {
	t, err := a.table(op)
	if err != nil {
		return err
	}
	b := t.Clone()
	lines, others := &b.Props.Rows, b.Props.Cols
	if !isRow {
		lines, others = &b.Props.Cols, b.Props.Rows
	}
	key, err := lineKeyAfter(*lines, op.After, "", a.resolve)
	if err != nil {
		return err
	}
	line := model.TableLine{ID: model.NewLineID(), Order: key}
	if err := a.addRef(op.Ref, string(line.ID)); err != nil {
		return err
	}
	*lines = append(*lines, line)
	sortLines(*lines)
	a.bump(b)
	if err := a.put(b); err != nil {
		return err
	}
	ordered := slices.Clone(others)
	slices.SortFunc(ordered, func(x, y model.TableLine) int { return strings.Compare(x.Order+string(x.ID), y.Order+string(y.ID)) })
	for i, o := range ordered {
		v := ""
		if i < len(op.Values) {
			v = op.Values[i]
		}
		row, col := line.ID, o.ID
		if !isRow {
			row, col = o.ID, line.ID
		}
		if err := a.newCell(b, row, col, v); err != nil {
			return err
		}
	}
	return nil
}

func (a *applier) lineID(op Op) (model.LineID, error) {
	id, err := a.resolve(string(op.LineID))
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", goerr.Wrap(model.ErrInvalidArgument, "line_id is required")
	}
	return model.LineID(id), nil
}

func (a *applier) deleteLine(op Op, isRow bool) error {
	t, err := a.table(op)
	if errors.Is(err, errConflictRecorded) {
		a.conflicts = a.conflicts[:len(a.conflicts)-1]
		return nil
	}
	if err != nil {
		return err
	}
	id, err := a.lineID(op)
	if err != nil {
		return err
	}
	b := t.Clone()
	lines := &b.Props.Rows
	if !isRow {
		lines = &b.Props.Cols
	}
	n := len(*lines)
	*lines = slices.DeleteFunc(*lines, func(l model.TableLine) bool { return l.ID == id })
	if len(*lines) == n {
		return nil // already gone
	}
	if len(*lines) == 0 {
		return goerr.Wrap(model.ErrInvalidArgument, "a table keeps at least one row and one column; delete the table instead")
	}
	a.bump(b)
	if err := a.put(b); err != nil {
		return err
	}
	// D3: no base revisions; tell the writer who is editing the cells.
	return a.deleteCells(t, func(c *model.Block) bool {
		if isRow {
			return c.Props.RowID == id
		}
		return c.Props.ColID == id
	})
}

func (a *applier) moveLine(op Op, isRow bool) error {
	t, err := a.table(op)
	if errors.Is(err, errConflictRecorded) {
		a.conflicts = a.conflicts[:len(a.conflicts)-1]
		return nil
	}
	if err != nil {
		return err
	}
	id, err := a.lineID(op)
	if err != nil {
		return err
	}
	b := t.Clone()
	lines := b.Props.Rows
	if !isRow {
		lines = b.Props.Cols
	}
	idx := slices.IndexFunc(lines, func(l model.TableLine) bool { return l.ID == id })
	if idx < 0 {
		return nil // deleted meanwhile: nothing to move
	}
	key, err := lineKeyAfter(lines, op.After, id, a.resolve)
	if err != nil {
		return err
	}
	if key == lines[idx].Order {
		return nil
	}
	lines[idx].Order = key
	sortLines(lines)
	a.bump(b)
	return a.put(b)
}

// deleteCells removes the cells of a table that match keep (all if nil)
// and reports who was editing them.
func (a *applier) deleteCells(table *model.Block, match func(*model.Block) bool) error {
	cells, err := a.tx.Children(a.paper.ID, table.ID)
	if err != nil {
		return goerr.Wrap(err, "failed to list table cells")
	}
	for _, c := range cells {
		if match != nil && !match(c) {
			continue
		}
		if match != nil {
			if err := a.noteEditing(c.ID); err != nil {
				return err
			}
		}
		if err := a.remove(c); err != nil {
			return err
		}
	}
	return nil
}

func (a *applier) noteEditing(id model.BlockID) error {
	if a.presence == nil {
		evs, err := a.u.repo.ListEvents(a.ctx, a.paper.WorkspaceID, a.paper.ID, a.now.Add(-model.PresenceTTL))
		if err != nil {
			return goerr.Wrap(err, "failed to read presence")
		}
		a.presence = append([]*model.Event{}, evs...)
	}
	latest := map[model.UserID]*model.Presence{}
	for _, ev := range a.presence {
		if ev.Type == model.EventPresence && ev.Presence != nil {
			latest[ev.Presence.Actor.UserID] = ev.Presence
		}
	}
	for uid, pr := range latest {
		if pr.BlockID == id && !pr.Leaving && pr.Until.After(a.now) && uid != a.actor.UserID {
			a.result.Editing = append(a.result.Editing, Editing{BlockID: id, Actor: pr.Actor})
		}
	}
	return nil
}

func (a *applier) apply(op Op) error {
	if a.paper.IsHTML() && !htmlPaperOp(op.Kind) {
		return goerr.Wrap(model.ErrInvalidArgument, "an HTML paper's body is written only with set_html", goerr.V("op", op.Kind), goerr.V("paper_id", a.paper.ID))
	}
	switch op.Kind {
	case OpSetHTML:
		return a.setHTML(op)
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
	case OpSetIcon:
		return a.setIcon(op)
	case OpInsertMarkdown:
		return a.insertMarkdown(op)
	case OpInsertRow:
		return a.insertLine(op, true)
	case OpInsertCol:
		return a.insertLine(op, false)
	case OpDeleteRow:
		return a.deleteLine(op, true)
	case OpDeleteCol:
		return a.deleteLine(op, false)
	case OpMoveRow:
		return a.moveLine(op, true)
	case OpMoveCol:
		return a.moveLine(op, false)
	default:
		return goerr.Wrap(model.ErrInvalidArgument, "unknown op", goerr.V("op", op.Kind))
	}
}

// finish writes the paper and the feed entries.
func (a *applier) finish() error {
	if a.inserted {
		n, err := a.tx.CountBlocks(a.paper.ID)
		if err != nil {
			return goerr.Wrap(err, "failed to count blocks")
		}
		if n > model.MaxBlocksPerPaper {
			return goerr.Wrap(model.ErrInvalidArgument, "the paper would have too many blocks", goerr.V("max", model.MaxBlocksPerPaper))
		}
	}
	if len(a.order) > 0 && a.now.Sub(a.paper.UpdatedAt) >= a.u.cfg.PaperTouchInterval {
		a.paperChanged = true
	}
	if a.paperChanged {
		a.paper.UpdatedBy, a.paper.UpdatedAt = a.actor, a.now
		if err := a.tx.PutPaper(a.paper); err != nil {
			return goerr.Wrap(err, "failed to update paper")
		}
	}
	ws, pid := a.paper.WorkspaceID, a.paper.ID
	ev := func(e *model.Event) error {
		e.WorkspaceID, e.At = ws, a.now
		if e.PaperID == "" {
			e.PaperID = pid
		}
		return a.tx.AddEvent(e)
	}
	for _, id := range a.order {
		if b := a.touched[id]; b != nil {
			a.result.Blocks = append(a.result.Blocks, b)
			if err := ev(&model.Event{Type: model.EventBlockPut, Block: b}); err != nil {
				return err
			}
		} else {
			a.result.Deleted = append(a.result.Deleted, id)
			if err := ev(&model.Event{Type: model.EventBlockDeleted, BlockID: id}); err != nil {
				return err
			}
		}
	}
	var papers []*model.Paper
	if a.paperChanged {
		papers = append(papers, a.paper)
	}
	for _, q := range a.others {
		papers = append(papers, q)
	}
	for _, q := range papers {
		for _, e := range paperUpdatedEvents(q) {
			if err := ev(e); err != nil {
				return err
			}
		}
	}
	a.result.Paper = a.paper
	a.result.Refs = a.refs
	return nil
}

// Apply runs a batch of operations on a paper atomically. An op based on an
// older revision of a block whose contents someone else has changed since
// fails the whole batch with a *ConflictError listing every such op, unless
// in.Force is set. A paper-level op (move_paper, set_visibility,
// save_version, restore_version, archive, unarchive) must be the only op of
// its batch.
func (u *UseCases) Apply(ctx context.Context, p *model.Principal, wsID model.WorkspaceID, paperID model.PaperID, in BatchInput) (*BatchResult, error) {
	if len(in.Ops) == 0 {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "no operations")
	}
	if len(in.Ops) > u.cfg.MaxOpsPerBatch {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "too many operations", goerr.V("count", len(in.Ops)), goerr.V("max", u.cfg.MaxOpsPerBatch))
	}
	for _, op := range in.Ops {
		if op.Kind.paperLevel() {
			if len(in.Ops) != 1 {
				return nil, goerr.Wrap(model.ErrInvalidArgument, "a paper-level operation must be the only operation of its batch", goerr.V("op", op.Kind))
			}
			return u.applyPaperOp(ctx, p, wsID, paperID, op)
		}
	}
	_, target, err := u.writablePaper(ctx, p, wsID, paperID)
	if err != nil {
		return nil, err
	}
	return u.applyBatch(ctx, p, p.Actor(), target, in, nil)
}

// applyBatch runs the ops of a batch on target, a paper the caller may
// write, as actor. p is the principal behind the request, nil when papers
// itself writes (demo samples); only paper-level ops need it, and those never
// come here. check, when set, runs first inside the transaction and may stop
// it before anything is written.
func (u *UseCases) applyBatch(ctx context.Context, p *model.Principal, actor model.Actor, target *model.Paper, in BatchInput, check func(a *applier) error) (*BatchResult, error) {
	wsID, paperID := target.WorkspaceID, target.ID
	html, children, err := u.stageBatchHTML(ctx, actor, target, in.Ops)
	if err != nil {
		return nil, err
	}

	now := u.now()
	var result *BatchResult
	var jobs []*model.Job
	err = u.repo.RunTx(ctx, wsID, func(tx interfaces.Tx) error {
		paper, err := tx.GetPaper(paperID)
		if err != nil {
			return goerr.Wrap(err, "failed to get paper", goerr.V("paper_id", paperID))
		}
		if paper.IsBusy() {
			return busy(paper)
		}
		if paper.IsDatabase() {
			res, err := u.applyDatabase(ctx, tx, actor, paper, in.Ops, now)
			if err != nil {
				return err
			}
			result = res
			return nil
		}
		for _, op := range in.Ops {
			if op.Kind.databaseOp() {
				return goerr.Wrap(model.ErrInvalidArgument, "this operation is only for a database", goerr.V("op", op.Kind), goerr.V("paper_id", paper.ID))
			}
		}
		a := &applier{
			u: u, ctx: ctx, tx: tx, p: p, paper: paper, actor: actor, now: now, force: in.Force,
			orig: map[model.BlockID]*model.Block{}, created: map[model.BlockID]bool{},
			touched: map[model.BlockID]*model.Block{}, refs: map[string]string{},
			others: map[model.PaperID]*model.Paper{}, bytes: map[model.BlockID]int{},
			result: &BatchResult{}, html: html, children: children,
		}
		if check != nil {
			if err := check(a); err != nil {
				return err
			}
		}
		for i, op := range in.Ops {
			a.idx = i
			if err := a.apply(op); errors.Is(err, errConflictRecorded) {
				continue
			} else if err != nil {
				return goerr.Wrap(err, fmt.Sprintf("op #%d (%s) failed", i, op.Kind), goerr.V("index", i))
			}
		}
		if len(a.conflicts) > 0 {
			return &ConflictError{Conflicts: a.conflicts}
		}
		if err := a.finish(); err != nil {
			return err
		}
		result, jobs = a.result, a.jobs
		return nil
	})
	if err != nil {
		return nil, err
	}
	u.kick(ctx, jobs)
	return result, nil
}

// stageBatchHTML checks the HTML a batch carries and stores it before the
// transaction: the document of set_html (at most one, for an HTML paper),
// and those of the HTML papers its inserts create, under IDs issued now.
func (u *UseCases) stageBatchHTML(ctx context.Context, actor model.Actor, paper *model.Paper, ops []Op) (*stagedHTML, map[int]*stagedChild, error) {
	var html *stagedHTML
	children := map[int]*stagedChild{}
	sets := 0
	// What the transaction would refuse anyway is refused before anything
	// is stored.
	for _, op := range ops {
		if op.PaperKind != nil && op.Kind != OpInsert {
			return nil, nil, goerr.Wrap(model.ErrInvalidArgument, "paper_kind is only for inserting a paper block", goerr.V("op", op.Kind))
		}
		if paper.IsHTML() && !htmlPaperOp(op.Kind) {
			return nil, nil, goerr.Wrap(model.ErrInvalidArgument, "an HTML paper's body is written only with set_html", goerr.V("op", op.Kind), goerr.V("paper_id", paper.ID))
		}
	}
	for _, op := range ops {
		switch {
		case op.Kind == OpSetHTML:
			if sets++; sets > 1 {
				return nil, nil, goerr.Wrap(model.ErrInvalidArgument, "a batch has at most one set_html")
			}
			if !paper.IsHTML() {
				return nil, nil, goerr.Wrap(model.ErrInvalidArgument, "set_html is only for HTML papers", goerr.V("paper_id", paper.ID))
			}
			if op.HTML == nil {
				return nil, nil, goerr.Wrap(model.ErrInvalidArgument, "set_html requires html")
			}
		case op.Kind == OpInsert && op.PaperKind != nil:
			if err := op.PaperKind.Validate(); err != nil {
				return nil, nil, err
			}
			if *op.PaperKind == model.PaperKindHTML && op.HTML == nil {
				return nil, nil, goerr.Wrap(model.ErrInvalidArgument, "an HTML paper is created with its html")
			}
			if *op.PaperKind != model.PaperKindHTML && op.HTML != nil {
				return nil, nil, goerr.Wrap(model.ErrInvalidArgument, "html is only for an HTML paper (paper_kind html)")
			}
			if op.Type == nil || *op.Type != model.BlockPaper {
				return nil, nil, goerr.Wrap(model.ErrInvalidArgument, "paper_kind and html are only for inserting a paper block")
			}
			if *op.PaperKind == model.PaperKindHTML {
				ok, err := u.canHaveChildren(ctx, paper)
				if err != nil {
					return nil, nil, err
				}
				if !ok {
					return nil, nil, goerr.Wrap(model.ErrInvalidArgument, "this paper cannot have child papers", goerr.V("paper_id", paper.ID))
				}
			}
		case op.HTML != nil:
			return nil, nil, goerr.Wrap(model.ErrInvalidArgument, "html is only for set_html and for inserting an HTML paper", goerr.V("op", op.Kind))
		}
		if op.HTML != nil {
			if err := checkHTML(*op.HTML); err != nil {
				return nil, nil, err
			}
		}
		if op.Databases != nil {
			if op.HTML == nil {
				return nil, nil, goerr.Wrap(model.ErrInvalidArgument, "databases is only for set_html and for inserting an HTML paper; an html block takes props.databases", goerr.V("op", op.Kind))
			}
			var known []model.DatabaseLink
			if op.Kind == OpSetHTML {
				// Links the document already has stay valid when sent back,
				// even to a database removed since (it reads as missing).
				links, err := u.currentDatabaseLinks(ctx, paper)
				if err != nil {
					return nil, nil, err
				}
				known = links
			}
			if err := u.checkDatabaseLinks(ctx, paper.WorkspaceID, *op.Databases, known); err != nil {
				return nil, nil, err
			}
		}
	}
	for i, op := range ops {
		switch {
		case op.Kind == OpSetHTML:
			s, err := u.stageHTML(ctx, actor, paper.WorkspaceID, paper.ID, *op.HTML)
			if err != nil {
				return nil, nil, err
			}
			s.keep = op.Databases == nil
			if op.Databases != nil {
				s.databases = *op.Databases
			}
			html = s
		case op.Kind == OpInsert && op.HTML != nil:
			id := model.NewPaperID()
			s, err := u.stageHTML(ctx, actor, paper.WorkspaceID, id, *op.HTML)
			if err != nil {
				return nil, nil, err
			}
			if op.Databases != nil {
				s.databases = *op.Databases
			}
			children[i] = &stagedChild{id: id, html: s}
		}
	}
	return html, children, nil
}

func (u *UseCases) applyPaperOp(ctx context.Context, p *model.Principal, ws model.WorkspaceID, paper model.PaperID, op Op) (*BatchResult, error) {
	switch op.Kind {
	case OpMovePaper:
		return u.MovePaper(ctx, p, ws, paper, MoveInput{Parent: op.Parent, After: op.After, Workspace: op.Workspace, Unfiled: op.Unfiled})
	case OpSetVisibility:
		return u.SetVisibility(ctx, p, ws, paper, VisibilityInput{Visibility: op.Visibility, Parent: op.Parent, After: op.After})
	case OpSaveVersion:
		v, err := u.SaveVersion(ctx, p, ws, paper, op.Note)
		if err != nil {
			return nil, err
		}
		return &BatchResult{Version: v, Refs: map[string]string{}}, nil
	case OpRestoreVersion:
		return u.RestoreVersion(ctx, p, ws, paper, op.Version)
	case OpArchive:
		return u.Archive(ctx, p, ws, paper, op.Subtree)
	case OpUnarchive:
		return u.Unarchive(ctx, p, ws, paper, op.Subtree)
	}
	return nil, goerr.Wrap(model.ErrInvalidArgument, "unknown op", goerr.V("op", op.Kind))
}
