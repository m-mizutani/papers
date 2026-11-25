package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

// databaseIn makes a database with a select and a date property, and its
// items with the given titles, in one transaction.
func databaseIn(t *testing.T, repo interfaces.Repository, ws model.WorkspaceID, titles ...string) (*model.Paper, []*model.Paper) {
	t.Helper()
	db := paperIn(ws, "", "Projects")
	db.Kind = model.PaperKindDatabase
	db.Properties = []model.Property{
		{ID: model.NewPropertyID(), Name: "Status", Type: model.PropertySelect, Elements: []model.Element{
			{ID: model.NewElementID(), Value: "Todo", Color: "gray"},
			{ID: model.NewElementID(), Value: "Doing", Color: "blue"},
		}},
		{ID: model.NewPropertyID(), Name: "Due", Type: model.PropertyDate},
		{ID: model.NewPropertyID(), Name: "Tags", Type: model.PropertyMultiSelect, Elements: []model.Element{
			{ID: model.NewElementID(), Value: "search", Color: "green"},
		}},
		{ID: model.NewPropertyID(), Name: "Memo", Type: model.PropertyText},
		{ID: model.NewPropertyID(), Name: "Owner", Type: model.PropertyPerson},
	}
	var items []*model.Paper
	for i, title := range titles {
		q := paperIn(ws, "", title)
		q.DatabaseID = db.ID
		q.CreatedAt = q.CreatedAt.Add(time.Duration(i) * time.Second)
		q.UpdatedAt = q.CreatedAt
		items = append(items, q)
		db.Items = append(db.Items, q.ID)
	}
	gt.NoError(t, repo.RunTx(context.Background(), ws, func(tx interfaces.Tx) error {
		if err := tx.PutPaper(db); err != nil {
			return err
		}
		for _, q := range items {
			if err := tx.PutPaper(q); err != nil {
				return err
			}
		}
		return nil
	})).Required()
	return db, items
}

func valueOf(db, item *model.Paper, prop model.PropertyID, v any) *model.PropertyValue {
	return &model.PropertyValue{
		WorkspaceID: db.WorkspaceID, DatabaseID: db.ID, PaperID: item.ID, PropertyID: prop,
		Value: v, UpdatedBy: humanActor(item.Owner()), UpdatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
}

func TestDatabaseRoundTrip(t *testing.T) {
	// T22: every field of a database, an item, its property values and an
	// item's version survives storage.
	runRepositoryTest(t, "database, items and values", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		ws := newWorkspaceID()
		db, items := databaseIn(t, repo, ws, "A", "B")
		status, due, tags, memo, owner := db.Properties[0], db.Properties[1], db.Properties[2], db.Properties[3], db.Properties[4]
		a := items[0]
		vals := []*model.PropertyValue{
			valueOf(db, a, status.ID, string(status.Elements[1].ID)),
			valueOf(db, a, due.ID, "2026-12-10"),
			valueOf(db, a, tags.ID, []string{string(tags.Elements[0].ID)}),
			valueOf(db, a, memo.ID, "sort by relevance"),
			valueOf(db, a, owner.ID, model.PeopleValue([]model.PersonRef{{UserID: model.NewUserID(), Name: "Ann"}, {UserID: model.NewUserID(), Name: "Bob"}})),
		}
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
			for _, v := range vals {
				if err := tx.PutPropertyValue(v); err != nil {
					return err
				}
			}
			return nil
		}))

		got := gt.R1(repo.GetPaper(ctx, ws, db.ID)).NoError(t)
		gt.Equal(t, got.Kind, model.PaperKindDatabase)
		gt.Equal(t, got.Properties, db.Properties)
		gt.Equal(t, got.Items, db.Items)
		gotItem := gt.R1(repo.GetPaper(ctx, ws, a.ID)).NoError(t)
		gt.Equal(t, gotItem.DatabaseID, db.ID)
		gt.Equal(t, gotItem.ParentID, model.PaperID(""))

		var refs []model.PropertyValueRef
		for _, v := range vals {
			refs = append(refs, v.Ref())
		}
		refs = append(refs, model.PropertyValueRef{PaperID: items[1].ID, PropertyID: status.ID})
		read := gt.R1(repo.GetPropertyValues(ctx, ws, refs)).NoError(t)
		gt.A(t, read).Length(5)
		byProp := map[model.PropertyID]*model.PropertyValue{}
		for _, v := range read {
			byProp[v.PropertyID] = v
		}
		for _, v := range vals {
			r := byProp[v.PropertyID]
			gt.NotNil(t, r)
			gt.Equal(t, r.DatabaseID, db.ID)
			gt.Equal(t, r.PaperID, a.ID)
			gt.Equal(t, r.WorkspaceID, ws)
			gt.True(t, timeEq(r.UpdatedAt, v.UpdatedAt))
			gt.Equal(t, r.UpdatedBy, v.UpdatedBy)
			if want, ok := v.Strings(); ok {
				l, ok := r.Strings()
				gt.True(t, ok)
				gt.Equal(t, l, want)
			} else if want, ok := v.People(); ok {
				l, ok := r.People()
				gt.True(t, ok)
				gt.Equal(t, l, want)
			} else {
				s, ok := r.Str()
				gt.True(t, ok)
				gt.Equal(t, s, v.Value.(string))
			}
		}

		// within a transaction: list, overwrite, delete
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
			list, err := tx.ListItemPropertyValues(a.ID)
			if err != nil {
				return err
			}
			gt.A(t, list).Length(5)
			if err := tx.DeletePropertyValue(a.ID, memo.ID); err != nil {
				return err
			}
			list, err = tx.ListItemPropertyValues(a.ID)
			if err != nil {
				return err
			}
			gt.A(t, list).Length(4)
			got, err := tx.GetPropertyValues([]model.PropertyValueRef{{PaperID: a.ID, PropertyID: memo.ID}})
			gt.A(t, got).Length(0)
			return err
		}))
		left := gt.R1(repo.GetPropertyValues(ctx, ws, refs)).NoError(t)
		gt.A(t, left).Length(4)

		// a version keeps an item's values
		v := &model.Version{
			WorkspaceID: ws, PaperID: a.ID, Number: 1, Title: "A", CreatedBy: humanActor(a.Owner()), CreatedAt: time.Now().UTC(),
			Values: []model.VersionValue{{PropertyID: due.ID, Value: "2026-12-10"}, {PropertyID: tags.ID, Value: []string{string(tags.Elements[0].ID)}}},
		}
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutVersion(v) }))
		gv := gt.R1(repo.GetVersion(ctx, ws, a.ID, 1)).NoError(t)
		gt.A(t, gv.Values).Length(2)
		gt.Equal(t, gv.Values[0].Value, any("2026-12-10"))
		l, ok := (&model.PropertyValue{Value: gv.Values[1].Value}).Strings()
		gt.True(t, ok)
		gt.Equal(t, l, []string{string(tags.Elements[0].ID)})
	})

	runRepositoryTest(t, "purge removes the values", func(t *testing.T, repo interfaces.Repository) {
		// T28
		ctx := context.Background()
		ws := newWorkspaceID()
		db, items := databaseIn(t, repo, ws, "A")
		v := valueOf(db, items[0], db.Properties[3].ID, "memo")
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutPropertyValue(v) }))
		gt.NoError(t, repo.PurgePaper(ctx, ws, items[0].ID))
		got := gt.R1(repo.GetPropertyValues(ctx, ws, []model.PropertyValueRef{v.Ref()})).NoError(t)
		gt.A(t, got).Length(0)
		vals, _ := gt.R2(repo.QueryPropertyValues(ctx, ws, db.ID, model.PropertyValueQuery{Property: v.PropertyID, Limit: 10})).NoError(t)
		gt.A(t, vals).Length(0)
	})

	runRepositoryTest(t, "a value of an unknown item is refused", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		ws := newWorkspaceID()
		db, _ := databaseIn(t, repo, ws)
		v := valueOf(db, &model.Paper{ID: model.NewPaperID()}, db.Properties[3].ID, "x")
		gt.Error(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutPropertyValue(v) }))
		bad := valueOf(db, &model.Paper{ID: model.NewPaperID()}, db.Properties[3].ID, 12)
		gt.Error(t, bad.Validate())
	})
}

func TestQueryPropertyValues(t *testing.T) {
	// T41: the values of one property in Value order (then by item),
	// ascending and descending, by equal value, and with a cursor.
	runRepositoryTest(t, "sorted values", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		ws := newWorkspaceID()
		db, items := databaseIn(t, repo, ws, "A", "B", "C", "D")
		other, others := databaseIn(t, repo, ws, "X")
		status, due := db.Properties[0], db.Properties[1]
		todo, doing := string(status.Elements[0].ID), string(status.Elements[1].ID)
		vals := []*model.PropertyValue{
			valueOf(db, items[0], due.ID, "2026-12-10"),
			valueOf(db, items[1], due.ID, "2027-01-20"),
			valueOf(db, items[3], due.ID, "2026-12-01"),
			valueOf(db, items[0], status.ID, doing),
			valueOf(db, items[1], status.ID, todo),
			valueOf(db, items[2], status.ID, doing),
			// another database's value of a property with the same role
			valueOf(other, others[0], due.ID, "2026-01-01"),
		}
		for _, v := range vals {
			ws := v.WorkspaceID
			gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutPropertyValue(v) }))
		}
		ids := func(vs []*model.PropertyValue) []model.PaperID {
			var out []model.PaperID
			for _, v := range vs {
				out = append(out, v.PaperID)
			}
			return out
		}

		asc, next := gt.R2(repo.QueryPropertyValues(ctx, ws, db.ID, model.PropertyValueQuery{Property: due.ID, Limit: 10})).NoError(t)
		gt.Equal(t, ids(asc), []model.PaperID{items[3].ID, items[0].ID, items[1].ID})
		gt.Equal(t, next, "")
		desc, _ := gt.R2(repo.QueryPropertyValues(ctx, ws, db.ID, model.PropertyValueQuery{Property: due.ID, Desc: true, Limit: 10})).NoError(t)
		gt.Equal(t, ids(desc), []model.PaperID{items[1].ID, items[0].ID, items[3].ID})

		// one at a time with the cursor
		var walked []model.PaperID
		cursor := ""
		for range 5 {
			page, next := gt.R2(repo.QueryPropertyValues(ctx, ws, db.ID, model.PropertyValueQuery{Property: due.ID, Limit: 1, Cursor: cursor})).NoError(t)
			walked = append(walked, ids(page)...)
			if next == "" {
				break
			}
			cursor = next
		}
		gt.Equal(t, walked, ids(asc))

		// equal value, by item id
		eq, _ := gt.R2(repo.QueryPropertyValues(ctx, ws, db.ID, model.PropertyValueQuery{Property: status.ID, Equals: &doing, Limit: 10})).NoError(t)
		want := []model.PaperID{items[0].ID, items[2].ID}
		if want[0] > want[1] {
			want[0], want[1] = want[1], want[0]
		}
		gt.Equal(t, ids(eq), want)
		first, next := gt.R2(repo.QueryPropertyValues(ctx, ws, db.ID, model.PropertyValueQuery{Property: status.ID, Equals: &doing, Limit: 1})).NoError(t)
		gt.Equal(t, ids(first), want[:1])
		rest, _ := gt.R2(repo.QueryPropertyValues(ctx, ws, db.ID, model.PropertyValueQuery{Property: status.ID, Equals: &doing, Limit: 1, Cursor: next})).NoError(t)
		gt.Equal(t, ids(rest), want[1:])
	})

	runRepositoryTest(t, "items by paper field", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		ws := newWorkspaceID()
		db, items := databaseIn(t, repo, ws, "b", "a", "c")
		ids := func(ps []*model.Paper) []model.PaperID {
			var out []model.PaperID
			for _, p := range ps {
				out = append(out, p.ID)
			}
			return out
		}
		byTitle, _ := gt.R2(repo.ListItems(ctx, ws, db.ID, model.ItemQuery{Sort: model.ItemSortTitle, Limit: 10})).NoError(t)
		gt.Equal(t, ids(byTitle), []model.PaperID{items[1].ID, items[0].ID, items[2].ID})
		byTitleDesc, _ := gt.R2(repo.ListItems(ctx, ws, db.ID, model.ItemQuery{Sort: model.ItemSortTitle, Desc: true, Limit: 10})).NoError(t)
		gt.Equal(t, ids(byTitleDesc), []model.PaperID{items[2].ID, items[0].ID, items[1].ID})
		newest, next := gt.R2(repo.ListItems(ctx, ws, db.ID, model.ItemQuery{Sort: model.ItemSortCreated, Desc: true, Limit: 2})).NoError(t)
		gt.Equal(t, ids(newest), []model.PaperID{items[2].ID, items[1].ID})
		last, _ := gt.R2(repo.ListItems(ctx, ws, db.ID, model.ItemQuery{Sort: model.ItemSortCreated, Desc: true, Limit: 2, Cursor: next})).NoError(t)
		gt.Equal(t, ids(last), []model.PaperID{items[0].ID})
		_, _, err := repo.ListItems(ctx, ws, db.ID, model.ItemQuery{Sort: model.ItemSortUpdated, Limit: 2})
		gt.Error(t, err)
	})
}

func TestPropertyValueFeed(t *testing.T) {
	// T30: a value write reaches those watching the database, and the event
	// carries the value as stored.
	runRepositoryTest(t, "property values reach database watchers", func(t *testing.T, repo interfaces.Repository) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ws := newWorkspaceID()
		db, items := databaseIn(t, repo, ws, "A")
		status := db.Properties[0]
		ch := gt.R1(repo.Watch(ctx, ws, db.ID)).NoError(t)

		v := valueOf(db, items[0], status.ID, string(status.Elements[0].ID))
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error {
			if err := tx.PutPropertyValue(v); err != nil {
				return err
			}
			return tx.AddEvent(&model.Event{Type: model.EventPropertyValuePut, WorkspaceID: ws, PaperID: db.ID, At: time.Now().UTC(), PropertyValue: v})
		}))

		select {
		case ev, ok := <-ch:
			gt.True(t, ok).Required()
			gt.Equal(t, ev.Type, model.EventPropertyValuePut)
			gt.True(t, ev.PropertyValue != nil).Required()
			gt.Equal(t, ev.PropertyValue.Ref(), v.Ref())
			got, _ := ev.PropertyValue.Str()
			gt.Equal(t, got, string(status.Elements[0].ID))
		case <-ctx.Done():
			t.Fatal("feed timed out")
		}
	})
}

func TestItemsListedByDatabase(t *testing.T) {
	// Items have no ParentID: the unfiled query leaves them out, and
	// ListItems finds them by DatabaseID.
	runRepositoryTest(t, "items are found by database, not as unfiled", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		ws := newWorkspaceID()
		db, items := databaseIn(t, repo, ws, "A", "B")
		unfiled, _ := gt.R2(repo.ListPapers(ctx, ws, model.PaperQuery{Unfiled: true, Sort: model.SortUpdated, Limit: 100})).NoError(t)
		for _, q := range unfiled {
			gt.False(t, q.IsItem()).Describef("item %s listed as unfiled", q.ID)
		}
		got, _ := gt.R2(repo.ListItems(ctx, ws, db.ID, model.ItemQuery{Sort: model.ItemSortTitle, Limit: 10})).NoError(t)
		gt.A(t, got).Length(2)
		gt.Equal(t, got[0].ID, items[0].ID)
		kids := gt.R1(repo.ListChildren(ctx, ws, db.ID)).NoError(t)
		gt.A(t, kids).Length(0)
	})
}
