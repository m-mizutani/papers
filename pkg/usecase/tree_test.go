package usecase_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
)

func paperBlocks(t *testing.T, e *env, p *model.Principal, ws model.WorkspaceID, parent model.PaperID) []model.PaperID {
	t.Helper()
	var out []model.PaperID
	for _, b := range e.blocks(t, p, ws, parent) {
		if b.Type == model.BlockPaper {
			out = append(out, b.Props.PaperID)
		}
	}
	return out
}

func TestCreateChildAndPath(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	home := e.home(t, wsA)
	runbooks := e.child(t, alice, wsA, "", "Runbooks")
	db := e.child(t, alice, wsA, "Runbooks", "DB")

	gt.Equal(t, runbooks.ParentID, home)
	gt.Equal(t, db.ParentID, runbooks.ID)
	gt.Equal(t, paperBlocks(t, e, alice, wsA, home), []model.PaperID{runbooks.ID})
	gt.Equal(t, paperBlocks(t, e, alice, wsA, runbooks.ID), []model.PaperID{db.ID})

	got := gt.R1(e.uc.ResolvePaper(ctx, alice, wsA, "Runbooks/DB")).NoError(t)
	gt.Equal(t, got.ID, db.ID)
	view := gt.R1(e.uc.GetPaper(ctx, alice, wsA, db.ID)).NoError(t)
	gt.A(t, view.Path).Length(2)
	gt.Equal(t, view.Path[0].ID, home)
	gt.Equal(t, view.Path[1].ID, runbooks.ID)

	// two papers with the same title: the path is ambiguous
	e.child(t, alice, wsA, string(runbooks.ID), "DB")
	_, err := e.uc.ResolvePaper(ctx, alice, wsA, "Runbooks/DB")
	var amb *usecase.AmbiguousError
	gt.True(t, errors.As(err, &amb))
	gt.A(t, amb.Candidates).Length(2)

	// the body of the parent lists its children with one batch read
	for range 20 {
		e.child(t, alice, wsA, string(runbooks.ID), "x")
	}
	e.count.reset()
	page := gt.R1(e.uc.ListBlocks(ctx, alice, wsA, runbooks.ID, "", 100)).NoError(t)
	gt.A(t, page.Blocks).Length(22)
	gt.V(t, len(page.Papers)).Equal(22)
	gt.V(t, e.count.papers).Equal(2) // the paper itself, then all children at once
	gt.V(t, e.count.blocks).Equal(1)

	// a private paper has no children
	memo := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Memo", Visibility: model.VisibilityPrivate})).NoError(t)
	_, err = e.uc.Apply(ctx, alice, wsA, memo.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpInsert, Type: ptr(model.BlockPaper), Title: ptr("sub")}}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestDeletePaperBlock(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	parent := e.child(t, alice, wsA, "", "Parent")
	mine := e.child(t, alice, wsA, string(parent.ID), "Mine")
	sub := e.child(t, alice, wsA, string(mine.ID), "Sub")
	theirs := e.child(t, bob, wsA, string(parent.ID), "Theirs")

	blockOf := func(id model.PaperID) *model.Block {
		for _, b := range e.blocks(t, alice, wsA, parent.ID) {
			if b.Props.PaperID == id {
				return b
			}
		}
		return nil
	}
	// alice deletes her child: it goes to the trash with everything under it
	b := blockOf(mine.ID)
	e.apply(t, alice, wsA, parent.ID, usecase.Op{Kind: usecase.OpDelete, BlockID: b.ID, BaseRev: b.Rev})
	gt.True(t, e.paper(t, wsA, mine.ID).IsTrashed())
	gt.True(t, e.paper(t, wsA, sub.ID).IsTrashed())
	gt.Equal(t, e.paper(t, wsA, sub.ID).DeletedWith, mine.ID)
	gt.Nil(t, blockOf(mine.ID))
	// trashed papers are hidden from others
	_, err := e.uc.GetPaper(ctx, bob, wsA, mine.ID)
	gt.True(t, errors.Is(err, model.ErrNotFound))

	// alice deletes bob's child: it is archived and the block stays
	b = blockOf(theirs.ID)
	e.apply(t, alice, wsA, parent.ID, usecase.Op{Kind: usecase.OpDelete, BlockID: b.ID, BaseRev: b.Rev})
	gt.NotNil(t, blockOf(theirs.ID))
	gt.True(t, e.paper(t, wsA, theirs.ID).IsArchived())

	// restoring from the trash puts it back in its parent
	gt.R1(e.uc.RestoreFromTrash(ctx, alice, wsA, mine.ID)).NoError(t)
	gt.False(t, e.paper(t, wsA, sub.ID).IsTrashed())
	gt.NotNil(t, blockOf(mine.ID))
}

func TestTrashRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	home := e.home(t, wsA)
	a := e.child(t, alice, wsA, "", "A")
	b := e.child(t, alice, wsA, string(a.ID), "B")
	c := e.child(t, alice, wsA, string(b.ID), "C")

	// only the creator, and not the home
	_, err := e.uc.Trash(ctx, bob, wsA, a.ID)
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))
	_, err = e.uc.Trash(ctx, alice, wsA, home)
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	_, err = e.uc.Archive(ctx, alice, wsA, home, false)
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	// a subtree with someone else's paper is archived instead of deleted
	x := e.child(t, bob, wsA, string(b.ID), "X")
	gt.R1(e.uc.Trash(ctx, alice, wsA, a.ID)).NoError(t)
	gt.False(t, e.paper(t, wsA, a.ID).IsTrashed())
	gt.True(t, e.paper(t, wsA, a.ID).IsArchived())
	gt.True(t, e.paper(t, wsA, x.ID).IsArchived())
	gt.R1(e.uc.Unarchive(ctx, alice, wsA, a.ID, true)).NoError(t)
	gt.False(t, e.paper(t, wsA, x.ID).IsArchived())
	gt.R1(e.uc.Trash(ctx, bob, wsA, x.ID)).NoError(t)

	// trash B (with C), then A; restore B: it goes to the nearest live ancestor (home)
	gt.R1(e.uc.Trash(ctx, alice, wsA, b.ID)).NoError(t)
	gt.R1(e.uc.Trash(ctx, alice, wsA, a.ID)).NoError(t)
	trash := gt.R1(e.uc.ListTrash(ctx, alice, wsA)).NoError(t)
	gt.A(t, trash).Length(2)
	gt.R1(e.uc.RestoreFromTrash(ctx, alice, wsA, b.ID)).NoError(t)
	gb := e.paper(t, wsA, b.ID)
	gt.Equal(t, gb.ParentID, home)
	gt.False(t, e.paper(t, wsA, c.ID).IsTrashed())
	gt.True(t, e.paper(t, wsA, a.ID).IsTrashed())
	gt.A(t, paperBlocks(t, e, alice, wsA, home)).Length(1)

	// writes to a trashed paper are refused; purge after the retention
	_, err = e.uc.Apply(ctx, alice, wsA, a.ID, usecase.BatchInput{Ops: []usecase.Op{para("x")}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	gt.V(t, gt.R1(e.uc.PurgeTrash(ctx)).NoError(t)).Equal(0)
	e.clock.Advance(31 * 24 * time.Hour)
	gt.V(t, gt.R1(e.uc.PurgeTrash(ctx)).NoError(t)).Equal(2) // A and X
	_, err = e.repo.GetPaper(ctx, wsA, a.ID)
	gt.True(t, errors.Is(err, model.ErrNotFound))
}

func TestArchiveHidesFromListsAndSearch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	a := e.child(t, alice, wsA, "", "Quarterly plan")
	e.child(t, alice, wsA, string(a.ID), "Quarterly detail")
	gt.R1(e.uc.Archive(ctx, bob, wsA, a.ID, true)).NoError(t)

	hits := gt.R1(e.uc.Search(ctx, alice, wsA, usecase.SearchInput{Query: "quarterly"})).NoError(t)
	gt.A(t, hits).Length(0)
	hits = gt.R1(e.uc.Search(ctx, alice, wsA, usecase.SearchInput{Query: "quarterly", IncludeArchived: true})).NoError(t)
	gt.A(t, hits).Length(2)
	kids := gt.R1(e.uc.ListChildren(ctx, alice, wsA, e.home(t, wsA), false)).NoError(t)
	gt.A(t, kids).Length(0)
	// still opens by URL
	gt.R1(e.uc.GetPaper(ctx, alice, wsA, a.ID)).NoError(t)
	gt.R1(e.uc.Unarchive(ctx, bob, wsA, a.ID, false)).NoError(t)
	gt.False(t, e.paper(t, wsA, a.ID).IsArchived())
}

func TestMovePaper(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	home := e.home(t, wsA)
	a := e.child(t, alice, wsA, "", "A")
	b := e.child(t, alice, wsA, "", "B")
	c := e.child(t, alice, wsA, string(a.ID), "C")
	d := e.child(t, alice, wsA, string(c.ID), "D")

	// move C (with D) under B, by anyone
	e.apply(t, bob, wsA, c.ID, usecase.Op{Kind: usecase.OpMovePaper, Parent: "B"})
	gt.Equal(t, e.paper(t, wsA, c.ID).ParentID, b.ID)
	gt.Equal(t, e.paper(t, wsA, d.ID).ParentID, c.ID) // untouched
	gt.A(t, paperBlocks(t, e, alice, wsA, a.ID)).Length(0)
	gt.Equal(t, paperBlocks(t, e, alice, wsA, b.ID), []model.PaperID{c.ID})

	// cycles and the home are refused
	_, err := e.uc.MovePaper(ctx, alice, wsA, b.ID, usecase.MoveInput{Parent: string(d.ID)})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	_, err = e.uc.MovePaper(ctx, alice, wsA, home, usecase.MoveInput{Parent: string(a.ID)})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	// reorder within the same parent
	e.apply(t, alice, wsA, b.ID, usecase.Op{Kind: usecase.OpMovePaper, Parent: string(home), After: ptr("")})
	gt.Equal(t, paperBlocks(t, e, alice, wsA, home), []model.PaperID{b.ID, a.ID})
}

func TestVisibility(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	home := e.home(t, wsA)
	a := e.child(t, alice, wsA, "", "A")
	e.child(t, alice, wsA, string(a.ID), "child")
	leaf := e.child(t, alice, wsA, "", "Leaf")

	_, err := e.uc.SetVisibility(ctx, bob, wsA, leaf.ID, usecase.VisibilityInput{Visibility: model.VisibilityPrivate})
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))
	_, err = e.uc.SetVisibility(ctx, alice, wsA, a.ID, usecase.VisibilityInput{Visibility: model.VisibilityPrivate})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument)) // has children

	gt.R1(e.uc.SetVisibility(ctx, alice, wsA, leaf.ID, usecase.VisibilityInput{Visibility: model.VisibilityPrivate})).NoError(t)
	got := e.paper(t, wsA, leaf.ID)
	gt.Equal(t, got.ParentID, model.PaperID(""))
	gt.Equal(t, paperBlocks(t, e, alice, wsA, home), []model.PaperID{a.ID})
	_, err = e.uc.GetPaper(ctx, bob, wsA, leaf.ID)
	gt.True(t, errors.Is(err, model.ErrNotFound))
	sb := gt.R1(e.uc.Sidebar(ctx, alice, wsA)).NoError(t)
	gt.A(t, sb.Private).Length(1)

	gt.R1(e.uc.SetVisibility(ctx, alice, wsA, leaf.ID, usecase.VisibilityInput{Visibility: model.VisibilityPublic, Parent: "A"})).NoError(t)
	gt.Equal(t, e.paper(t, wsA, leaf.ID).ParentID, a.ID)
	gt.R1(e.uc.GetPaper(ctx, bob, wsA, leaf.ID)).NoError(t)
}

func TestMoveAcrossWorkspaces(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	a := e.child(t, alice, wsA, "", "A")
	sub := e.child(t, alice, wsA, string(a.ID), "Sub")
	att := gt.R1(e.uc.Upload(ctx, alice, wsA, a.ID, usecase.UploadInput{Name: "x.png", ContentType: "image/png", Body: strings.NewReader("png")})).NoError(t)
	e.apply(t, alice, wsA, a.ID,
		para("saved"),
		usecase.Op{Kind: usecase.OpInsert, Type: ptr(model.BlockImage), Props: &model.BlockProps{AttachmentID: att.ID}},
	)
	gt.R1(e.uc.SaveVersion(ctx, alice, wsA, a.ID, "first")).NoError(t)
	e.apply(t, alice, wsA, a.ID, para("unsaved"))

	e.apply(t, alice, wsA, a.ID, usecase.Op{Kind: usecase.OpMovePaper, Workspace: wsB})
	fwd := gt.R1(e.repo.GetForward(ctx, wsA, a.ID)).NoError(t)
	newID := fwd.ToPaperID

	// originals are in the trash and forward
	gt.True(t, e.paper(t, wsA, a.ID).IsTrashed())
	gt.True(t, e.paper(t, wsA, sub.ID).IsTrashed())
	// opening the original leads to the copy, also while it is in the trash
	_, err := e.uc.GetPaper(ctx, alice, wsA, a.ID)
	f, moved := usecase.IsMoved(err)
	gt.True(t, moved)
	gt.Equal(t, f.ToPaperID, newID)
	bob := e.user(t, "bob")
	_, err = e.uc.GetPaper(ctx, bob, wsA, a.ID)
	gt.True(t, errors.Is(err, model.ErrNotFound))

	// the copy has the current state and the latest version as v1
	cp := e.paper(t, wsB, newID)
	gt.Equal(t, cp.VersionNumber, 1)
	gt.Equal(t, cp.ParentID, e.home(t, wsB))
	blocks := e.blocks(t, alice, wsB, newID)
	var contents []string
	var image *model.Block
	for _, b := range blocks {
		contents = append(contents, b.Content)
		if b.Type == model.BlockImage {
			image = b
		}
	}
	gt.True(t, slices.Contains(contents, "unsaved"))
	gt.NotEqual(t, image.Props.AttachmentID, att.ID)
	gt.R1(e.uc.OpenAttachment(ctx, alice, wsB, newID, image.Props.AttachmentID)).NoError(t)
	v1 := gt.R1(e.uc.GetVersion(ctx, alice, wsB, newID, 1)).NoError(t)
	gt.Equal(t, v1.Version.Note, "first")
	st := gt.R1(e.uc.Status(ctx, alice, wsB, newID)).NoError(t)
	gt.True(t, st.Changed)
	kids := gt.R1(e.uc.ListChildren(ctx, alice, wsB, newID, false)).NoError(t)
	gt.A(t, kids).Length(1)
	gt.Equal(t, kids[0].Title, "Sub")

	// a forward is reported for the old ID once the trash is gone
	e.clock.Advance(31 * 24 * time.Hour)
	gt.R1(e.uc.PurgeTrash(ctx)).NoError(t)
	_, err = e.uc.GetPaper(ctx, alice, wsA, a.ID)
	f, moved = usecase.IsMoved(err)
	gt.True(t, moved)
	gt.Equal(t, f.ToPaperID, newID)
}
