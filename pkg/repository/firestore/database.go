package firestore

import (
	"cmp"
	"context"
	"slices"

	"cloud.google.com/go/firestore"
	"github.com/m-mizutani/goerr/v2"
	"google.golang.org/api/iterator"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

// propertyValueDoc is papers/{item}/properties/{property}.
func (r *Repository) propertyValueDoc(ws model.WorkspaceID, ref model.PropertyValueRef) *firestore.DocumentRef {
	return r.paperDoc(ws, ref.PaperID).Collection(colProperties).Doc(string(ref.PropertyID))
}

func (r *Repository) valueRefs(ws model.WorkspaceID, refs []model.PropertyValueRef) []*firestore.DocumentRef {
	out := make([]*firestore.DocumentRef, 0, len(refs))
	for _, ref := range refs {
		if validKeys(string(ref.PaperID), string(ref.PropertyID)) {
			out = append(out, r.propertyValueDoc(ws, ref))
		}
	}
	return out
}

func (r *Repository) GetPropertyValues(ctx context.Context, ws model.WorkspaceID, refs []model.PropertyValueRef) ([]*model.PropertyValue, error) {
	if !validKeys(string(ws)) {
		return nil, nil
	}
	return getAll[model.PropertyValue](ctx, r.client, r.valueRefs(ws, refs), "property values")
}

// QueryPropertyValues reads the properties collection group, which spans
// every item of every database; DatabaseID and PropertyID narrow it to one
// range of the (DatabaseID, PropertyID, Value) index.
func (r *Repository) QueryPropertyValues(ctx context.Context, ws model.WorkspaceID, db model.PaperID, q model.PropertyValueQuery) ([]*model.PropertyValue, string, error) {
	if err := q.Validate(); err != nil {
		return nil, "", err
	}
	if !validKeys(string(ws), string(db)) {
		return nil, "", nil
	}
	dir := firestore.Asc
	if q.Desc {
		dir = firestore.Desc
	}
	query := r.client.CollectionGroup(colProperties).
		Where("DatabaseID", "==", string(db)).
		Where("PropertyID", "==", string(q.Property))
	if q.Equals != nil {
		query = query.Where("Value", "==", *q.Equals).OrderBy(firestore.DocumentID, firestore.Asc)
	} else {
		query = query.OrderBy("Value", dir).OrderBy(firestore.DocumentID, dir)
	}
	if q.Cursor != "" {
		c, err := model.ParseValueCursor(q.Cursor)
		if err != nil {
			return nil, "", err
		}
		after := r.propertyValueDoc(ws, model.PropertyValueRef{PaperID: c.Item, PropertyID: q.Property})
		if q.Equals != nil {
			query = query.StartAfter(after)
		} else {
			query = query.StartAfter(c.Value, after)
		}
	}
	out, err := all[model.PropertyValue](query.Limit(q.Limit+1).Documents(ctx), "property values")
	if err != nil {
		return nil, "", err
	}
	if len(out) > q.Limit {
		return out[:q.Limit], model.EncodeValueCursor(out[q.Limit-1]), nil
	}
	return out, "", nil
}

// --- transactions

func (t *fsTx) GetPropertyValues(refs []model.PropertyValueRef) ([]*model.PropertyValue, error) {
	var out []*model.PropertyValue
	var read []model.PropertyValueRef
	for _, ref := range refs {
		if t.valueDeletes[ref] {
			continue
		}
		if v, ok := t.values[ref]; ok {
			out = append(out, v.Clone())
			continue
		}
		read = append(read, ref)
	}
	if docs := t.r.valueRefs(t.ws, read); len(docs) > 0 {
		snaps, err := t.tx.GetAll(docs)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to get property values")
		}
		got, err := decodeSnaps[model.PropertyValue](snaps, "property value")
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

func (t *fsTx) ListItemPropertyValues(item model.PaperID) ([]*model.PropertyValue, error) {
	seen := map[model.PropertyID]*model.PropertyValue{}
	if validKeys(string(item)) {
		it := t.tx.Documents(t.r.paperDoc(t.ws, item).Collection(colProperties))
		defer it.Stop()
		for {
			snap, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, goerr.Wrap(err, "failed to list property values", goerr.V("paper_id", item))
			}
			v, err := decode[model.PropertyValue](snap, "property value")
			if err != nil {
				return nil, err
			}
			seen[v.PropertyID] = v
		}
	}
	for ref, v := range t.values {
		if ref.PaperID == item {
			seen[ref.PropertyID] = v.Clone()
		}
	}
	for ref := range t.valueDeletes {
		if ref.PaperID == item {
			delete(seen, ref.PropertyID)
		}
	}
	out := make([]*model.PropertyValue, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b *model.PropertyValue) int { return cmp.Compare(a.PropertyID, b.PropertyID) })
	return out, nil
}

func (t *fsTx) PutPropertyValue(v *model.PropertyValue) error {
	if err := v.Validate(); err != nil {
		return err
	}
	if v.WorkspaceID != t.ws || !validKeys(string(v.PaperID), string(v.PropertyID)) {
		return goerr.Wrap(model.ErrInvalidArgument, "invalid property value", goerr.V("paper_id", v.PaperID))
	}
	ref := v.Ref()
	delete(t.valueDeletes, ref)
	t.values[ref] = v.Clone()
	return nil
}

func (t *fsTx) DeletePropertyValue(item model.PaperID, property model.PropertyID) error {
	ref := model.PropertyValueRef{PaperID: item, PropertyID: property}
	delete(t.values, ref)
	t.valueDeletes[ref] = true
	return nil
}

func itemSortField(s model.ItemSort) string {
	switch s {
	case model.ItemSortTitle:
		return "Title"
	case model.ItemSortCreated:
		return "CreatedAt"
	}
	return "UpdatedAt"
}

// ListItems uses the papers indexes on (DatabaseID, Title asc / desc) and
// (DatabaseID, UpdatedAt / CreatedAt desc).
func (r *Repository) ListItems(ctx context.Context, ws model.WorkspaceID, db model.PaperID, q model.ItemQuery) ([]*model.Paper, string, error) {
	if err := q.Validate(); err != nil {
		return nil, "", err
	}
	if !validKeys(string(ws), string(db)) {
		return nil, "", nil
	}
	dir := firestore.Asc
	if q.Desc {
		dir = firestore.Desc
	}
	field := itemSortField(q.Sort)
	query := r.papersCol(ws).Where("DatabaseID", "==", string(db)).OrderBy(field, dir).OrderBy(firestore.DocumentID, dir)
	if q.Cursor != "" {
		c, err := model.ParseItemCursor(q.Cursor)
		if err != nil {
			return nil, "", err
		}
		var v any = c.At
		if q.Sort == model.ItemSortTitle {
			v = c.Title
		}
		query = query.StartAfter(v, string(c.ID))
	}
	out, err := all[model.Paper](query.Limit(q.Limit+1).Documents(ctx), "items")
	if err != nil {
		return nil, "", err
	}
	if len(out) > q.Limit {
		return out[:q.Limit], model.ItemCursorOf(q.Sort, out[q.Limit-1]), nil
	}
	return out, "", nil
}
