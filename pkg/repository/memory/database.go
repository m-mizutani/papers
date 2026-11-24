package memory

import (
	"cmp"
	"context"
	"slices"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func (r *Repository) GetPropertyValues(_ context.Context, ws model.WorkspaceID, refs []model.PropertyValueRef) ([]*model.PropertyValue, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*model.PropertyValue
	for _, ref := range refs {
		d := r.papers[paperKey{ws, ref.PaperID}]
		if d == nil {
			continue
		}
		if v, ok := d.values[ref.PropertyID]; ok {
			out = append(out, v.Clone())
		}
	}
	return out, nil
}

// QueryPropertyValues mirrors the Firestore query on the properties
// collection group: the values of one property of a database, ordered by
// Value and then by item ID (both descending when q.Desc).
func (r *Repository) QueryPropertyValues(_ context.Context, ws model.WorkspaceID, db model.PaperID, q model.PropertyValueQuery) ([]*model.PropertyValue, string, error) {
	if err := q.Validate(); err != nil {
		return nil, "", err
	}
	r.mu.RLock()
	var all []*model.PropertyValue
	for key, d := range r.papers {
		if key.ws != ws {
			continue
		}
		v, ok := d.values[q.Property]
		if !ok || v.DatabaseID != db {
			continue
		}
		s, isStr := v.Str()
		if !isStr || (q.Equals != nil && s != *q.Equals) {
			continue
		}
		all = append(all, v.Clone())
	}
	r.mu.RUnlock()
	compare := func(a, b *model.PropertyValue) int {
		as, _ := a.Str()
		bs, _ := b.Str()
		c := cmp.Or(cmp.Compare(as, bs), cmp.Compare(a.PaperID, b.PaperID))
		if q.Desc {
			return -c
		}
		return c
	}
	slices.SortFunc(all, compare)
	if q.Cursor != "" {
		c, err := model.ParseValueCursor(q.Cursor)
		if err != nil {
			return nil, "", err
		}
		at := &model.PropertyValue{PaperID: c.Item, Value: c.Value}
		i := 0
		for i < len(all) && compare(all[i], at) <= 0 {
			i++
		}
		all = all[i:]
	}
	if len(all) > q.Limit {
		return all[:q.Limit], model.EncodeValueCursor(all[q.Limit-1]), nil
	}
	return all, "", nil
}

func (r *Repository) ListItems(_ context.Context, ws model.WorkspaceID, db model.PaperID, q model.ItemQuery) ([]*model.Paper, string, error) {
	if err := q.Validate(); err != nil {
		return nil, "", err
	}
	all := r.listPapers(ws, func(p *model.Paper) bool { return p.DatabaseID == db })
	compare := func(a, b *model.Paper) int {
		var c int
		switch q.Sort {
		case model.ItemSortTitle:
			c = cmp.Compare(a.Title, b.Title)
		case model.ItemSortCreated:
			c = a.CreatedAt.Compare(b.CreatedAt)
		default:
			c = a.UpdatedAt.Compare(b.UpdatedAt)
		}
		c = cmp.Or(c, cmp.Compare(a.ID, b.ID))
		if q.Desc {
			return -c
		}
		return c
	}
	slices.SortFunc(all, compare)
	if q.Cursor != "" {
		c, err := model.ParseItemCursor(q.Cursor)
		if err != nil {
			return nil, "", err
		}
		at := &model.Paper{ID: c.ID, Title: c.Title, CreatedAt: c.At, UpdatedAt: c.At}
		i := 0
		for i < len(all) && compare(all[i], at) <= 0 {
			i++
		}
		all = all[i:]
	}
	if len(all) > q.Limit {
		return all[:q.Limit], model.ItemCursorOf(q.Sort, all[q.Limit-1]), nil
	}
	return all, "", nil
}
