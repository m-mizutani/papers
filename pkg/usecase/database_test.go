package usecase_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
)

func (c *counting) GetPropertyValues(ctx context.Context, ws model.WorkspaceID, refs []model.PropertyValueRef) ([]*model.PropertyValue, error) {
	c.called("GetPropertyValues")
	return c.Repository.GetPropertyValues(ctx, ws, refs)
}

func (c *counting) QueryPropertyValues(ctx context.Context, ws model.WorkspaceID, db model.PaperID, q model.PropertyValueQuery) ([]*model.PropertyValue, string, error) {
	c.called("QueryPropertyValues")
	return c.Repository.QueryPropertyValues(ctx, ws, db, q)
}

func (c *counting) ListItems(ctx context.Context, ws model.WorkspaceID, db model.PaperID, q model.ItemQuery) ([]*model.Paper, string, error) {
	c.called("ListItems")
	return c.Repository.ListItems(ctx, ws, db, q)
}

// example is the spec's example: a database "プロジェクト" with 状態
// (select: 未着手, 進行中, レビュー中) and 期日 (date), and four items.
type example struct {
	db                        *model.Paper
	status, due               model.PropertyID
	todo, doing, review       model.ElementID
	invite, search, notify, x model.PaperID // 1csQ, 4Rt9, 9ZkL, 2b8M
}

func newExample(t *testing.T, e *env, p *model.Principal) *example {
	t.Helper()
	db := gt.R1(e.uc.CreatePaper(context.Background(), p, wsA, usecase.CreateInput{Title: "プロジェクト", Parent: string(e.home(t, wsA)), Kind: model.PaperKindDatabase})).NoError(t)
	res := e.apply(t, p, wsA, db.ID,
		usecase.Op{Kind: usecase.OpAddProperty, Name: ptr("状態"), PropertyType: model.PropertySelect, Ref: "s"},
		usecase.Op{Kind: usecase.OpAddElement, Property: "ref:s", Value: ptr("未着手"), Ref: "e1"},
		usecase.Op{Kind: usecase.OpAddElement, Property: "ref:s", Value: ptr("進行中"), Color: ptr("blue"), Ref: "e2"},
		usecase.Op{Kind: usecase.OpAddElement, Property: "ref:s", Value: ptr("レビュー中"), Color: ptr("yellow"), Ref: "e3"},
		usecase.Op{Kind: usecase.OpAddProperty, Name: ptr("期日"), PropertyType: model.PropertyDate, Ref: "d"},
		usecase.Op{Kind: usecase.OpInsertItem, Title: ptr("招待機能"), Ref: "a", Set: map[string]any{"ref:s": "ref:e2", "ref:d": "2026-12-10"}},
		usecase.Op{Kind: usecase.OpInsertItem, Title: ptr("検索の改善"), Ref: "b", Set: map[string]any{"ref:s": "ref:e1", "ref:d": "2027-01-20"}},
		usecase.Op{Kind: usecase.OpInsertItem, Title: ptr("通知の整理"), Ref: "c", Set: map[string]any{"ref:s": "ref:e2"}},
		usecase.Op{Kind: usecase.OpInsertItem, Title: ptr("エクスポートの高速化"), Ref: "x", Set: map[string]any{"ref:s": "ref:e3", "ref:d": "2026-12-01"}},
	)
	r := res.Refs
	return &example{
		db: res.Paper, status: model.PropertyID(r["s"]), due: model.PropertyID(r["d"]),
		todo: model.ElementID(r["e1"]), doing: model.ElementID(r["e2"]), review: model.ElementID(r["e3"]),
		invite: model.PaperID(r["a"]), search: model.PaperID(r["b"]), notify: model.PaperID(r["c"]), x: model.PaperID(r["x"]),
	}
}

func itemIDs(page *usecase.ItemsPage) []model.PaperID {
	var out []model.PaperID
	for _, r := range page.Items {
		out = append(out, r.Item.ID)
	}
	return out
}

func valueIn(row *usecase.ItemRow, prop model.PropertyID) any {
	for _, v := range row.Values {
		if v.PropertyID == prop {
			return v.Value
		}
	}
	return nil
}

func isInvalid(t *testing.T, err error) {
	t.Helper()
	gt.True(t, errors.Is(err, model.ErrInvalidArgument)).Describef("want invalid argument, got %v", err)
}

func TestCreateDatabase(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	home := e.home(t, wsA)

	// T1: a child database through a paper block
	res := e.apply(t, alice, wsA, home, usecase.Op{Kind: usecase.OpInsert, Type: ptr(model.BlockPaper), Title: ptr("P"), PaperKind: ptr(model.PaperKindDatabase)})
	var dbID model.PaperID
	for _, b := range res.Blocks {
		if b.Type == model.BlockPaper {
			dbID = b.Props.PaperID
		}
	}
	db := e.paper(t, wsA, dbID)
	gt.Equal(t, db.Kind, model.PaperKindDatabase)
	gt.Equal(t, db.ParentID, home)
	gt.A(t, db.Properties).Length(0)
	gt.A(t, db.Items).Length(0)
	kids := gt.R1(e.uc.ListChildren(ctx, alice, wsA, home, false)).NoError(t)
	gt.A(t, kids).Length(1)
	gt.Equal(t, kids[0].Kind, model.PaperKindDatabase)

	// T2: an unfiled database holds items
	un := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "U", Kind: model.PaperKindDatabase})).NoError(t)
	gt.Equal(t, un.ParentID, model.PaperID(""))
	r := e.apply(t, alice, wsA, un.ID, usecase.Op{Kind: usecase.OpInsertItem, Title: ptr("one")})
	gt.A(t, r.Items).Length(1)
	gt.Equal(t, e.paper(t, wsA, un.ID).Items, []model.PaperID{r.Items[0].ID})

	// T3: never private, never created as an item
	_, err := e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "P", Kind: model.PaperKindDatabase, Visibility: model.VisibilityPrivate})
	isInvalid(t, err)
	_, err = e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "P", Kind: "item"})
	isInvalid(t, err)
	_, err = e.uc.SetVisibility(ctx, alice, wsA, un.ID, usecase.VisibilityInput{Visibility: model.VisibilityPrivate})
	isInvalid(t, err)
	_, err = e.uc.SetVisibility(ctx, alice, wsA, r.Items[0].ID, usecase.VisibilityInput{Visibility: model.VisibilityPrivate})
	isInvalid(t, err)
	_, err = e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "P", Kind: model.PaperKindDatabase, Markdown: "# x"})
	isInvalid(t, err)
}

func TestPropertyDefinitions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	db := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "D", Kind: model.PaperKindDatabase})).NoError(t)

	// T8
	res := e.apply(t, alice, wsA, db.ID,
		usecase.Op{Kind: usecase.OpAddProperty, Name: ptr(" Memo "), PropertyType: model.PropertyText, Ref: "m"},
		usecase.Op{Kind: usecase.OpAddProperty, Name: ptr("Due"), PropertyType: model.PropertyDate, Ref: "d"},
		usecase.Op{Kind: usecase.OpUpdateProperty, Property: "ref:m", Name: ptr("Note")},
		usecase.Op{Kind: usecase.OpMoveProperty, Property: "ref:d", After: ptr("")},
	)
	m, d := model.PropertyID(res.Refs["m"]), model.PropertyID(res.Refs["d"])
	gt.True(t, model.ValidID(string(m)))
	got := e.paper(t, wsA, db.ID).Properties
	gt.A(t, got).Length(2)
	gt.Equal(t, got[0].ID, d)
	gt.Equal(t, got[1].Name, "Note")
	e.apply(t, alice, wsA, db.ID, usecase.Op{Kind: usecase.OpDeleteProperty, Property: string(d)})
	got = e.paper(t, wsA, db.ID).Properties
	gt.A(t, got).Length(1)
	gt.Equal(t, got[0].ID, m)

	// duplicate names and elements on a text property are refused
	_, err := e.uc.Apply(ctx, alice, wsA, db.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpAddProperty, Name: ptr("Note"), PropertyType: model.PropertyText}}})
	isInvalid(t, err)
	_, err = e.uc.Apply(ctx, alice, wsA, db.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpAddElement, Property: string(m), Value: ptr("x")}}})
	isInvalid(t, err)

	// T10: a database has no body or versions; database ops need a database
	_, err = e.uc.Apply(ctx, alice, wsA, db.ID, usecase.BatchInput{Ops: []usecase.Op{para("x")}})
	isInvalid(t, err)
	_, err = e.uc.Apply(ctx, alice, wsA, db.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpInsert, Type: ptr(model.BlockPaper), Title: ptr("child")}}})
	isInvalid(t, err)
	_, err = e.uc.SaveVersion(ctx, alice, wsA, db.ID, "")
	isInvalid(t, err)
	_, err = e.uc.RestoreVersion(ctx, alice, wsA, db.ID, 1)
	isInvalid(t, err)
	plain := e.child(t, alice, wsA, "", "plain")
	_, err = e.uc.Apply(ctx, alice, wsA, plain.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpAddProperty, Name: ptr("x"), PropertyType: model.PropertyText}}})
	isInvalid(t, err)
	_, err = e.uc.Apply(ctx, alice, wsA, plain.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpInsertItem}}})
	isInvalid(t, err)
}

func TestConcurrentDefinitionChanges(t *testing.T) {
	// T9: two writers that both started from the same definitions change
	// different elements; both changes stay (each batch reads, then
	// changes one thing).
	e := newEnv(t)
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	ex := newExample(t, e, alice)
	e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpUpdateElement, Property: string(ex.status), Element: string(ex.doing), Value: ptr("作業中")})
	e.apply(t, bob, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpUpdateElement, Property: string(ex.status), Element: string(ex.todo), Color: ptr("red")})
	prop := model.FindProperty(e.paper(t, wsA, ex.db.ID).Properties, ex.status)
	gt.Equal(t, prop.Elements[prop.ElementIndex(ex.doing)].Value, "作業中")
	gt.Equal(t, prop.Elements[prop.ElementIndex(ex.todo)].Color, "red")
}

func TestElementValueChangeRewritesNoValue(t *testing.T) {
	// T14
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	ex := newExample(t, e, alice)
	before := gt.R1(e.uc.GetPaper(ctx, alice, wsA, ex.invite)).NoError(t).Values
	res := e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpUpdateElement, Property: string(ex.status), Element: string(ex.doing), Value: ptr("作業中")})
	gt.A(t, res.Values).Length(0)
	after := gt.R1(e.uc.GetPaper(ctx, alice, wsA, ex.invite)).NoError(t)
	gt.Equal(t, after.Values[0].UpdatedAt, before[0].UpdatedAt)
	prop := model.FindProperty(after.Database.Properties, ex.status)
	gt.Equal(t, prop.Elements[prop.ElementIndex(ex.doing)].Value, "作業中")
}

func TestValuesAgainstDefinitions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	ex := newExample(t, e, alice)

	// t2 of 2.7: deleting an element leaves the stored value, shown empty
	e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpDeleteElement, Property: string(ex.status), Element: string(ex.todo)})
	view := gt.R1(e.uc.GetPaper(ctx, alice, wsA, ex.search)).NoError(t)
	gt.Nil(t, valueIn(&usecase.ItemRow{Values: view.Values}, ex.status))

	// T15 / t3: setting an element that is gone is refused, nothing written
	_, err := e.uc.Apply(ctx, alice, wsA, ex.db.ID, usecase.BatchInput{Ops: []usecase.Op{
		{Kind: usecase.OpSetValues, Item: string(ex.search), Set: map[string]any{string(ex.due): "2027-02-01"}},
		{Kind: usecase.OpSetValues, Item: string(ex.search), Set: map[string]any{string(ex.status): string(ex.todo)}},
	}})
	isInvalid(t, err)
	gt.Equal(t, valueIn(&usecase.ItemRow{Values: gt.R1(e.uc.GetPaper(ctx, alice, wsA, ex.search)).NoError(t).Values}, ex.due), any("2027-01-20"))
	_, err = e.uc.Apply(ctx, alice, wsA, ex.db.ID, usecase.BatchInput{Ops: []usecase.Op{
		{Kind: usecase.OpSetValues, Item: string(ex.search), Set: map[string]any{string(model.NewPropertyID()): "x"}},
	}})
	isInvalid(t, err)

	// the next write of the item drops the stale value from storage
	e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpSetValues, Item: string(ex.search), Set: map[string]any{string(ex.due): "2027-02-01"}})
	left := gt.R1(e.repo.GetPropertyValues(ctx, wsA, []model.PropertyValueRef{{PaperID: ex.search, PropertyID: ex.status}})).NoError(t)
	gt.A(t, left).Length(0)
}

func TestRefsInOneBatch(t *testing.T) {
	// T16
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	ex := newExample(t, e, alice)
	res := e.apply(t, alice, wsA, ex.db.ID,
		usecase.Op{Kind: usecase.OpAddElement, Property: string(ex.status), Value: ptr("保留"), Ref: "r1"},
		usecase.Op{Kind: usecase.OpSetValues, Item: string(ex.invite), Set: map[string]any{string(ex.status): "ref:r1"}},
		usecase.Op{Kind: usecase.OpInsertItem, Title: ptr("new"), Ref: "n1"},
		usecase.Op{Kind: usecase.OpMoveItem, Item: "ref:n1", After: ptr("")},
	)
	gt.True(t, model.ValidID(res.Refs["r1"]))
	gt.True(t, model.ValidID(res.Refs["n1"]))
	view := gt.R1(e.uc.GetPaper(ctx, alice, wsA, ex.invite)).NoError(t)
	gt.Equal(t, valueIn(&usecase.ItemRow{Values: view.Values}, ex.status), any(res.Refs["r1"]))
	gt.Equal(t, e.paper(t, wsA, ex.db.ID).Items[0], model.PaperID(res.Refs["n1"]))
}

func TestItemsOrder(t *testing.T) {
	// T18
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	ex := newExample(t, e, alice)
	order := []model.PaperID{ex.invite, ex.search, ex.notify, ex.x}
	gt.Equal(t, e.paper(t, wsA, ex.db.ID).Items, order)

	top := e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpInsertItem, Title: ptr("top"), After: ptr("")}).Items[0].ID
	mid := e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpInsertItem, Title: ptr("mid"), After: ptr(string(ex.search))}).Items[0].ID
	gt.Equal(t, e.paper(t, wsA, ex.db.ID).Items, []model.PaperID{top, ex.invite, ex.search, mid, ex.notify, ex.x})

	itemBefore := e.paper(t, wsA, ex.x)
	e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpMoveItem, Item: string(ex.x), After: ptr("")})
	gt.Equal(t, e.paper(t, wsA, ex.db.ID).Items[0], ex.x)
	gt.Equal(t, e.paper(t, wsA, ex.x).UpdatedAt, itemBefore.UpdatedAt)

	page := gt.R1(e.uc.ListItems(ctx, alice, wsA, ex.db.ID, usecase.ItemsInput{})).NoError(t)
	gt.Equal(t, itemIDs(page), e.paper(t, wsA, ex.db.ID).Items)
	gt.Equal(t, valueIn(page.Items[0], ex.due), any("2026-12-01"))

	// an item of another database is not found
	other := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "O", Kind: model.PaperKindDatabase})).NoError(t)
	_, err := e.uc.Apply(ctx, alice, wsA, other.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpMoveItem, Item: string(ex.x)}}})
	gt.True(t, errors.Is(err, model.ErrNotFound))
	// nor is an ID that names nothing, read in the same round trip as real items
	_, err = e.uc.Apply(ctx, alice, wsA, ex.db.ID, usecase.BatchInput{Ops: []usecase.Op{
		{Kind: usecase.OpSetValues, Item: string(ex.x), Set: map[string]any{string(ex.due): "2027-02-02"}},
		{Kind: usecase.OpSetValues, Item: string(model.NewPaperID()), Set: map[string]any{string(ex.due): "2027-02-02"}},
	}})
	gt.True(t, errors.Is(err, model.ErrNotFound))
}

func TestItemVersionsKeepValues(t *testing.T) {
	// T19
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	ex := newExample(t, e, alice)
	v1 := gt.R1(e.uc.SaveVersion(ctx, alice, wsA, ex.invite, "")).NoError(t)
	gt.A(t, v1.Values).Length(2)
	none := gt.R1(e.uc.SaveVersion(ctx, alice, wsA, ex.invite, "")).NoError(t)
	gt.Nil(t, none)

	e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpSetValues, Item: string(ex.invite), Set: map[string]any{string(ex.due): nil, string(ex.status): string(ex.review)}})
	v2 := gt.R1(e.uc.SaveVersion(ctx, alice, wsA, ex.invite, "")).NoError(t)
	gt.NotNil(t, v2)
	gt.A(t, v2.Values).Length(1)

	gt.R1(e.uc.RestoreVersion(ctx, alice, wsA, ex.invite, 1)).NoError(t)
	view := gt.R1(e.uc.GetPaper(ctx, alice, wsA, ex.invite)).NoError(t)
	row := &usecase.ItemRow{Values: view.Values}
	gt.Equal(t, valueIn(row, ex.status), any(string(ex.doing)))
	gt.Equal(t, valueIn(row, ex.due), any("2026-12-10"))

	// t4 of 2.7: a value whose element is gone is dropped on restore
	e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpDeleteElement, Property: string(ex.status), Element: string(ex.doing)})
	gt.R1(e.uc.RestoreVersion(ctx, alice, wsA, ex.invite, 1)).NoError(t)
	left := gt.R1(e.repo.GetPropertyValues(ctx, wsA, []model.PropertyValueRef{{PaperID: ex.invite, PropertyID: ex.status}})).NoError(t)
	gt.A(t, left).Length(0)
}

func TestItemStatusFollowsValues(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	ex := newExample(t, e, alice)

	// an untitled item with no body but a value has something to save
	res := e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpInsertItem, Ref: "n", Set: map[string]any{string(ex.status): string(ex.doing)}})
	blank := model.PaperID(res.Refs["n"])
	st := gt.R1(e.uc.Status(ctx, alice, wsA, blank)).NoError(t)
	gt.True(t, st.Changed)

	// deleting an element changes no saved item by itself
	v1 := gt.R1(e.uc.SaveVersion(ctx, alice, wsA, ex.invite, "")).NoError(t)
	gt.NotNil(t, v1)
	e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpDeleteElement, Property: string(ex.status), Element: string(ex.doing)})
	st = gt.R1(e.uc.Status(ctx, alice, wsA, ex.invite)).NoError(t)
	gt.False(t, st.Changed)
	none := gt.R1(e.uc.SaveVersion(ctx, alice, wsA, ex.invite, "")).NoError(t)
	gt.Nil(t, none)

	// a real change still shows
	e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpSetValues, Item: string(ex.invite), Set: map[string]any{string(ex.due): "2027-03-03"}})
	st = gt.R1(e.uc.Status(ctx, alice, wsA, ex.invite)).NoError(t)
	gt.True(t, st.Changed)
}

func TestItemsAreNotUnfiled(t *testing.T) {
	// Items have no ParentID; they belong to their database, not to the
	// unfiled papers, and lists show the database as where they are.
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	ex := newExample(t, e, alice)
	item := e.paper(t, wsA, ex.invite)
	gt.Equal(t, item.ParentID, model.PaperID(""))
	gt.Equal(t, item.DatabaseID, ex.db.ID)

	unfiled := gt.R1(e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{Place: usecase.PlaceUnfiled, Creator: usecase.CreatorMe})).NoError(t)
	for _, row := range unfiled.Papers {
		gt.NotEqual(t, row.ID, ex.invite)
	}
	side := gt.R1(e.uc.Sidebar(ctx, alice, wsA)).NoError(t)
	gt.Equal(t, side.UnfiledCount, 0)

	filed := gt.R1(e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{Place: usecase.PlaceFiled, Creator: usecase.CreatorMe})).NoError(t)
	found := false
	for _, row := range filed.Papers {
		if row.ID == ex.invite {
			found = true
			gt.True(t, row.Parent != nil).Required()
			gt.Equal(t, row.Parent.ID, ex.db.ID)
		}
	}
	gt.True(t, found)

	// breadcrumbs go through the database
	view := gt.R1(e.uc.GetPaper(ctx, alice, wsA, ex.invite)).NoError(t)
	gt.Equal(t, view.Path[len(view.Path)-1].ID, ex.db.ID)
}

func TestItemsAndTheTree(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	ex := newExample(t, e, alice)
	home := e.home(t, wsA)

	// T21: items stay in their database; nothing else goes in
	_, err := e.uc.MovePaper(ctx, alice, wsA, ex.invite, usecase.MoveInput{Parent: string(home)})
	isInvalid(t, err)
	plain := e.child(t, alice, wsA, "", "plain")
	_, err = e.uc.MovePaper(ctx, alice, wsA, plain.ID, usecase.MoveInput{Parent: string(ex.db.ID)})
	isInvalid(t, err)
	gt.R1(e.uc.MovePaper(ctx, alice, wsA, plain.ID, usecase.MoveInput{Parent: string(ex.invite)})).NoError(t)
	gt.Equal(t, e.paper(t, wsA, plain.ID).ParentID, ex.invite)

	// T27: items are not children; a database with items can be unfiled
	gt.R1(e.uc.MovePaper(ctx, alice, wsA, ex.db.ID, usecase.MoveInput{Unfiled: true})).NoError(t)
	gt.Equal(t, e.paper(t, wsA, ex.db.ID).ParentID, model.PaperID(""))
	view := gt.R1(e.uc.GetPaper(ctx, alice, wsA, plain.ID)).NoError(t)
	gt.A(t, view.Path).Length(2) // the unfiled database, then the item

	// T20: someone else's item is archived, not trashed
	r := e.apply(t, bob, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpInsertItem, Title: ptr("bob's")})
	bobs := r.Items[0].ID
	_, err = e.uc.Trash(ctx, alice, wsA, bobs)
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))
	gt.R1(e.uc.Archive(ctx, alice, wsA, bobs, false)).NoError(t)
	page := gt.R1(e.uc.ListItems(ctx, alice, wsA, ex.db.ID, usecase.ItemsInput{})).NoError(t)
	gt.False(t, slices.Contains(itemIDs(page), bobs))
	withArchived := gt.R1(e.uc.ListItems(ctx, alice, wsA, ex.db.ID, usecase.ItemsInput{Archived: true})).NoError(t)
	gt.True(t, slices.Contains(itemIDs(withArchived), bobs))
	gt.R1(e.uc.Trash(ctx, bob, wsA, bobs)).NoError(t)
	gt.True(t, slices.Contains(e.paper(t, wsA, ex.db.ID).Items, bobs))
}

func TestTrashAndRestoreDatabase(t *testing.T) {
	// T24, T26
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	ex := newExample(t, e, alice)

	gt.R1(e.uc.Trash(ctx, alice, wsA, ex.search)).NoError(t)
	gt.True(t, e.paper(t, wsA, ex.search).IsTrashed())
	gt.R1(e.uc.RestoreFromTrash(ctx, alice, wsA, ex.search)).NoError(t)
	gt.False(t, e.paper(t, wsA, ex.search).IsTrashed())
	gt.Equal(t, e.paper(t, wsA, ex.db.ID).Items[1], ex.search)
	blocks, err := e.repo.ListPaperBlocks(ctx, wsA, e.home(t, wsA))
	gt.NoError(t, err)
	gt.A(t, blocks).Length(1) // only the database's paper block

	gt.R1(e.uc.Trash(ctx, alice, wsA, ex.db.ID)).NoError(t)
	for _, id := range []model.PaperID{ex.db.ID, ex.invite, ex.search, ex.notify, ex.x} {
		q := e.paper(t, wsA, id)
		gt.True(t, q.IsTrashed())
		gt.Equal(t, q.DeletedWith, ex.db.ID)
	}
	_, err = e.uc.RestoreFromTrash(ctx, alice, wsA, ex.invite)
	isInvalid(t, err)
	gt.R1(e.uc.RestoreFromTrash(ctx, alice, wsA, ex.db.ID)).NoError(t)
	for _, id := range []model.PaperID{ex.db.ID, ex.invite, ex.search, ex.notify, ex.x} {
		gt.False(t, e.paper(t, wsA, id).IsTrashed())
	}
	page := gt.R1(e.uc.ListItems(ctx, alice, wsA, ex.db.ID, usecase.ItemsInput{})).NoError(t)
	gt.A(t, page.Items).Length(4)

	// someone else's item makes the database archive instead
	bob := e.user(t, "bob")
	e.apply(t, bob, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpInsertItem, Title: ptr("bob's")})
	gt.R1(e.uc.Trash(ctx, alice, wsA, ex.db.ID)).NoError(t)
	gt.False(t, e.paper(t, wsA, ex.db.ID).IsTrashed())
	gt.True(t, e.paper(t, wsA, ex.db.ID).IsArchived())
}

func TestPurgeTakesItemsOut(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	ex := newExample(t, e, alice)
	gt.R1(e.uc.Trash(ctx, alice, wsA, ex.search)).NoError(t)
	e.clock.Advance(31 * 24 * time.Hour)
	gt.V(t, gt.R1(e.uc.PurgeTrash(ctx)).NoError(t)).Equal(1)
	gt.Equal(t, e.paper(t, wsA, ex.db.ID).Items, []model.PaperID{ex.invite, ex.notify, ex.x})
	events := gt.R1(e.repo.ListEvents(ctx, wsA, ex.db.ID, time.Time{})).NoError(t)
	gt.True(t, slices.ContainsFunc(events, func(ev *model.Event) bool {
		return ev.Type == model.EventItemDeleted && ev.ItemID == ex.search
	}))
}

func TestMoveDatabaseAcrossWorkspaces(t *testing.T) {
	// T25
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	ex := newExample(t, e, alice)
	gt.R1(e.uc.SaveVersion(ctx, alice, wsA, ex.invite, "")).NoError(t)
	e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpInsertItem, Title: ptr("empty")})
	gt.R1(e.uc.MovePaper(ctx, alice, wsA, ex.db.ID, usecase.MoveInput{Workspace: wsB, Parent: string(e.home(t, wsB))})).NoError(t)

	f := gt.R1(e.repo.GetForward(ctx, wsA, ex.db.ID)).NoError(t)
	cp := e.paper(t, wsB, f.ToPaperID)
	gt.Equal(t, cp.Kind, model.PaperKindDatabase)
	gt.Equal(t, cp.Properties, ex.db.Properties)
	gt.A(t, cp.Items).Length(5)
	page := gt.R1(e.uc.ListItems(ctx, alice, wsB, cp.ID, usecase.ItemsInput{})).NoError(t)
	gt.A(t, page.Items).Length(5)
	gt.Equal(t, page.Items[0].Item.Title, "招待機能")
	gt.Equal(t, page.Items[0].Item.DatabaseID, cp.ID)
	gt.Equal(t, valueIn(page.Items[0], ex.due), any("2026-12-10"))
	gt.Equal(t, page.Items[3].Item.Title, "エクスポートの高速化")
	for _, row := range page.Items {
		for _, v := range row.Values {
			gt.Equal(t, v.DatabaseID, cp.ID)
			gt.Equal(t, v.WorkspaceID, wsB)
		}
	}
	v := gt.R1(e.uc.GetVersion(ctx, alice, wsB, page.Items[0].Item.ID, 1)).NoError(t)
	gt.A(t, v.Values).Length(2)
	gt.True(t, e.paper(t, wsA, ex.db.ID).IsTrashed())
}

func TestDatabaseFeed(t *testing.T) {
	// T29
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	ex := newExample(t, e, alice)
	e.clock.Advance(time.Second)
	since := e.clock.Now()
	of := func(paper model.PaperID) []*model.Event {
		return gt.R1(e.repo.ListEvents(ctx, wsA, paper, since)).NoError(t)
	}
	has := func(evs []*model.Event, typ model.EventType) bool {
		return slices.ContainsFunc(evs, func(ev *model.Event) bool { return ev.Type == typ })
	}
	e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpSetValues, Item: string(ex.invite), Set: map[string]any{string(ex.due): "2026-12-05"}})
	gt.True(t, has(of(ex.db.ID), model.EventPropertyValuePut))
	gt.True(t, has(of(ex.invite), model.EventPropertyValuePut))
	gt.False(t, has(of(ex.db.ID), model.EventPaperUpdated))

	e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpSetValues, Item: string(ex.invite), Set: map[string]any{string(ex.due): nil}})
	gt.True(t, has(of(ex.db.ID), model.EventPropertyValueDeleted))

	e.apply(t, alice, wsA, ex.invite, usecase.Op{Kind: usecase.OpSetTitle, Title: ptr("招待")})
	gt.True(t, slices.ContainsFunc(of(ex.db.ID), func(ev *model.Event) bool {
		return ev.Type == model.EventItemPut && ev.Paper.ID == ex.invite && ev.Paper.Title == "招待"
	}))
	gt.R1(e.uc.Archive(ctx, alice, wsA, ex.notify, false)).NoError(t)
	gt.True(t, slices.ContainsFunc(of(ex.db.ID), func(ev *model.Event) bool {
		return ev.Type == model.EventItemPut && ev.Paper.ID == ex.notify && ev.Paper.IsArchived()
	}))
	e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpMoveItem, Item: string(ex.x), After: ptr("")})
	gt.True(t, has(of(ex.db.ID), model.EventPaperUpdated))
}

func TestFilterAndSortItems(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	ex := newExample(t, e, alice)
	list := func(in usecase.ItemsInput) []model.PaperID {
		t.Helper()
		return itemIDs(gt.R1(e.uc.ListItems(ctx, alice, wsA, ex.db.ID, in)).NoError(t))
	}
	doingOrReview := []usecase.FilterCondition{{Property: ex.status, Op: usecase.FilterIs, Elements: []model.ElementID{ex.doing, ex.review}}}

	// T36: no sort keeps the Items order
	gt.Equal(t, list(usecase.ItemsInput{Filter: doingOrReview}), []model.PaperID{ex.invite, ex.notify, ex.x})

	// T37: by date, empty last in both directions; with a filter
	due := usecase.ItemSortSpec{Property: ex.due}
	gt.Equal(t, list(usecase.ItemsInput{Sort: due}), []model.PaperID{ex.x, ex.invite, ex.search, ex.notify})
	gt.Equal(t, list(usecase.ItemsInput{Sort: usecase.ItemSortSpec{Property: ex.due, Desc: true}}), []model.PaperID{ex.search, ex.invite, ex.x, ex.notify})
	gt.Equal(t, list(usecase.ItemsInput{Sort: due, Filter: doingOrReview}), []model.PaperID{ex.x, ex.invite, ex.notify})

	// T38: a select sorts by its elements' order, which move_element
	// changes; items with the same element come in item ID order
	doing := []model.PaperID{ex.invite, ex.notify}
	slices.Sort(doing)
	gt.Equal(t, list(usecase.ItemsInput{Sort: usecase.ItemSortSpec{Property: ex.status}}), append([]model.PaperID{ex.search}, append(doing, ex.x)...))
	e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpMoveElement, Property: string(ex.status), Element: string(ex.review), After: ptr("")})
	gt.Equal(t, list(usecase.ItemsInput{Sort: usecase.ItemSortSpec{Property: ex.status}}), append([]model.PaperID{ex.x, ex.search}, doing...))

	// paper fields
	gt.Equal(t, list(usecase.ItemsInput{Sort: usecase.ItemSortSpec{Field: model.ItemSortTitle}}), []model.PaperID{ex.x, ex.invite, ex.search, ex.notify})

	// other operators
	gt.Equal(t, list(usecase.ItemsInput{Filter: []usecase.FilterCondition{{Property: ex.due, Op: usecase.FilterIsEmpty}}}), []model.PaperID{ex.notify})
	gt.Equal(t, list(usecase.ItemsInput{Filter: []usecase.FilterCondition{{Property: ex.due, Op: usecase.FilterBetween, From: "2026-12-01", To: "2026-12-10"}}}), []model.PaperID{ex.invite, ex.x})
	gt.Equal(t, list(usecase.ItemsInput{Filter: []usecase.FilterCondition{{Property: ex.due, Op: usecase.FilterBefore, Date: "2026-12-01"}}}), []model.PaperID{ex.x})

	// one at a time with the cursor, in every mode, with no repeat or gap
	for _, in := range []usecase.ItemsInput{
		{Filter: doingOrReview},
		{Sort: due},
		{Sort: usecase.ItemSortSpec{Property: ex.due, Desc: true}, Filter: doingOrReview},
		{Sort: usecase.ItemSortSpec{Property: ex.status}},
		{Sort: usecase.ItemSortSpec{Field: model.ItemSortCreated, Desc: true}},
	} {
		want := list(in)
		var got []model.PaperID
		in.Limit = 1
		for range 10 {
			page := gt.R1(e.uc.ListItems(ctx, alice, wsA, ex.db.ID, in)).NoError(t)
			got = append(got, itemIDs(page)...)
			if page.NextCursor == "" {
				break
			}
			in.After = page.NextCursor
		}
		gt.Equal(t, got, want)
	}

	// items added while reading do not repeat what was read
	in := usecase.ItemsInput{Limit: 2}
	first := gt.R1(e.uc.ListItems(ctx, alice, wsA, ex.db.ID, in)).NoError(t)
	e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpInsertItem, Title: ptr("new top"), After: ptr("")})
	in.After = first.NextCursor
	rest := gt.R1(e.uc.ListItems(ctx, alice, wsA, ex.db.ID, in)).NoError(t)
	gt.False(t, slices.ContainsFunc(rest.Items, func(r *usecase.ItemRow) bool { return slices.Contains(itemIDs(first), r.Item.ID) }))
}

func TestInvalidQueries(t *testing.T) {
	// T39
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	ex := newExample(t, e, alice)
	tags := e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpAddProperty, Name: ptr("Tags"), PropertyType: model.PropertyMultiSelect, Ref: "t"}).Refs["t"]
	e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpDeleteElement, Property: string(ex.status), Element: string(ex.todo)})
	bad := []usecase.ItemsInput{
		{Filter: []usecase.FilterCondition{{Property: ex.status, Op: usecase.FilterIs, Elements: []model.ElementID{ex.todo}}}},
		{Filter: []usecase.FilterCondition{{Property: model.NewPropertyID(), Op: usecase.FilterIsEmpty}}},
		{Sort: usecase.ItemSortSpec{Property: model.PropertyID(tags)}},
		{Filter: []usecase.FilterCondition{{Property: ex.due, Op: usecase.FilterStartsWith, Text: "2026"}}},
		{Filter: slices.Repeat([]usecase.FilterCondition{{Property: ex.due, Op: usecase.FilterIsEmpty}}, 6)},
		{Sort: usecase.ItemSortSpec{Field: model.ItemSortCreated}},
		{After: "garbage"},
	}
	for _, in := range bad {
		_, err := e.uc.ListItems(ctx, alice, wsA, ex.db.ID, in)
		isInvalid(t, err)
	}
	// a cursor of another query
	first := gt.R1(e.uc.ListItems(ctx, alice, wsA, ex.db.ID, usecase.ItemsInput{Limit: 1})).NoError(t)
	_, err := e.uc.ListItems(ctx, alice, wsA, ex.db.ID, usecase.ItemsInput{Limit: 1, After: first.NextCursor, Sort: usecase.ItemSortSpec{Property: ex.due}})
	isInvalid(t, err)
	// not a database
	_, err = e.uc.ListItems(ctx, alice, wsA, ex.invite, usecase.ItemsInput{})
	isInvalid(t, err)
}

func TestItemsIntegrity(t *testing.T) {
	// T40: after inserts, moves, trash, restore, purge and a move across
	// workspaces, Items lists each item that is not purged exactly once,
	// and every value's DatabaseID is its item's parent.
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	ex := newExample(t, e, alice)
	e.apply(t, alice, wsA, ex.db.ID,
		usecase.Op{Kind: usecase.OpInsertItem, Title: ptr("n"), Ref: "n", Set: map[string]any{string(ex.due): "2026-01-01"}},
		usecase.Op{Kind: usecase.OpMoveItem, Item: "ref:n", After: ptr(string(ex.invite))},
	)
	gt.R1(e.uc.Trash(ctx, alice, wsA, ex.notify)).NoError(t)
	gt.R1(e.uc.RestoreFromTrash(ctx, alice, wsA, ex.notify)).NoError(t)
	gt.R1(e.uc.Trash(ctx, alice, wsA, ex.x)).NoError(t)
	e.clock.Advance(31 * 24 * time.Hour)
	gt.R1(e.uc.PurgeTrash(ctx)).NoError(t)
	check := func(ws model.WorkspaceID, db *model.Paper) {
		kids, _ := gt.R2(e.repo.ListItems(ctx, ws, db.ID, model.ItemQuery{Sort: model.ItemSortTitle, Limit: model.MaxPropertyValueQueryLimit})).NoError(t)
		var live []model.PaperID
		for _, k := range kids {
			live = append(live, k.ID)
		}
		got := slices.Clone(db.Items)
		slices.Sort(got)
		slices.Sort(live)
		gt.Equal(t, got, live)
		var refs []model.PropertyValueRef
		for _, id := range db.Items {
			for _, prop := range db.Properties {
				refs = append(refs, model.PropertyValueRef{PaperID: id, PropertyID: prop.ID})
			}
		}
		for _, v := range gt.R1(e.repo.GetPropertyValues(ctx, ws, refs)).NoError(t) {
			gt.Equal(t, v.DatabaseID, db.ID)
		}
	}
	check(wsA, e.paper(t, wsA, ex.db.ID))
	gt.R1(e.uc.MovePaper(ctx, alice, wsA, ex.db.ID, usecase.MoveInput{Workspace: wsB, Parent: string(e.home(t, wsB))})).NoError(t)
	f := gt.R1(e.repo.GetForward(ctx, wsA, ex.db.ID)).NoError(t)
	check(wsB, e.paper(t, wsB, f.ToPaperID))
}

func TestDatabaseWrites(t *testing.T) {
	e := newEnv(t)
	alice := e.user(t, "alice")
	ex := newExample(t, e, alice)

	// T44: deleting a property writes no value, however many items
	var ops []usecase.Op
	for range 200 {
		ops = append(ops, usecase.Op{Kind: usecase.OpInsertItem, Set: map[string]any{string(ex.due): "2026-01-01"}})
	}
	e.apply(t, alice, wsA, ex.db.ID, ops...)
	res := e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpDeleteProperty, Property: string(ex.due)})
	gt.A(t, res.Values).Length(0)
	gt.A(t, res.Items).Length(0)

	// T45: a value write touches neither the item nor the database record
	before := e.paper(t, wsA, ex.db.ID)
	item := e.paper(t, wsA, ex.invite)
	e.clock.Advance(time.Hour)
	res = e.apply(t, alice, wsA, ex.db.ID, usecase.Op{Kind: usecase.OpSetValues, Item: string(ex.invite), Set: map[string]any{string(ex.status): string(ex.review)}})
	gt.A(t, res.Values).Length(1)
	gt.Equal(t, e.paper(t, wsA, ex.db.ID).UpdatedAt, before.UpdatedAt)
	gt.Equal(t, e.paper(t, wsA, ex.invite).UpdatedAt, item.UpdatedAt)
}

func TestItemsPageReads(t *testing.T) {
	// T46: a page reads the database and its items and values in batches,
	// not once per item.
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	ex := newExample(t, e, alice)
	var ops []usecase.Op
	for i := range 200 {
		set := map[string]any{}
		if i%100 == 0 {
			set[string(ex.status)] = string(ex.review)
		}
		ops = append(ops, usecase.Op{Kind: usecase.OpInsertItem, Set: set})
	}
	e.apply(t, alice, wsA, ex.db.ID, ops...)

	e.count.reset()
	page := gt.R1(e.uc.ListItems(ctx, alice, wsA, ex.db.ID, usecase.ItemsInput{Limit: 50})).NoError(t)
	gt.A(t, page.Items).Length(50)
	gt.Equal(t, e.count.callsOf("GetPropertyValues"), 1)
	gt.True(t, e.count.reads() <= 6).Describef("reads: %d", e.count.reads())

	e.count.reset()
	filtered := gt.R1(e.uc.ListItems(ctx, alice, wsA, ex.db.ID, usecase.ItemsInput{Limit: 50, Filter: []usecase.FilterCondition{{Property: ex.status, Op: usecase.FilterIs, Elements: []model.ElementID{ex.review}}}})).NoError(t)
	gt.True(t, len(filtered.Items) >= 1)
	gt.True(t, e.count.callsOf("GetPropertyValues") <= 5)
	gt.True(t, e.count.reads() <= 15).Describef("reads: %d", e.count.reads())

	e.count.reset()
	sorted := gt.R1(e.uc.ListItems(ctx, alice, wsA, ex.db.ID, usecase.ItemsInput{Limit: 50, Sort: usecase.ItemSortSpec{Property: ex.due}})).NoError(t)
	gt.A(t, sorted.Items).Length(50)
	gt.True(t, e.count.reads() <= 15).Describef("reads: %d", e.count.reads())
}

func TestDatabaseAccess(t *testing.T) {
	// T47
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	ex := newExample(t, e, alice)
	ro := &model.Principal{User: bob.User, Token: &model.APIToken{ID: model.NewTokenID(), Kind: model.TokenPersonal, Scope: model.ScopeRead}}
	_, err := e.uc.Apply(ctx, ro, wsA, ex.db.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpInsertItem}}})
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))
	gt.R1(e.uc.ListItems(ctx, ro, wsA, ex.db.ID, usecase.ItemsInput{})).NoError(t)
	e.deny(wsA, bob)
	_, err = e.uc.ListItems(ctx, bob, wsA, ex.db.ID, usecase.ItemsInput{})
	gt.True(t, errors.Is(err, model.ErrNotFound))
	_, err = e.uc.GetPaper(ctx, bob, wsA, ex.db.ID)
	gt.True(t, errors.Is(err, model.ErrNotFound))
}

func TestDatabaseItemLimit(t *testing.T) {
	// T49
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	db := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "D", Kind: model.PaperKindDatabase})).NoError(t)
	full := e.paper(t, wsA, db.ID)
	for len(full.Items) < model.MaxDatabaseItems-1 {
		full.Items = append(full.Items, model.NewPaperID())
	}
	putPaper(t, e, full)
	e.apply(t, alice, wsA, db.ID, usecase.Op{Kind: usecase.OpInsertItem, Title: ptr("last")})
	_, err := e.uc.Apply(ctx, alice, wsA, db.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpInsertItem, Title: ptr("over")}}})
	isInvalid(t, err)
}

func TestExportDatabase(t *testing.T) {
	// T33
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	ex := newExample(t, e, alice)
	tags := e.apply(t, alice, wsA, ex.db.ID,
		usecase.Op{Kind: usecase.OpAddProperty, Name: ptr("タグ"), PropertyType: model.PropertyMultiSelect, Ref: "t"},
		usecase.Op{Kind: usecase.OpAddElement, Property: "ref:t", Value: ptr("検索"), Ref: "g1"},
		usecase.Op{Kind: usecase.OpAddElement, Property: "ref:t", Value: ptr("UI"), Ref: "g2"},
		usecase.Op{Kind: usecase.OpSetValues, Item: string(ex.invite), Set: map[string]any{"ref:t": []any{"ref:g1", "ref:g2"}}},
	)
	gt.True(t, model.ValidID(tags.Refs["t"]))
	md := gt.R1(e.uc.Export(ctx, alice, wsA, ex.db.ID)).NoError(t)
	gt.S(t, md).Contains("# プロジェクト")
	gt.S(t, md).Contains("| Name | 状態 | 期日 | タグ |")
	gt.S(t, md).Contains("| 招待機能 | 進行中 | 2026-12-10 |")
	gt.True(t, slices.ContainsFunc([]string{"検索, UI", "UI, 検索"}, func(s string) bool { return containsStr(md, s) }))
	e.apply(t, alice, wsA, ex.invite, para("本文"))
	item := gt.R1(e.uc.Export(ctx, alice, wsA, ex.invite)).NoError(t)
	gt.S(t, item).Contains("# 招待機能\n\n- 状態: 進行中\n- 期日: 2026-12-10")
	gt.S(t, item).Contains("本文")
}

func putPaper(t *testing.T, e *env, q *model.Paper) {
	t.Helper()
	gt.NoError(t, e.repo.RunTx(context.Background(), q.WorkspaceID, func(tx interfaces.Tx) error { return tx.PutPaper(q) })).Required()
}

func containsStr(s, sub string) bool { return strings.Contains(s, sub) }
