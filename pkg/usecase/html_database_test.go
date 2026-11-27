package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
)

func (e *env) database(t *testing.T, p *model.Principal, ws model.WorkspaceID, title string) *model.Paper {
	t.Helper()
	return gt.R1(e.uc.CreatePaper(context.Background(), p, ws, usecase.CreateInput{Title: title, Parent: string(e.home(t, ws)), Kind: model.PaperKindDatabase})).NoError(t)
}

func links(l ...model.DatabaseLink) *[]model.DatabaseLink { return &l }

func TestHTMLDatabaseLinks(t *testing.T) { // T3
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	db := e.database(t, alice, wsA, "Roadmap")
	other := e.database(t, alice, wsA, "Incidents")
	rw := model.DatabaseLink{ID: db.ID, Write: true}
	ro := model.DatabaseLink{ID: other.ID}

	t.Run("an unfiled HTML paper is created with its databases", func(t *testing.T) {
		p := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Board", Kind: model.PaperKindHTML, HTML: docV1, Databases: []model.DatabaseLink{rw}})).NoError(t)
		gt.Equal(t, e.page(t, wsA, p.ID).Props.Databases, []model.DatabaseLink{rw})
		view := gt.R1(e.uc.ReadHTML(ctx, alice, wsA, p.ID, "")).NoError(t)
		gt.Equal(t, view.Databases, []model.DatabaseLink{rw})
	})

	t.Run("a filed HTML paper is created with its databases", func(t *testing.T) {
		p := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Board", Parent: string(e.home(t, wsA)), Kind: model.PaperKindHTML, HTML: docV1, Databases: []model.DatabaseLink{rw, ro}})).NoError(t)
		gt.Equal(t, e.page(t, wsA, p.ID).Props.Databases, []model.DatabaseLink{rw, ro})
	})

	t.Run("set_html keeps the databases without databases and replaces them with it", func(t *testing.T) {
		p := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Board", Kind: model.PaperKindHTML, HTML: docV1, Databases: []model.DatabaseLink{rw}})).NoError(t)
		e.apply(t, alice, wsA, p.ID, setHTML(docV2, 1))
		page := e.page(t, wsA, p.ID)
		gt.Equal(t, page.Props.Databases, []model.DatabaseLink{rw})

		op := setHTML(docV2, page.Rev)
		op.Databases = links(ro)
		e.apply(t, alice, wsA, p.ID, op)
		page = e.page(t, wsA, p.ID)
		gt.Equal(t, page.Props.Databases, []model.DatabaseLink{ro})
		gt.Equal(t, page.Rev, int64(3))

		op = setHTML(docV2, page.Rev)
		op.Databases = links()
		e.apply(t, alice, wsA, p.ID, op)
		gt.Nil(t, e.page(t, wsA, p.ID).Props.Databases)
	})

	t.Run("an html block keeps props.databases", func(t *testing.T) {
		host := e.child(t, alice, wsA, "", "Weekly")
		res := e.apply(t, alice, wsA, host.ID, usecase.Op{Kind: usecase.OpInsert, Type: ptr(model.BlockHTML), Content: ptr("<p>x</p>"), Props: &model.BlockProps{Height: 120, Databases: []model.DatabaseLink{ro}}})
		b := res.Blocks[0]
		gt.Equal(t, b.Props.Databases, []model.DatabaseLink{ro})
		res = e.apply(t, alice, wsA, host.ID, usecase.Op{Kind: usecase.OpUpdate, BlockID: b.ID, BaseRev: b.Rev, Props: &model.BlockProps{Height: 120, Databases: []model.DatabaseLink{ro, rw}}})
		gt.Equal(t, res.Blocks[0].Props.Databases, []model.DatabaseLink{ro, rw})
	})
}

func TestHTMLDatabaseLinksRejected(t *testing.T) { // T4
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	db := e.database(t, alice, wsA, "Roadmap")
	elsewhere := e.database(t, alice, wsB, "Elsewhere")
	notDB := e.child(t, alice, wsA, "", "Notes")
	bad := map[string]model.DatabaseLink{
		"another workspace": {ID: elsewhere.ID},
		"not a database":    {ID: notDB.ID},
		"missing":           {ID: model.NewPaperID()},
	}
	for name, l := range bad {
		t.Run(name, func(t *testing.T) {
			before := htmlAttachments(t, e)
			_, err := e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Board", Kind: model.PaperKindHTML, HTML: docV1, Databases: []model.DatabaseLink{{ID: db.ID}, l}})
			gt.True(t, errors.Is(err, model.ErrInvalidArgument))
			gt.Equal(t, htmlAttachments(t, e), before)

			p := e.htmlPaper(t, alice, wsA, "Board", docV1)
			op := setHTML(docV2, 1)
			op.Databases = links(l)
			_, err = e.uc.Apply(ctx, alice, wsA, p.ID, usecase.BatchInput{Ops: []usecase.Op{op}})
			gt.True(t, errors.Is(err, model.ErrInvalidArgument))
			gt.Equal(t, e.page(t, wsA, p.ID).Rev, int64(1))

			host := e.child(t, alice, wsA, "", "Weekly")
			_, err = e.uc.Apply(ctx, alice, wsA, host.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpInsert, Type: ptr(model.BlockHTML), Content: ptr("<p>x</p>"), Props: &model.BlockProps{Databases: []model.DatabaseLink{l}}}}})
			gt.True(t, errors.Is(err, model.ErrInvalidArgument))
		})
	}
	t.Run("databases only on HTML papers", func(t *testing.T) {
		_, err := e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Notes", Databases: []model.DatabaseLink{{ID: db.ID}}})
		gt.True(t, errors.Is(err, model.ErrInvalidArgument))
		host := e.child(t, alice, wsA, "", "Weekly")
		_, err = e.uc.Apply(ctx, alice, wsA, host.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpInsert, Type: ptr(model.BlockParagraph), Content: ptr("x"), Databases: links(model.DatabaseLink{ID: db.ID})}}})
		gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	})
	t.Run("a database already listed may be gone when the list is kept", func(t *testing.T) {
		gone := e.database(t, alice, wsA, "Gone")
		host := e.child(t, alice, wsA, "", "Weekly")
		res := e.apply(t, alice, wsA, host.ID, usecase.Op{Kind: usecase.OpInsert, Type: ptr(model.BlockHTML), Content: ptr("<p>x</p>"), Props: &model.BlockProps{Databases: []model.DatabaseLink{{ID: gone.ID}}}})
		gt.NoError(t, e.repo.PurgePaper(ctx, wsA, gone.ID))
		b := res.Blocks[0]
		e.apply(t, alice, wsA, host.ID, usecase.Op{Kind: usecase.OpUpdate, BlockID: b.ID, BaseRev: b.Rev, Content: ptr("<p>y</p>"), Props: &model.BlockProps{Databases: []model.DatabaseLink{{ID: gone.ID}}}})
	})
}

func TestHTMLDatabaseLinksMoveAcrossWorkspaces(t *testing.T) { // T5
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	parent := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Planning", Parent: string(e.home(t, wsA))})).NoError(t)
	inside := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Roadmap", Parent: string(parent.ID), Kind: model.PaperKindDatabase})).NoError(t)
	outside := e.database(t, alice, wsA, "Incidents")
	board := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Board", Parent: string(parent.ID), Kind: model.PaperKindHTML, HTML: docV1,
		Databases: []model.DatabaseLink{{ID: outside.ID}, {ID: inside.ID, Write: true}}})).NoError(t)

	gt.R1(e.uc.MovePaper(ctx, alice, wsA, parent.ID, usecase.MoveInput{Workspace: wsB, Parent: string(e.home(t, wsB))})).NoError(t)
	boardCopy := gt.R1(e.repo.GetForward(ctx, wsA, board.ID)).NoError(t).ToPaperID
	dbCopy := gt.R1(e.repo.GetForward(ctx, wsA, inside.ID)).NoError(t).ToPaperID
	gt.Equal(t, e.page(t, wsB, boardCopy).Props.Databases, []model.DatabaseLink{{ID: dbCopy, Write: true}})
}

func TestHTMLDatabaseLinksToATrashedDatabase(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	bob := e.user(t, "bob")
	db := e.database(t, alice, wsA, "Roadmap")
	gone := e.database(t, alice, wsA, "Old roadmap")
	keep := []model.DatabaseLink{{ID: db.ID, Write: true}, {ID: gone.ID}}
	p := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Board", Kind: model.PaperKindHTML, HTML: docV1, Databases: keep})).NoError(t)
	gt.R1(e.uc.Trash(ctx, alice, wsA, gone.ID)).NoError(t)

	// sent back as they are, the links stay, the trashed database's too
	op := setHTML(docV2, e.page(t, wsA, p.ID).Rev)
	op.Databases = links(keep...)
	e.apply(t, bob, wsA, p.ID, op)
	gt.Equal(t, e.page(t, wsA, p.ID).Props.Databases, keep)

	// a new link to a trashed database is refused as a missing one, for its creator too
	for _, who := range []*model.Principal{alice, bob} {
		_, err := e.uc.CreatePaper(ctx, who, wsA, usecase.CreateInput{Title: "Board", Kind: model.PaperKindHTML, HTML: docV1, Databases: []model.DatabaseLink{{ID: gone.ID}}})
		gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	}
}
