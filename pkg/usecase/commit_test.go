package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
)

type paperEnv struct {
	*env
	owner, editor, viewer *model.Principal
	ws                    model.WorkspaceID
	paper                 model.PaperID
}

func newPaperEnv(t *testing.T) *paperEnv {
	t.Helper()
	e := newEnv(t)
	owner, editor, viewer := e.user(t, "owner"), e.user(t, "editor"), e.user(t, "viewer")
	ws := e.workspace(t, owner, userBinding(editor, model.RoleEditor), userBinding(viewer, model.RoleViewer))
	view := gt.R1(e.uc.CreatePaper(context.Background(), owner, ws, usecase.CreatePaperInput{Title: "Doc"})).NoError(t)
	return &paperEnv{env: e, owner: owner, editor: editor, viewer: viewer, ws: ws, paper: view.Paper.ID}
}

// commit applies ops, basing every update, delete and restore that names
// no base_version on the block's current version (tests of other behaviour
// should not have to track versions).
func (pe *paperEnv) commit(t *testing.T, p *model.Principal, ops ...usecase.Op) *usecase.CommitResult {
	t.Helper()
	for i, op := range ops {
		if op.BaseVersion != 0 || op.BlockID == "" {
			continue
		}
		switch op.Kind {
		case usecase.OpUpdate, usecase.OpDelete, usecase.OpRestore:
			if b, err := pe.repo.GetBlock(context.Background(), pe.ws, pe.paper, op.BlockID); err == nil {
				ops[i].BaseVersion = b.Version
			}
		}
	}
	return gt.R1(pe.uc.Commit(context.Background(), p, pe.ws, pe.paper, usecase.CommitInput{Ops: ops, Message: "test"})).NoError(t)
}

func (pe *paperEnv) try(p *model.Principal, force bool, ops ...usecase.Op) (*usecase.CommitResult, error) {
	return pe.uc.Commit(context.Background(), p, pe.ws, pe.paper, usecase.CommitInput{Ops: ops, Force: force})
}

func conflictsOf(t *testing.T, err error) []usecase.Conflict {
	t.Helper()
	gt.True(t, errors.Is(err, model.ErrConflict)).Required()
	var ce *usecase.ConflictError
	gt.True(t, errors.As(err, &ce)).Required()
	return ce.Conflicts
}

func (pe *paperEnv) blocks(t *testing.T) []*model.Block {
	t.Helper()
	return gt.R1(pe.uc.GetPaper(context.Background(), pe.owner, pe.ws, pe.paper)).NoError(t).Blocks
}

func topLevel(bs []*model.Block) []*model.Block {
	var out []*model.Block
	for _, b := range bs {
		if b.ParentID == "" {
			out = append(out, b)
		}
	}
	return out
}

func contents(bs []*model.Block) []string {
	var out []string
	for _, b := range topLevel(bs) {
		out = append(out, b.Content)
	}
	return out
}

func insertOp(content string, after *model.BlockID) usecase.Op {
	return usecase.Op{Kind: usecase.OpInsert, Type: ptr(model.BlockParagraph), Content: ptr(content), After: after}
}

func TestCommitInsertPositions(t *testing.T) {
	pe := newPaperEnv(t)
	r := pe.commit(t, pe.editor, insertOp("b", nil), insertOp("d", nil))
	gt.A(t, r.Blocks).Length(2)
	gt.Equal(t, r.Commit.Actor.Kind, model.ActorHuman)
	gt.Equal(t, r.Commit.Actor.UserID, pe.editor.User.ID)
	bID := r.Commit.Changes[0].BlockID

	pe.commit(t, pe.editor, insertOp("a", ptr(model.BlockID(""))), insertOp("c", &bID))
	gt.Equal(t, contents(pe.blocks(t)), []string{"a", "b", "c", "d"})

	_, err := pe.uc.Commit(context.Background(), pe.editor, pe.ws, pe.paper, usecase.CommitInput{Ops: []usecase.Op{insertOp("x", ptr(model.BlockID("missing")))}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestCommitClientChosenID(t *testing.T) {
	pe := newPaperEnv(t)
	id := model.NewBlockID()
	op := insertOp("x", nil)
	op.BlockID = id
	pe.commit(t, pe.editor, op)
	gt.Equal(t, pe.blocks(t)[0].ID, id)

	_, err := pe.uc.Commit(context.Background(), pe.editor, pe.ws, pe.paper, usecase.CommitInput{Ops: []usecase.Op{op}})
	gt.True(t, errors.Is(err, model.ErrAlreadyExists))

	bad := insertOp("x", nil)
	bad.BlockID = "not-a-uuid"
	_, err = pe.uc.Commit(context.Background(), pe.editor, pe.ws, pe.paper, usecase.CommitInput{Ops: []usecase.Op{bad}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestCommitUpdateKeepsHistory(t *testing.T) {
	pe := newPaperEnv(t)
	r := pe.commit(t, pe.editor, insertOp("v1", nil))
	id := r.Commit.Changes[0].BlockID

	r = pe.commit(t, pe.owner, usecase.Op{Kind: usecase.OpUpdate, BlockID: id, Content: ptr("owner"), BaseVersion: 1})
	gt.Equal(t, r.Blocks[0].Version, int64(2))
	gt.Equal(t, r.Commit.Changes[0].FromVersion, int64(1))
	gt.Equal(t, r.Commit.Changes[0].ToVersion, int64(2))
	gt.Equal(t, r.Commit.Changes[0].OverwroteVersion, int64(0))
	gt.Equal(t, pe.blocks(t)[0].UpdatedBy.UserID, pe.owner.User.ID)
	gt.Equal(t, pe.blocks(t)[0].CreatedBy.UserID, pe.editor.User.ID)

	// a no-op update produces no commit
	r = pe.commit(t, pe.owner, usecase.Op{Kind: usecase.OpUpdate, BlockID: id, Content: ptr("owner")})
	gt.Nil(t, r.Commit)

	versions := gt.R1(pe.uc.ListBlockVersions(context.Background(), pe.viewer, pe.ws, pe.paper, id, 0)).NoError(t)
	gt.Equal(t, len(versions), 2)
	gt.Equal(t, versions[0].Content, "owner")
	gt.Equal(t, versions[1].Content, "v1")
}

func TestCommitRequiresBaseVersion(t *testing.T) {
	pe := newPaperEnv(t)
	id := pe.commit(t, pe.editor, insertOp("x", nil)).Blocks[0].ID
	for _, op := range []usecase.Op{
		{Kind: usecase.OpUpdate, BlockID: id, Content: ptr("y")},
		{Kind: usecase.OpDelete, BlockID: id},
		{Kind: usecase.OpRestore, BlockID: id, Version: 1},
	} {
		_, err := pe.try(pe.editor, false, op)
		gt.True(t, errors.Is(err, model.ErrInvalidArgument)).Describef("op %s", op.Kind)
	}
	// a base newer than the block is a client bug
	_, err := pe.try(pe.editor, false, usecase.Op{Kind: usecase.OpUpdate, BlockID: id, Content: ptr("y"), BaseVersion: 9})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	// moves need no base
	pe.commit(t, pe.editor, insertOp("z", nil))
	gt.R1(pe.try(pe.editor, false, usecase.Op{Kind: usecase.OpMove, BlockID: id})).NoError(t)
}

func TestCommitConflicts(t *testing.T) {
	pe := newPaperEnv(t)
	r := pe.commit(t, pe.editor, insertOp("v1", nil), insertOp("other", nil))
	id, other := r.Blocks[0].ID, r.Blocks[1].ID

	// owner and editor both read v1; owner saves first
	pe.commit(t, pe.owner, usecase.Op{Kind: usecase.OpUpdate, BlockID: id, Content: ptr("owner"), BaseVersion: 1})

	// the editor's save based on v1 is rejected as a whole, listing the
	// current state; nothing is written
	_, err := pe.try(pe.editor, false,
		usecase.Op{Kind: usecase.OpUpdate, BlockID: other, Content: ptr("fine"), BaseVersion: 1},
		usecase.Op{Kind: usecase.OpUpdate, BlockID: id, Content: ptr("editor"), BaseVersion: 1},
	)
	cs := conflictsOf(t, err)
	gt.A(t, cs).Length(1)
	gt.Equal(t, cs[0].BlockID, id)
	gt.Equal(t, cs[0].BaseVersion, int64(1))
	gt.Equal(t, cs[0].Current.Content, "owner")
	gt.Equal(t, cs[0].Current.Version, int64(2))
	gt.Equal(t, contents(pe.blocks(t)), []string{"owner", "other"})

	// forcing wins and records whose version was replaced
	r = gt.R1(pe.try(pe.editor, true, usecase.Op{Kind: usecase.OpUpdate, BlockID: id, Content: ptr("editor"), BaseVersion: 1})).NoError(t)
	ch := r.Commit.Changes[0]
	gt.Equal(t, ch.OverwroteVersion, int64(2))
	gt.Equal(t, ch.OverwroteBy.UserID, pe.owner.User.ID)
	gt.Equal(t, pe.blocks(t)[0].Content, "editor")
	// the replaced contents stay in the history
	prev := gt.R1(pe.repo.GetBlockVersion(context.Background(), pe.ws, pe.paper, id, 2)).NoError(t)
	gt.Equal(t, prev.Content, "owner")

	// a delete based on an old version conflicts too
	_, err = pe.try(pe.owner, false, usecase.Op{Kind: usecase.OpDelete, BlockID: id, BaseVersion: 2})
	gt.A(t, conflictsOf(t, err)).Length(1)
}

func TestCommitMovesDoNotConflict(t *testing.T) {
	pe := newPaperEnv(t)
	r := pe.commit(t, pe.editor, insertOp("a", nil), insertOp("b", nil))
	a := r.Blocks[0].ID

	// someone moves a (v2) while the editor edits it based on v1
	pe.commit(t, pe.owner, usecase.Op{Kind: usecase.OpMove, BlockID: a})
	r = gt.R1(pe.try(pe.editor, false, usecase.Op{Kind: usecase.OpUpdate, BlockID: a, Content: ptr("a2"), BaseVersion: 1})).NoError(t)
	gt.Equal(t, r.Commit.Changes[0].OverwroteVersion, int64(0))
	gt.Equal(t, contents(pe.blocks(t)), []string{"b", "a2"}) // content mine, position theirs

	// several ops on one block in one commit share the same base
	r = gt.R1(pe.try(pe.editor, false,
		usecase.Op{Kind: usecase.OpUpdate, BlockID: a, Content: ptr("a3"), BaseVersion: 3},
		usecase.Op{Kind: usecase.OpUpdate, BlockID: a, Content: ptr("a4"), BaseVersion: 3},
	)).NoError(t)
	gt.Equal(t, r.Blocks[0].Content, "a4")
}

func TestCommitOnDeletedBlocks(t *testing.T) {
	pe := newPaperEnv(t)
	id := pe.commit(t, pe.editor, insertOp("x", nil)).Blocks[0].ID
	pe.commit(t, pe.owner, usecase.Op{Kind: usecase.OpDelete, BlockID: id})

	// editing a block someone deleted is a conflict without current state,
	// even with force
	for _, force := range []bool{false, true} {
		_, err := pe.try(pe.editor, force, usecase.Op{Kind: usecase.OpUpdate, BlockID: id, Content: ptr("y"), BaseVersion: 1})
		cs := conflictsOf(t, err)
		gt.Nil(t, cs[0].Current)
	}
	// deleting or moving it again is a no-op
	r := gt.R1(pe.try(pe.editor, false, usecase.Op{Kind: usecase.OpDelete, BlockID: id, BaseVersion: 1})).NoError(t)
	gt.Nil(t, r.Commit)
	r = gt.R1(pe.try(pe.editor, false, usecase.Op{Kind: usecase.OpMove, BlockID: id})).NoError(t)
	gt.Nil(t, r.Commit)
	// a block that never existed is a plain invalid argument
	_, err := pe.try(pe.editor, false, usecase.Op{Kind: usecase.OpUpdate, BlockID: model.NewBlockID(), Content: ptr("y"), BaseVersion: 1})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestCommitRestore(t *testing.T) {
	pe := newPaperEnv(t)
	r := pe.commit(t, pe.editor, insertOp("a", nil), insertOp("b", nil))
	a, b := r.Blocks[0].ID, r.Blocks[1].ID
	pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpUpdate, BlockID: a, Content: ptr("a2")})
	pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpUpdate, BlockID: a, Type: ptr(model.BlockHeading), Content: ptr("a3")})

	// restoring v1 creates v4 with v1's contents
	r = pe.commit(t, pe.owner, usecase.Op{Kind: usecase.OpRestore, BlockID: a, Version: 1})
	ch := r.Commit.Changes[0]
	gt.Equal(t, ch.Kind, model.ChangeRestore)
	gt.Equal(t, ch.FromVersion, int64(3))
	gt.Equal(t, ch.ToVersion, int64(4))
	gt.Equal(t, ch.RestoredVersion, int64(1))
	got := r.Blocks[0]
	gt.Equal(t, got.Type, model.BlockParagraph)
	gt.Equal(t, got.Content, "a")

	// restore checks the base like an update
	_, err := pe.try(pe.editor, false, usecase.Op{Kind: usecase.OpRestore, BlockID: a, Version: 2, BaseVersion: 3})
	gt.A(t, conflictsOf(t, err)).Length(1)
	_, err = pe.try(pe.editor, false, usecase.Op{Kind: usecase.OpRestore, BlockID: a, Version: 9, BaseVersion: 4})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	// a deleted block comes back at its last position with a new version
	pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpDelete, BlockID: b})
	gt.Equal(t, contents(pe.blocks(t)), []string{"a"})
	r = pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpRestore, BlockID: b, Version: 1})
	gt.Equal(t, r.Blocks[0].Version, int64(2))
	gt.Equal(t, contents(pe.blocks(t)), []string{"a", "b"})
	versions := gt.R1(pe.uc.ListBlockVersions(context.Background(), pe.viewer, pe.ws, pe.paper, b, 0)).NoError(t)
	gt.A(t, versions).Length(2)

	// deleted tables cannot be restored (their cells are separate blocks)
	tbl := pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpInsert, Type: ptr(model.BlockTable), Props: &model.BlockProps{Rows: 1, Cols: 1}}).Blocks[0].ID
	pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpDelete, BlockID: tbl})
	_, err = pe.try(pe.editor, false, usecase.Op{Kind: usecase.OpRestore, BlockID: tbl, Version: 1})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestCommitTypeChangeNormalizesProps(t *testing.T) {
	pe := newPaperEnv(t)
	r := pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpInsert, Type: ptr(model.BlockCode), Content: ptr("x"), Props: &model.BlockProps{Language: "go", Level: 2}})
	b := r.Blocks[0]
	gt.Equal(t, b.Props, model.BlockProps{Language: "go"})

	r = pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpUpdate, BlockID: b.ID, Type: ptr(model.BlockHeading)})
	gt.Equal(t, r.Blocks[0].Props, model.BlockProps{Level: 1})

	r = pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpUpdate, BlockID: b.ID, Type: ptr(model.BlockDivider)})
	gt.Equal(t, r.Blocks[0].Content, "")
}

func TestCommitDeleteAndMove(t *testing.T) {
	pe := newPaperEnv(t)
	r := pe.commit(t, pe.editor, insertOp("a", nil), insertOp("b", nil), insertOp("c", nil))
	a, c := r.Commit.Changes[0].BlockID, r.Commit.Changes[2].BlockID

	pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpMove, BlockID: a, After: &c})
	gt.Equal(t, contents(pe.blocks(t)), []string{"b", "c", "a"})

	pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpMove, BlockID: a, After: ptr(model.BlockID(""))})
	gt.Equal(t, contents(pe.blocks(t)), []string{"a", "b", "c"})

	_, err := pe.uc.Commit(context.Background(), pe.editor, pe.ws, pe.paper, usecase.CommitInput{Ops: []usecase.Op{{Kind: usecase.OpMove, BlockID: a, After: &a}}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpDelete, BlockID: a})
	gt.Equal(t, contents(pe.blocks(t)), []string{"b", "c"})
}

func TestCommitIsAtomic(t *testing.T) {
	pe := newPaperEnv(t)
	_, err := pe.uc.Commit(context.Background(), pe.editor, pe.ws, pe.paper, usecase.CommitInput{Ops: []usecase.Op{
		insertOp("ok", nil),
		{Kind: usecase.OpUpdate, BlockID: "missing", Content: ptr("x")},
	}})
	gt.Error(t, err)
	gt.A(t, pe.blocks(t)).Length(0)
	commits := gt.R1(pe.uc.ListCommits(context.Background(), pe.owner, pe.ws, pe.paper, "", 0)).NoError(t)
	gt.A(t, commits).Length(0)
}

func TestCommitSetTitle(t *testing.T) {
	pe := newPaperEnv(t)
	r := pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpSetTitle, Title: ptr(" New ")})
	gt.Equal(t, r.Paper.Title, "New")
	gt.Equal(t, r.Commit.Changes[0].TitleBefore, "Doc")
	gt.Equal(t, r.Commit.Changes[0].TitleAfter, "New")

	_, err := pe.uc.Commit(context.Background(), pe.editor, pe.ws, pe.paper, usecase.CommitInput{Ops: []usecase.Op{{Kind: usecase.OpSetTitle, Title: ptr("a\nb")}}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestCommitTables(t *testing.T) {
	pe := newPaperEnv(t)
	r := pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpInsert, Type: ptr(model.BlockTable), Props: &model.BlockProps{HeaderRow: true}, Cells: [][]string{{"k", "v"}, {"a", "1"}}})
	table := r.Blocks[0]
	gt.Equal(t, table.Props, model.BlockProps{Rows: 2, Cols: 2, HeaderRow: true})
	gt.A(t, r.Commit.Changes).Length(5) // table + 4 cells

	cells := func() map[[2]int]*model.Block {
		m := map[[2]int]*model.Block{}
		for _, b := range pe.blocks(t) {
			if b.ParentID == table.ID {
				m[[2]int{b.Props.Row, b.Props.Col}] = b
			}
		}
		return m
	}
	gt.Equal(t, cells()[[2]int{1, 0}].Content, "a")

	// cells are edited individually and keep their position
	cell := cells()[[2]int{1, 1}]
	pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpUpdate, BlockID: cell.ID, Content: ptr("2"), Props: &model.BlockProps{Row: 9, Col: 9}})
	gt.Equal(t, cells()[[2]int{1, 1}].Content, "2")

	// resizing adds and removes cells
	pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpUpdate, BlockID: table.ID, Props: &model.BlockProps{Rows: 3, Cols: 1, HeaderRow: true}})
	m := cells()
	gt.Equal(t, len(m), 3)
	gt.Equal(t, m[[2]int{0, 0}].Content, "k")
	gt.Equal(t, m[[2]int{2, 0}].Content, "")

	// cells cannot be deleted, inserted or retyped directly
	for _, op := range []usecase.Op{
		{Kind: usecase.OpDelete, BlockID: m[[2]int{0, 0}].ID},
		{Kind: usecase.OpInsert, Type: ptr(model.BlockTableCell)},
		{Kind: usecase.OpUpdate, BlockID: m[[2]int{0, 0}].ID, Type: ptr(model.BlockParagraph)},
	} {
		_, err := pe.uc.Commit(context.Background(), pe.editor, pe.ws, pe.paper, usecase.CommitInput{Ops: []usecase.Op{op}})
		gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	}

	// deleting the table removes its cells
	r = pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpDelete, BlockID: table.ID})
	gt.A(t, pe.blocks(t)).Length(0)
	gt.A(t, r.Deleted).Length(4)

	// tables beyond the cell budget are refused
	_, err := pe.uc.Commit(context.Background(), pe.editor, pe.ws, pe.paper, usecase.CommitInput{Ops: []usecase.Op{{Kind: usecase.OpInsert, Type: ptr(model.BlockTable), Props: &model.BlockProps{Rows: 20, Cols: 20}}}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestCommitInsertMarkdown(t *testing.T) {
	pe := newPaperEnv(t)
	r := pe.commit(t, pe.editor, insertOp("first", nil), insertOp("last", nil))
	first := r.Commit.Changes[0].BlockID
	md := "## Heading\n\nText\n\n```mermaid\ngraph TD; A-->B\n```"
	pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpInsertMarkdown, Markdown: &md, After: &first})
	bs := topLevel(pe.blocks(t))
	gt.Equal(t, contents(bs), []string{"first", "Heading", "Text", "graph TD; A-->B", "last"})
	gt.Equal(t, bs[1].Type, model.BlockHeading)
	gt.Equal(t, bs[3].Type, model.BlockMermaid)
}

func TestCommitPermissions(t *testing.T) {
	pe := newPaperEnv(t)
	_, err := pe.uc.Commit(context.Background(), pe.viewer, pe.ws, pe.paper, usecase.CommitInput{Ops: []usecase.Op{insertOp("x", nil)}})
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))

	outsider := pe.user(t, "outsider")
	_, err = pe.uc.Commit(context.Background(), outsider, pe.ws, pe.paper, usecase.CommitInput{Ops: []usecase.Op{insertOp("x", nil)}})
	gt.True(t, errors.Is(err, model.ErrNotFound))

	readToken := &model.Principal{User: pe.owner.User, Token: &model.APIToken{ID: "t", Scope: model.ScopeRead}}
	_, err = pe.uc.Commit(context.Background(), readToken, pe.ws, pe.paper, usecase.CommitInput{Ops: []usecase.Op{insertOp("x", nil)}})
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))
}

func TestCommitAgentActor(t *testing.T) {
	pe := newPaperEnv(t)
	agent := &model.Principal{User: pe.editor.User, Token: &model.APIToken{ID: "tok", Kind: model.TokenAgent, AgentName: "Claude", Scope: model.ScopeWrite}}
	r := pe.commit(t, agent, insertOp("from ai", nil))
	gt.Equal(t, r.Commit.Actor.Kind, model.ActorAgent)
	gt.Equal(t, r.Commit.Actor.AgentName, "Claude")
	gt.Equal(t, pe.blocks(t)[0].CreatedBy.Kind, model.ActorAgent)
}

func TestCommitLimits(t *testing.T) {
	pe := newPaperEnv(t)
	_, err := pe.uc.Commit(context.Background(), pe.editor, pe.ws, pe.paper, usecase.CommitInput{})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	ops := make([]usecase.Op, 501)
	for i := range ops {
		ops[i] = insertOp("x", nil)
	}
	_, err = pe.uc.Commit(context.Background(), pe.editor, pe.ws, pe.paper, usecase.CommitInput{Ops: ops})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	_, err = pe.uc.Commit(context.Background(), pe.editor, pe.ws, pe.paper, usecase.CommitInput{Ops: []usecase.Op{{Kind: "explode"}}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestCommitIsVisibleToWatchers(t *testing.T) {
	pe := newPaperEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := gt.R1(pe.uc.Watch(ctx, pe.viewer, pe.ws, pe.paper)).NoError(t)
	r := pe.commit(t, pe.editor, insertOp("x", nil))
	select {
	case ev := <-ch:
		gt.Equal(t, ev.Type, model.EventBlockPut)
		gt.Equal(t, ev.Block.ID, r.Blocks[0].ID)
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}
}

func TestCommitTouchesPaperSparingly(t *testing.T) {
	pe := newPaperEnv(t)
	ctx := context.Background()
	created := gt.R1(pe.uc.GetPaper(ctx, pe.owner, pe.ws, pe.paper)).NoError(t).Paper.UpdatedAt

	pe.commit(t, pe.editor, insertOp("x", nil))
	p := gt.R1(pe.uc.GetPaper(ctx, pe.owner, pe.ws, pe.paper)).NoError(t).Paper
	gt.Equal(t, p.UpdatedAt, created) // within the touch interval

	pe.clock.Advance(2 * time.Minute)
	pe.commit(t, pe.editor, insertOp("y", nil))
	p = gt.R1(pe.uc.GetPaper(ctx, pe.owner, pe.ws, pe.paper)).NoError(t).Paper
	gt.Equal(t, p.UpdatedAt, pe.clock.Now())
	gt.Equal(t, p.UpdatedBy.UserID, pe.editor.User.ID)

	// a title change always writes the paper
	pe.clock.Advance(time.Second)
	pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpSetTitle, Title: ptr("Renamed")})
	p = gt.R1(pe.uc.GetPaper(ctx, pe.owner, pe.ws, pe.paper)).NoError(t).Paper
	gt.Equal(t, p.Title, "Renamed")
	gt.Equal(t, p.UpdatedAt, pe.clock.Now())
}

func TestCommitMoveToEnd(t *testing.T) {
	pe := newPaperEnv(t)
	r := pe.commit(t, pe.editor, insertOp("a", nil), insertOp("b", nil))
	a, b := r.Blocks[0].ID, r.Blocks[1].ID
	pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpMove, BlockID: a})
	gt.Equal(t, contents(pe.blocks(t)), []string{"b", "a"})
	// moving the last block to the end is a no-op
	r = pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpMove, BlockID: a})
	gt.Nil(t, r.Commit)
	_ = b
}

func TestCommitContentBudget(t *testing.T) {
	pe := newPaperEnv(t)
	big := strings.Repeat("x", model.MaxContentLength)
	n := model.MaxCommitContentBytes/model.MaxContentLength + 1
	var ops []usecase.Op
	for range n {
		ops = append(ops, insertOp(big, nil))
	}
	_, err := pe.uc.Commit(context.Background(), pe.editor, pe.ws, pe.paper, usecase.CommitInput{Ops: ops})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	// Rewriting the same block counts its last size only.
	r := pe.commit(t, pe.editor, insertOp(big, nil))
	id := r.Commit.Changes[0].BlockID
	var updates []usecase.Op
	for i := range n {
		content := strings.Repeat(string(rune('a'+i)), model.MaxContentLength)
		updates = append(updates, usecase.Op{Kind: usecase.OpUpdate, BlockID: id, Content: ptr(content)})
	}
	r = pe.commit(t, pe.editor, updates...)
	gt.A(t, r.Commit.Changes).Length(n)
}
