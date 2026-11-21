package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
)

func TestVersions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	agent := &model.Principal{User: alice.User, Token: &model.APIToken{ID: model.NewTokenID(), Kind: model.TokenAgent, AgentName: "Claude", Scope: model.ScopeWrite}}
	p := e.child(t, alice, wsA, "", "Doc")
	res := e.apply(t, alice, wsA, p.ID, para("one"), para("two"))
	one, two := res.Blocks[0], res.Blocks[1]

	st := gt.R1(e.uc.Status(ctx, alice, wsA, p.ID)).NoError(t)
	gt.Equal(t, *st, usecase.Status{VersionNumber: 0, Changed: true})

	v1 := gt.R1(e.uc.SaveVersion(ctx, alice, wsA, p.ID, "first")).NoError(t)
	gt.Equal(t, v1.Number, 1)
	gt.Equal(t, e.paper(t, wsA, p.ID).Status(), model.StatusCanonical)
	// nothing changed: no version
	none := gt.R1(e.uc.SaveVersion(ctx, alice, wsA, p.ID, "again")).NoError(t)
	gt.Nil(t, none)

	// change one block, delete another, add one, retitle; an agent saves
	e.apply(t, alice, wsA, p.ID,
		usecase.Op{Kind: usecase.OpUpdate, BlockID: one.ID, BaseRev: 1, Content: ptr("ONE")},
		usecase.Op{Kind: usecase.OpDelete, BlockID: two.ID, BaseRev: 1},
		para("three"),
		usecase.Op{Kind: usecase.OpSetTitle, Title: ptr("Doc v2")},
	)
	v2 := gt.R1(e.uc.SaveVersion(ctx, agent, wsA, p.ID, "")).NoError(t)
	gt.Equal(t, v2.CreatedBy.Kind, model.ActorAgent)
	gt.Equal(t, v2.Title, "Doc v2")

	list := gt.R1(e.uc.ListVersions(ctx, alice, wsA, p.ID, 0, 10)).NoError(t)
	gt.A(t, list).Length(2)
	gt.Equal(t, list[0].Number, 2)

	latest := gt.R1(e.uc.GetVersion(ctx, alice, wsA, p.ID, -1)).NoError(t)
	gt.Equal(t, latest.Version.Number, 2)
	gt.Equal(t, latest.Paper.Title, "Doc v2")
	gt.A(t, latest.Blocks).Length(2)

	d := gt.R1(e.uc.Diff(ctx, alice, wsA, p.ID, 1, 2)).NoError(t)
	kinds := map[usecase.DiffKind]int{}
	for _, en := range d.Entries {
		kinds[en.Kind]++
	}
	gt.Equal(t, kinds, map[usecase.DiffKind]int{usecase.DiffChanged: 1, usecase.DiffRemoved: 1, usecase.DiffAdded: 1})
	gt.Equal(t, d.TitleBefore, "Doc")

	// restore v1: the deleted block comes back with its ID, the new one goes
	e.apply(t, alice, wsA, p.ID, para("unsaved"))
	res = gt.R1(e.uc.RestoreVersion(ctx, alice, wsA, p.ID, 1)).NoError(t)
	blocks := e.blocks(t, alice, wsA, p.ID)
	gt.A(t, blocks).Length(2)
	gt.Equal(t, blocks[0].ID, one.ID)
	gt.Equal(t, blocks[0].Content, "one")
	gt.Equal(t, blocks[1].ID, two.ID)
	gt.Equal(t, blocks[1].Content, "two")
	gt.Equal(t, e.paper(t, wsA, p.ID).Title, "Doc")
	// the current state equals v1, so the status says changed against v2
	st = gt.R1(e.uc.Status(ctx, alice, wsA, p.ID)).NoError(t)
	gt.True(t, st.Changed)
	// saving makes v3 with v1's contents; editing the restored block keeps
	// older versions intact
	gt.R1(e.uc.SaveVersion(ctx, alice, wsA, p.ID, "back to v1")).NoError(t)
	e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpUpdate, BlockID: two.ID, BaseRev: blocks[1].Rev, Content: ptr("TWO")})
	gt.R1(e.uc.SaveVersion(ctx, alice, wsA, p.ID, "")).NoError(t)
	old := gt.R1(e.uc.GetVersion(ctx, alice, wsA, p.ID, 1)).NoError(t)
	gt.Equal(t, old.Blocks[1].Content, "two")
	v3 := gt.R1(e.uc.GetVersion(ctx, alice, wsA, p.ID, 3)).NoError(t)
	gt.Equal(t, v3.Blocks[1].Content, "two")
	v4 := gt.R1(e.uc.GetVersion(ctx, alice, wsA, p.ID, 4)).NoError(t)
	gt.Equal(t, v4.Blocks[1].Content, "TWO")

	_, err := e.uc.GetVersion(ctx, alice, wsA, p.ID, 99)
	gt.True(t, errors.Is(err, model.ErrNotFound))
	_, err = e.uc.SaveVersion(ctx, alice, wsA, p.ID, strings.Repeat("x", model.MaxVersionNoteLength+1))
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestSnapshotsOnlyForChangedBlocks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	p := e.child(t, alice, wsA, "", "Doc")
	res := e.apply(t, alice, wsA, p.ID, para("a"), para("b"))
	gt.R1(e.uc.SaveVersion(ctx, alice, wsA, p.ID, "")).NoError(t)
	e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpUpdate, BlockID: res.Blocks[0].ID, BaseRev: 1, Content: ptr("A")})
	v2 := gt.R1(e.uc.SaveVersion(ctx, alice, wsA, p.ID, "")).NoError(t)
	// b still points at its first snapshot
	gt.Equal(t, v2.Blocks[1].Rev, int64(1))
	gt.Equal(t, v2.Blocks[0].Rev, int64(2))
	snaps := gt.R1(e.repo.GetSnapshots(ctx, wsA, p.ID, []model.SnapshotRef{{BlockID: res.Blocks[1].ID, Rev: 1}, {BlockID: res.Blocks[0].ID, Rev: 1}, {BlockID: res.Blocks[0].ID, Rev: 2}})).NoError(t)
	gt.A(t, snaps).Length(3)
}

func TestRestoreVersionAndChildren(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	parent := e.child(t, alice, wsA, "", "Parent")
	stays := e.child(t, alice, wsA, string(parent.ID), "Stays")
	moves := e.child(t, alice, wsA, string(parent.ID), "Moves")
	bobs := e.child(t, bob, wsA, string(parent.ID), "Bob's")
	gt.R1(e.uc.SaveVersion(ctx, alice, wsA, parent.ID, "")).NoError(t)

	// after the version: one child moves away, one is trashed by bob, a new one is added
	other := e.child(t, alice, wsA, "", "Other")
	gt.R1(e.uc.MovePaper(ctx, alice, wsA, moves.ID, usecase.MoveInput{Parent: string(other.ID)})).NoError(t)
	gt.R1(e.uc.Trash(ctx, bob, wsA, bobs.ID)).NoError(t)
	newer := e.child(t, alice, wsA, string(parent.ID), "Newer")

	res := gt.R1(e.uc.RestoreVersion(ctx, alice, wsA, parent.ID, 1)).NoError(t)
	reasons := map[model.PaperID]string{}
	for _, n := range res.NotRestored {
		reasons[n.PaperID] = n.Reason
	}
	gt.Equal(t, reasons, map[model.PaperID]string{moves.ID: "moved", bobs.ID: "trashed"})
	gt.Equal(t, paperBlocks(t, e, alice, wsA, parent.ID), []model.PaperID{stays.ID})
	// the newer child (alice's) went to the trash
	gt.True(t, e.paper(t, wsA, newer.ID).IsTrashed())

	// bob restoring brings his trashed child back
	res = gt.R1(e.uc.RestoreVersion(ctx, bob, wsA, parent.ID, 1)).NoError(t)
	gt.False(t, e.paper(t, wsA, bobs.ID).IsTrashed())
	gt.Equal(t, paperBlocks(t, e, alice, wsA, parent.ID), []model.PaperID{stays.ID, bobs.ID})
}
