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
)

// Op is one requested operation. Positioning (insert, move,
// insert_markdown) uses After: nil appends at the end, a pointer to ""
// inserts at the top, and a block ID inserts right after that block.
// Only top-level blocks are positioned; table cells are managed through
// their table's Rows / Cols.
type Op struct {
	Kind        OpKind            `json:"op"`
	BlockID     model.BlockID     `json:"block_id,omitempty"`
	Type        *model.BlockType  `json:"type,omitempty"`
	Content     *string           `json:"content,omitempty"`
	Props       *model.BlockProps `json:"props,omitempty"`
	After       *model.BlockID    `json:"after,omitempty"`
	BaseVersion int64             `json:"base_version,omitempty"`
	Title       *string           `json:"title,omitempty"`
	Markdown    *string           `json:"markdown,omitempty"`
	// Cells fills a new table (insert of type table), row by row.
	Cells [][]string `json:"cells,omitempty"`
}

type CommitInput struct {
	Ops     []Op
	Message string
	// Force writes even to blocks another holder has soft-locked.
	Force bool
	// HolderKey identifies the writer's live connection; it overrides the
	// principal's own key so a browser tab can edit the blocks it locked.
	HolderKey string
}

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
	locks   map[model.BlockID]*model.BlockLock
	holder  string
	force   bool
	changes []model.Change
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

func (a *applier) checkLock(id model.BlockID) error {
	if a.force {
		return nil
	}
	l, ok := a.locks[id]
	if !ok || l.HolderKey == a.holder {
		return nil
	}
	return goerr.Wrap(model.ErrBlockLocked, "block is being edited",
		goerr.V("block_id", id), goerr.V("holder", l.Holder.Name), goerr.V("expires_at", l.ExpiresAt))
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
			if err := a.checkLock(c.ID); err != nil {
				return err
			}
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
		if err := a.checkLock(c.ID); err != nil {
			return err
		}
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
	return b, nil
}

func (a *applier) update(op Op) error {
	before, err := a.get(op.BlockID)
	if err != nil {
		return err
	}
	if err := a.checkLock(before.ID); err != nil {
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
	if op.BaseVersion > 0 && op.BaseVersion < before.Version {
		ch.OverwroteVersion = before.Version
	}
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
	b, err := a.get(op.BlockID)
	if err != nil {
		return err
	}
	if b.Type == model.BlockTableCell {
		return goerr.Wrap(model.ErrInvalidArgument, "table cells are removed by resizing their table", goerr.V("block_id", b.ID))
	}
	if err := a.checkLock(b.ID); err != nil {
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
	return a.record(model.Change{Kind: model.ChangeDelete, BlockID: b.ID, FromVersion: b.Version}, nil)
}

func (a *applier) move(op Op) error {
	before, err := a.get(op.BlockID)
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
	default:
		return goerr.Wrap(model.ErrInvalidArgument, "unknown op", goerr.V("op", op.Kind))
	}
}

// Commit applies operations to a paper atomically and records them as one
// commit. Each changed block gets a new version (with a stored snapshot);
// the last writer wins on a block, and an overwrite of a newer version than
// the writer based its edit on is recorded in the change. Watchers learn
// about the result from the repository's change feed.
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
	active, err := u.repo.ListLocks(ctx, wsID, paperID, now)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list locks")
	}
	locks := make(map[model.BlockID]*model.BlockLock, len(active))
	for _, l := range active {
		locks[l.BlockID] = l
	}
	holder := in.HolderKey
	if holder == "" {
		holder = p.HolderKey
	}

	var result CommitResult
	err = u.repo.RunPaperTx(ctx, wsID, paperID, func(tx interfaces.PaperTx) error {
		a := &applier{tx: tx, paper: tx.Paper(), actor: p.Actor(), now: now, locks: locks, holder: holder, force: in.Force, touched: map[model.BlockID]*model.Block{}}
		for i, op := range in.Ops {
			if err := a.apply(op); err != nil {
				return goerr.Wrap(err, fmt.Sprintf("op #%d (%s) failed", i, op.Kind), goerr.V("index", i))
			}
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
