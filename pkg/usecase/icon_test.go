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

func setIcon(icon string) usecase.Op {
	return usecase.Op{Kind: usecase.OpSetIcon, Icon: ptr(icon)}
}

func TestSetIconOnEveryKind(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	bob := e.user(t, "bob")
	home := e.home(t, wsA)
	doc := e.child(t, alice, wsA, "", "Doc")
	html := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Report", Parent: string(home), Kind: model.PaperKindHTML, HTML: docV1})).NoError(t)
	db := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Tasks", Parent: string(home), Kind: model.PaperKindDatabase})).NoError(t)
	item := e.apply(t, alice, wsA, db.ID, usecase.Op{Kind: usecase.OpInsertItem, Title: ptr("One"), Ref: "i1"}).Refs["i1"]

	for _, id := range []model.PaperID{doc.ID, html.ID, db.ID, model.PaperID(item), home} {
		e.clock.Advance(time.Second)
		e.apply(t, bob, wsA, id, setIcon(" 🚀 "))
		got := e.paper(t, wsA, id)
		gt.Equal(t, got.Icon, "🚀")
		gt.Equal(t, got.UpdatedBy.UserID, bob.User.ID)
		gt.Equal(t, got.UpdatedAt, e.clock.Now())
	}

	// the same icon again does not write the paper
	e.clock.Advance(time.Hour)
	before := e.paper(t, wsA, doc.ID).UpdatedAt
	e.apply(t, alice, wsA, doc.ID, setIcon("🚀"))
	gt.Equal(t, e.paper(t, wsA, doc.ID).UpdatedAt, before)

	// removing it, together with a title change
	e.apply(t, alice, wsA, doc.ID, setIcon(""), usecase.Op{Kind: usecase.OpSetTitle, Title: ptr("Renamed")})
	got := e.paper(t, wsA, doc.ID)
	gt.Equal(t, got.Icon, "")
	gt.Equal(t, got.Title, "Renamed")
	e.apply(t, alice, wsA, db.ID, setIcon(""))
	gt.Equal(t, e.paper(t, wsA, db.ID).Icon, "")
}

func TestSetIconRefused(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	bob := e.user(t, "bob")
	p := e.child(t, alice, wsA, "", "Doc")

	for _, bad := range []string{"🚀🔥", "ab", "\u0007"} {
		_, err := e.uc.Apply(ctx, alice, wsA, p.ID, usecase.BatchInput{Ops: []usecase.Op{setIcon(bad)}})
		gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	}
	_, err := e.uc.Apply(ctx, alice, wsA, p.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpSetIcon}}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	readOnly := &model.Principal{User: alice.User, Token: &model.APIToken{ID: model.NewTokenID(), UserID: alice.User.ID, Scope: model.ScopeRead}}
	_, err = e.uc.Apply(ctx, readOnly, wsA, p.ID, usecase.BatchInput{Ops: []usecase.Op{setIcon("🚀")}})
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))

	private := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Memo", Visibility: model.VisibilityPrivate})).NoError(t)
	_, err = e.uc.Apply(ctx, bob, wsA, private.ID, usecase.BatchInput{Ops: []usecase.Op{setIcon("🚀")}})
	gt.True(t, errors.Is(err, model.ErrNotFound))
	gt.Equal(t, e.paper(t, wsA, p.ID).Icon, "")
	gt.Equal(t, e.paper(t, wsA, private.ID).Icon, "")

	_, err = e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "X", Icon: "📚📚"})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestCreateWithIcon(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	home := e.home(t, wsA)
	unfiled := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "U", Icon: "📚"})).NoError(t)
	gt.Equal(t, unfiled.Icon, "📚")
	filed := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "F", Icon: "🧪", Parent: string(home)})).NoError(t)
	gt.Equal(t, e.paper(t, wsA, filed.ID).Icon, "🧪")
	db := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "D", Parent: string(home), Kind: model.PaperKindDatabase})).NoError(t)
	item := e.apply(t, alice, wsA, db.ID, usecase.Op{Kind: usecase.OpInsertItem, Title: ptr("I"), Icon: ptr("🐛"), Ref: "i"}).Refs["i"]
	gt.Equal(t, e.paper(t, wsA, model.PaperID(item)).Icon, "🐛")
}

func TestIconInReferences(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	home := e.home(t, wsA)
	parent := e.child(t, alice, wsA, "", "Runbooks")
	child := e.child(t, alice, wsA, string(parent.ID), "DB failover")
	e.apply(t, alice, wsA, parent.ID, setIcon("📚"))
	e.apply(t, alice, wsA, child.ID, setIcon("🚀"))
	e.apply(t, alice, wsA, child.ID, para("failover steps"))
	e.apply(t, alice, wsA, home, setIcon("🗺️"))

	view := gt.R1(e.uc.GetPaper(ctx, alice, wsA, child.ID)).NoError(t)
	gt.Equal(t, view.Paper.Icon, "🚀")
	gt.Equal(t, view.Path[len(view.Path)-1].Icon, "📚")

	kids := gt.R1(e.uc.ListChildren(ctx, alice, wsA, parent.ID, false)).NoError(t)
	gt.Equal(t, kids[0].Icon, "🚀")

	sb := gt.R1(e.uc.Sidebar(ctx, alice, wsA)).NoError(t)
	gt.Equal(t, sb.Home.Icon, "🗺️")
	gt.Equal(t, sb.Recent[0].Icon, "🚀")

	page := gt.R1(e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{Place: usecase.PlaceAll, Creator: usecase.CreatorMe})).NoError(t)
	found := false
	for _, r := range page.Papers {
		if r.ID == child.ID {
			found = true
			gt.Equal(t, r.Icon, "🚀")
			gt.Equal(t, r.Parent.Icon, "📚")
			gt.Equal(t, r.Parent.Kind, model.PaperKindBlocks)
		}
	}
	gt.True(t, found)

	hits := gt.R1(e.uc.Search(ctx, alice, wsA, usecase.SearchInput{Query: "failover"})).NoError(t)
	gt.A(t, hits).Longer(0)
	gt.Equal(t, hits[0].Paper.Icon, "🚀")

	gt.R1(e.uc.Trash(ctx, alice, wsA, child.ID)).NoError(t)
	trash := gt.R1(e.uc.ListTrash(ctx, alice, wsA)).NoError(t)
	gt.Equal(t, trash[0].Icon, "🚀")
}

func TestIconOnFeed(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	alice := e.user(t, "alice")
	p := e.child(t, alice, wsA, "", "Doc")
	ch := gt.R1(e.uc.Watch(ctx, alice, wsA, p.ID)).NoError(t)
	e.apply(t, alice, wsA, p.ID, setIcon("🚀"))
	ev := <-ch
	gt.Equal(t, ev.Type, model.EventPaperUpdated)
	gt.Equal(t, ev.Paper.Icon, "🚀")

	// an item's icon also reaches its open database
	db := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Tasks", Parent: string(e.home(t, wsA)), Kind: model.PaperKindDatabase})).NoError(t)
	item := model.PaperID(e.apply(t, alice, wsA, db.ID, usecase.Op{Kind: usecase.OpInsertItem, Title: ptr("One"), Ref: "i"}).Refs["i"])
	dch := gt.R1(e.uc.Watch(ctx, alice, wsA, db.ID)).NoError(t)
	e.apply(t, alice, wsA, item, setIcon("🐛"))
	for ev := range dch {
		if ev.Type == model.EventItemPut && ev.Paper.ID == item {
			gt.Equal(t, ev.Paper.Icon, "🐛")
			return
		}
	}
	t.Fatal("no item_put for the item")
}

func TestIconIsNotVersioned(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	p := e.child(t, alice, wsA, "", "Doc")
	e.apply(t, alice, wsA, p.ID, para("x"))
	e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpSaveVersion})
	e.apply(t, alice, wsA, p.ID, setIcon("🚀"))
	st := gt.R1(e.uc.Status(ctx, alice, wsA, p.ID)).NoError(t)
	gt.False(t, st.Changed)
	gt.R1(e.uc.RestoreVersion(ctx, alice, wsA, p.ID, 1)).NoError(t)
	gt.Equal(t, e.paper(t, wsA, p.ID).Icon, "🚀")
}
