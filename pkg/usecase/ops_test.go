package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
)

func TestAccess(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	p := e.child(t, alice, wsA, "", "Plan")

	// public: everyone the workspace allows
	gt.R1(e.uc.GetPaper(ctx, bob, wsA, p.ID)).NoError(t)

	// the workspace rule denies: not found
	e.deny(wsA, bob)
	_, err := e.uc.GetPaper(ctx, bob, wsA, p.ID)
	gt.True(t, errors.Is(err, model.ErrNotFound))
	delete(e.rules.deny[wsA], bob.User.ID)

	// private: only the creator
	memo := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Memo", Visibility: model.VisibilityPrivate})).NoError(t)
	_, err = e.uc.GetPaper(ctx, bob, wsA, memo.ID)
	gt.True(t, errors.Is(err, model.ErrNotFound))
	gt.R1(e.uc.GetPaper(ctx, alice, wsA, memo.ID)).NoError(t)

	// an agent acting for alice sees her private paper
	agent := &model.Principal{User: alice.User, Token: &model.APIToken{ID: model.NewTokenID(), Kind: model.TokenAgent, AgentName: "Claude", Scope: model.ScopeWrite}}
	gt.R1(e.uc.GetPaper(ctx, agent, wsA, memo.ID)).NoError(t)
	res := e.apply(t, agent, wsA, memo.ID, para("by agent"))
	gt.Equal(t, res.Blocks[0].CreatedBy.Kind, model.ActorAgent)
	gt.Equal(t, res.Blocks[0].CreatedBy.UserID, alice.User.ID)

	// a read-only token can read but not write: 403
	ro := &model.Principal{User: bob.User, Token: &model.APIToken{ID: model.NewTokenID(), Kind: model.TokenPersonal, Scope: model.ScopeRead}}
	gt.R1(e.uc.GetPaper(ctx, ro, wsA, p.ID)).NoError(t)
	_, err = e.uc.Apply(ctx, ro, wsA, p.ID, usecase.BatchInput{Ops: []usecase.Op{para("x")}})
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))

	// a token limited to another workspace does not see this one
	other := &model.Principal{User: bob.User, Token: &model.APIToken{ID: model.NewTokenID(), Kind: model.TokenPersonal, Scope: model.ScopeWrite, Workspaces: []model.WorkspaceID{wsB}}}
	_, err = e.uc.GetPaper(ctx, other, wsA, p.ID)
	gt.True(t, errors.Is(err, model.ErrNotFound))

	// malformed and unknown IDs look the same
	_, err = e.uc.GetPaper(ctx, bob, wsA, "not-an-id")
	gt.True(t, errors.Is(err, model.ErrNotFound))
	_, err = e.uc.GetPaper(ctx, bob, wsA, model.NewPaperID())
	gt.True(t, errors.Is(err, model.ErrNotFound))
	_, err = e.uc.GetPaper(ctx, bob, "nowhere", p.ID)
	gt.True(t, errors.Is(err, model.ErrNotFound))

	// workspace list honours the rule
	e.deny(wsB, bob)
	list, _ := gt.R2(e.uc.ListWorkspaces(ctx, bob)).NoError(t)
	gt.A(t, list).Length(1)
	gt.Equal(t, list[0].ID, wsA)
	gt.Equal(t, list[0].HomePaperID, e.home(t, wsA))
}

func TestRefsAndServerIDs(t *testing.T) {
	e := newEnv(t)
	alice := e.user(t, "alice")
	p := e.child(t, alice, wsA, "", "Doc")
	res := e.apply(t, alice, wsA, p.ID,
		usecase.Op{Kind: usecase.OpInsert, Ref: "n1", Type: ptr(model.BlockHeading), Content: ptr("Title")},
		usecase.Op{Kind: usecase.OpInsert, Ref: "n2", Type: ptr(model.BlockParagraph), Content: ptr("second"), After: ptr("ref:n1")},
		usecase.Op{Kind: usecase.OpInsert, Type: ptr(model.BlockParagraph), Content: ptr("first-after-title"), After: ptr("ref:n1")},
	)
	gt.V(t, len(res.Refs)).Equal(2)
	gt.True(t, model.ValidID(res.Refs["n1"]))
	blocks := e.blocks(t, alice, wsA, p.ID)
	gt.A(t, blocks).Length(3)
	gt.Equal(t, string(blocks[0].ID), res.Refs["n1"])
	gt.Equal(t, blocks[0].Props.Level, 1)
	gt.Equal(t, blocks[1].Content, "first-after-title")
	gt.Equal(t, string(blocks[2].ID), res.Refs["n2"])

	// unknown and duplicated refs are rejected
	_, err := e.uc.Apply(context.Background(), alice, wsA, p.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpInsert, Type: ptr(model.BlockParagraph), After: ptr("ref:nope")}}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	_, err = e.uc.Apply(context.Background(), alice, wsA, p.ID, usecase.BatchInput{Ops: []usecase.Op{
		{Kind: usecase.OpInsert, Ref: "x", Type: ptr(model.BlockParagraph)}, {Kind: usecase.OpInsert, Ref: "x", Type: ptr(model.BlockParagraph)},
	}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestRevisionRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	p := e.child(t, alice, wsA, "", "Doc")
	b := e.apply(t, alice, wsA, p.ID, para("v1")).Blocks[0]
	gt.Equal(t, b.Rev, int64(1))

	update := func(who *model.Principal, base int64, content string, force bool) (*usecase.BatchResult, error) {
		return e.uc.Apply(ctx, who, wsA, p.ID, usecase.BatchInput{Force: force, Ops: []usecase.Op{{Kind: usecase.OpUpdate, BlockID: b.ID, BaseRev: base, Content: ptr(content)}}})
	}
	// missing or future base: 400
	_, err := update(alice, 0, "x", false)
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	_, err = update(alice, 5, "x", false)
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	// equal: applied, rev +1
	res := gt.R1(update(alice, 1, "v2", false)).NoError(t)
	gt.Equal(t, res.Blocks[0].Rev, int64(2))

	// same content: nothing happens
	res = gt.R1(update(alice, 2, "v2", false)).NoError(t)
	gt.A(t, res.Blocks).Length(0)

	// a move does not change the revision and never conflicts
	other := e.apply(t, alice, wsA, p.ID, para("other")).Blocks[0]
	res = e.apply(t, bob, wsA, p.ID, usecase.Op{Kind: usecase.OpMove, BlockID: b.ID, After: ptr(string(other.ID))})
	gt.Equal(t, res.Blocks[0].Rev, int64(2))

	// older base after someone else changed the content: conflict with cur
	_, err = update(bob, 1, "bob's", false)
	var ce *usecase.ConflictError
	gt.True(t, errors.As(err, &ce))
	gt.True(t, errors.Is(err, model.ErrConflict))
	gt.A(t, ce.Conflicts).Length(1)
	gt.Equal(t, ce.Conflicts[0].Current.Content, "v2")
	// nothing was written
	gt.Equal(t, e.blocks(t, alice, wsA, p.ID)[1].Content, "v2")

	// force overwrites
	res = gt.R1(update(bob, 1, "bob's", true)).NoError(t)
	gt.Equal(t, res.Blocks[0].Content, "bob's")
	gt.Equal(t, res.Blocks[0].Rev, int64(3))

	// touching a block twice in one batch checks against the pre-batch rev
	res = e.apply(t, alice, wsA, p.ID,
		usecase.Op{Kind: usecase.OpUpdate, BlockID: b.ID, BaseRev: 3, Content: ptr("one")},
		usecase.Op{Kind: usecase.OpUpdate, BlockID: b.ID, BaseRev: 3, Content: ptr("two")},
	)
	gt.Equal(t, res.Blocks[0].Content, "two")
	gt.Equal(t, res.Blocks[0].Rev, int64(4))

	// deleted by someone else: update conflicts even with force; delete and move do nothing
	e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpDelete, BlockID: other.ID, BaseRev: 1})
	_, err = e.uc.Apply(ctx, bob, wsA, p.ID, usecase.BatchInput{Force: true, Ops: []usecase.Op{{Kind: usecase.OpUpdate, BlockID: other.ID, BaseRev: 1, Content: ptr("x")}}})
	gt.True(t, errors.As(err, &ce))
	gt.Nil(t, ce.Conflicts[0].Current)
	res = e.apply(t, bob, wsA, p.ID, usecase.Op{Kind: usecase.OpDelete, BlockID: other.ID, BaseRev: 1}, usecase.Op{Kind: usecase.OpMove, BlockID: other.ID})
	gt.A(t, res.Deleted).Length(0)

	// blocks created in the batch are not checked
	res = e.apply(t, alice, wsA, p.ID,
		usecase.Op{Kind: usecase.OpInsert, Ref: "n", Type: ptr(model.BlockParagraph)},
	)
	nid := model.BlockID(res.Refs["n"])
	e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpDelete, BlockID: nid, BaseRev: 1})
}

func TestMarkupIsNormalised(t *testing.T) {
	e := newEnv(t)
	alice := e.user(t, "alice")
	p := e.child(t, alice, wsA, "", "Doc")
	res := e.apply(t, alice, wsA, p.ID, para("__bold__ and [red]{.red}"))
	gt.Equal(t, res.Blocks[0].Content, "**bold** and [red]{.red}")

	_, err := e.uc.Apply(context.Background(), alice, wsA, p.ID, usecase.BatchInput{Ops: []usecase.Op{para("[x]{.neon}")}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	// code is kept as it is
	res = e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpInsert, Type: ptr(model.BlockCode), Content: ptr("__x__ [y]{.neon}")})
	gt.Equal(t, res.Blocks[0].Content, "__x__ [y]{.neon}")

	hits := gt.R1(e.uc.Search(context.Background(), alice, wsA, usecase.SearchInput{Query: "bold and red"})).NoError(t)
	gt.A(t, hits).Length(1)
	gt.Equal(t, hits[0].Text, "bold and red")
}

func TestTables(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	p := e.child(t, alice, wsA, "", "Doc")
	res := e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpInsert, Type: ptr(model.BlockTable), Props: &model.BlockProps{HeaderRow: true}, Cells: [][]string{{"k", "v"}, {"a", "1"}}})
	table := res.Blocks[0]
	gt.A(t, table.Props.Rows).Length(2)
	gt.A(t, table.Props.Cols).Length(2)
	gt.A(t, res.Blocks).Length(5)

	// insert a row between, with values
	res = e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpInsertRow, BlockID: table.ID, Ref: "r", After: ptr(string(table.Props.Rows[0].ID)), Values: []string{"b", "2"}})
	newRow := model.LineID(res.Refs["r"])
	gt.Equal(t, res.Blocks[0].Rev, int64(2))
	md := gt.R1(e.uc.Export(ctx, alice, wsA, p.ID)).NoError(t)
	gt.S(t, md).Contains("| k | v |\n| --- | --- |\n| b | 2 |\n| a | 1 |")

	// a column at the start; then move it to the end
	res = e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpInsertCol, BlockID: table.ID, Ref: "c", After: ptr(""), Values: []string{"#", "x", "y"}})
	col := model.LineID(res.Refs["c"])
	md = gt.R1(e.uc.Export(ctx, alice, wsA, p.ID)).NoError(t)
	gt.S(t, md).Contains("| # | k | v |")
	e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpMoveCol, BlockID: table.ID, LineID: col})
	md = gt.R1(e.uc.Export(ctx, alice, wsA, p.ID)).NoError(t)
	gt.S(t, md).Contains("| k | v | # |")

	// bob is editing a cell of the new row; alice deletes the row and learns it
	var cell model.BlockID
	for _, b := range e.blocks(t, alice, wsA, p.ID) {
		if b.Props.RowID == newRow && b.Content == "b" {
			cell = b.ID
		}
	}
	gt.NoError(t, e.uc.ReportPresence(ctx, bob, wsA, p.ID, cell, false))
	res = e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpDeleteRow, BlockID: table.ID, LineID: newRow})
	gt.A(t, res.Editing).Length(1)
	gt.Equal(t, res.Editing[0].Actor.UserID, bob.User.ID)
	gt.A(t, res.Deleted).Length(3)

	// cells cannot be deleted alone
	var live *model.Block
	for _, b := range e.blocks(t, alice, wsA, p.ID) {
		if b.Type == model.BlockTableCell {
			live = b
		}
	}
	_, err := e.uc.Apply(ctx, alice, wsA, p.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpDelete, BlockID: live.ID, BaseRev: live.Rev}}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	// deleting the table removes its cells
	cur := e.blocks(t, alice, wsA, p.ID)[0]
	res = e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpDelete, BlockID: cur.ID, BaseRev: cur.Rev})
	gt.A(t, e.blocks(t, alice, wsA, p.ID)).Length(0)
}

func TestBatchLimits(t *testing.T) {
	e := newEnv(t)
	alice := e.user(t, "alice")
	p := e.child(t, alice, wsA, "", "Doc")
	ops := make([]usecase.Op, usecase.MaxBlocksPerBatch+1)
	for i := range ops {
		ops[i] = para("x")
	}
	_, err := e.uc.Apply(context.Background(), alice, wsA, p.ID, usecase.BatchInput{Ops: ops})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	big := make([]byte, 200*1024)
	for i := range big {
		big[i] = 'a'
	}
	ops = make([]usecase.Op, 16)
	for i := range ops {
		ops[i] = para(string(big))
	}
	_, err = e.uc.Apply(context.Background(), alice, wsA, p.ID, usecase.BatchInput{Ops: ops})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestPaperTouchInterval(t *testing.T) {
	e := newEnv(t)
	alice := e.user(t, "alice")
	p := e.child(t, alice, wsA, "", "Doc")
	before := e.paper(t, wsA, p.ID).UpdatedAt
	e.clock.Advance(10 * time.Second)
	e.apply(t, alice, wsA, p.ID, para("x"))
	gt.Equal(t, e.paper(t, wsA, p.ID).UpdatedAt, before) // within a minute: not rewritten
	e.clock.Advance(time.Minute)
	e.apply(t, alice, wsA, p.ID, para("y"))
	gt.Equal(t, e.paper(t, wsA, p.ID).UpdatedAt, e.clock.Now())
	// a title change always writes the paper
	e.clock.Advance(time.Second)
	e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpSetTitle, Title: ptr("New")})
	got := e.paper(t, wsA, p.ID)
	gt.Equal(t, got.Title, "New")
	gt.Equal(t, got.UpdatedAt, e.clock.Now())
}

func TestPaperLevelOpMustBeAlone(t *testing.T) {
	e := newEnv(t)
	alice := e.user(t, "alice")
	p := e.child(t, alice, wsA, "", "Doc")
	_, err := e.uc.Apply(context.Background(), alice, wsA, p.ID, usecase.BatchInput{Ops: []usecase.Op{para("x"), {Kind: usecase.OpSaveVersion}}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	res := e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpSaveVersion, Note: "first"})
	gt.Equal(t, res.Version.Number, 1)
}

func TestFeedCarriesChanges(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	alice := e.user(t, "alice")
	p := e.child(t, alice, wsA, "", "Doc")
	ch := gt.R1(e.uc.Watch(ctx, alice, wsA, p.ID)).NoError(t)
	b := e.apply(t, alice, wsA, p.ID, para("hello")).Blocks[0]
	ev := <-ch
	gt.Equal(t, ev.Type, model.EventBlockPut)
	gt.Equal(t, ev.Block.ID, b.ID)
}
