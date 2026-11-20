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

func (pe *paperEnv) commit(t *testing.T, p *model.Principal, ops ...usecase.Op) *usecase.CommitResult {
	t.Helper()
	return gt.R1(pe.uc.Commit(context.Background(), p, pe.ws, pe.paper, usecase.CommitInput{Ops: ops, Message: "test"})).NoError(t)
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

func TestCommitUpdateAndLastWriterWins(t *testing.T) {
	pe := newPaperEnv(t)
	r := pe.commit(t, pe.editor, insertOp("v1", nil))
	id := r.Commit.Changes[0].BlockID

	// owner edits based on version 1
	r = pe.commit(t, pe.owner, usecase.Op{Kind: usecase.OpUpdate, BlockID: id, Content: ptr("owner"), BaseVersion: 1})
	gt.Equal(t, r.Blocks[0].Version, int64(2))
	gt.Equal(t, r.Commit.Changes[0].FromVersion, int64(1))
	gt.Equal(t, r.Commit.Changes[0].ToVersion, int64(2))
	gt.Equal(t, r.Commit.Changes[0].OverwroteVersion, int64(0))

	// editor also edits based on version 1: last writer wins, overwrite recorded
	r = pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpUpdate, BlockID: id, Content: ptr("editor"), BaseVersion: 1})
	gt.Equal(t, r.Commit.Changes[0].OverwroteVersion, int64(2))
	prev := gt.R1(pe.repo.GetBlockVersion(context.Background(), pe.ws, pe.paper, id, r.Commit.Changes[0].FromVersion)).NoError(t)
	gt.Equal(t, prev.Content, "owner")
	gt.Equal(t, pe.blocks(t)[0].Content, "editor")
	gt.Equal(t, pe.blocks(t)[0].Version, int64(3))
	gt.Equal(t, pe.blocks(t)[0].UpdatedBy.UserID, pe.editor.User.ID)
	gt.Equal(t, pe.blocks(t)[0].CreatedBy.UserID, pe.editor.User.ID)

	// a no-op update produces no commit
	r = pe.commit(t, pe.editor, usecase.Op{Kind: usecase.OpUpdate, BlockID: id, Content: ptr("editor")})
	gt.Nil(t, r.Commit)

	// the whole history is kept per block
	versions := gt.R1(pe.uc.ListBlockVersions(context.Background(), pe.viewer, pe.ws, pe.paper, id, 0)).NoError(t)
	gt.Equal(t, len(versions), 3)
	gt.Equal(t, versions[0].Content, "editor")
	gt.Equal(t, versions[2].Content, "v1")
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

	_, err = pe.uc.Commit(context.Background(), pe.editor, pe.ws, pe.paper, usecase.CommitInput{Ops: []usecase.Op{{Kind: usecase.OpDelete, BlockID: a}}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
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
	agent := &model.Principal{User: pe.editor.User, Token: &model.APIToken{ID: "tok", Kind: model.TokenAgent, AgentName: "Claude", Scope: model.ScopeWrite}, HolderKey: "token:tok"}
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

func TestCommitRespectsSoftLocks(t *testing.T) {
	pe := newPaperEnv(t)
	ctx := context.Background()
	r := pe.commit(t, pe.editor, insertOp("x", nil))
	id := r.Commit.Changes[0].BlockID

	gt.R1(pe.uc.AcquireLock(ctx, pe.editor, pe.ws, pe.paper, id, "tab-1")).NoError(t)

	// another holder is rejected...
	_, err := pe.uc.Commit(ctx, pe.owner, pe.ws, pe.paper, usecase.CommitInput{Ops: []usecase.Op{{Kind: usecase.OpUpdate, BlockID: id, Content: ptr("y")}}})
	gt.True(t, errors.Is(err, model.ErrBlockLocked))
	// ...unless forced
	gt.R1(pe.uc.Commit(ctx, pe.owner, pe.ws, pe.paper, usecase.CommitInput{Force: true, Ops: []usecase.Op{{Kind: usecase.OpUpdate, BlockID: id, Content: ptr("y")}}})).NoError(t)
	// the lock holder writes freely
	gt.R1(pe.uc.Commit(ctx, pe.editor, pe.ws, pe.paper, usecase.CommitInput{HolderKey: "tab-1", Ops: []usecase.Op{{Kind: usecase.OpUpdate, BlockID: id, Content: ptr("z")}}})).NoError(t)
	// same user in another tab is another holder
	_, err = pe.uc.Commit(ctx, pe.editor, pe.ws, pe.paper, usecase.CommitInput{HolderKey: "tab-2", Ops: []usecase.Op{{Kind: usecase.OpDelete, BlockID: id}}})
	gt.True(t, errors.Is(err, model.ErrBlockLocked))

	// expired locks no longer block
	pe.clock.Advance(31 * time.Second)
	gt.R1(pe.uc.Commit(ctx, pe.owner, pe.ws, pe.paper, usecase.CommitInput{Ops: []usecase.Op{{Kind: usecase.OpDelete, BlockID: id}}})).NoError(t)
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
