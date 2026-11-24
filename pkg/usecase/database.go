package usecase

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

// Operations of a database batch (POST …/papers/{database}/ops).
const (
	OpAddProperty    OpKind = "add_property"
	OpUpdateProperty OpKind = "update_property"
	OpMoveProperty   OpKind = "move_property"
	OpDeleteProperty OpKind = "delete_property"
	OpAddElement     OpKind = "add_element"
	OpUpdateElement  OpKind = "update_element"
	OpMoveElement    OpKind = "move_element"
	OpDeleteElement  OpKind = "delete_element"
	OpInsertItem     OpKind = "insert_item"
	OpSetValues      OpKind = "set_values"
	OpMoveItem       OpKind = "move_item"
)

// MaxValuesPerBatch caps the property values one batch writes.
const MaxValuesPerBatch = 1000

func (k OpKind) databaseOp() bool {
	switch k {
	case OpAddProperty, OpUpdateProperty, OpMoveProperty, OpDeleteProperty,
		OpAddElement, OpUpdateElement, OpMoveElement, OpDeleteElement,
		OpInsertItem, OpSetValues, OpMoveItem:
		return true
	}
	return false
}

// paperUpdatedEvents are the feed entries for a written paper record: the
// paper's own paper_updated and, for an item, an item_put on its
// database's feed so that an open table sees the change. Callers set At.
func paperUpdatedEvents(q *model.Paper) []*model.Event {
	evs := []*model.Event{{Type: model.EventPaperUpdated, WorkspaceID: q.WorkspaceID, PaperID: q.ID, Paper: q.Clone()}}
	if q.IsItem() {
		evs = append(evs, &model.Event{Type: model.EventItemPut, WorkspaceID: q.WorkspaceID, PaperID: q.DatabaseID, Paper: q.Clone()})
	}
	return evs
}

// valueEvents are the feed entries for a written or removed property
// value, on both the database's and the item's feed.
func valueEvents(ws model.WorkspaceID, db, item model.PaperID, prop model.PropertyID, v *model.PropertyValue) []*model.Event {
	out := make([]*model.Event, 0, 2)
	for _, feed := range []model.PaperID{db, item} {
		if v != nil {
			out = append(out, &model.Event{Type: model.EventPropertyValuePut, WorkspaceID: ws, PaperID: feed, PropertyValue: v.Clone()})
		} else {
			out = append(out, &model.Event{Type: model.EventPropertyValueDeleted, WorkspaceID: ws, PaperID: feed, ItemID: item, PropertyID: prop})
		}
	}
	return out
}

// sameValue compares two stored property values.
func sameValue(a, b any) bool {
	if as, ok := a.(string); ok {
		bs, ok := b.(string)
		return ok && as == bs
	}
	al, aok := (&model.PropertyValue{Value: a}).Strings()
	bl, bok := (&model.PropertyValue{Value: b}).Strings()
	return aok && bok && slices.Equal(al, bl)
}

// valueWriter writes property values of the items of one database within
// a transaction, and drops values whose property or elements are gone.
type valueWriter struct {
	tx    interfaces.Tx
	db    *model.Paper
	actor model.Actor
	now   time.Time
	// cleaned are the items whose stale values have been dropped.
	cleaned map[model.PaperID]bool
	written int
	events  []*model.Event
	values  []*model.PropertyValue
	// unbounded lifts the per-batch limit (version restore).
	unbounded bool
}

func newValueWriter(tx interfaces.Tx, db *model.Paper, actor model.Actor, now time.Time) *valueWriter {
	return &valueWriter{tx: tx, db: db, actor: actor, now: now, cleaned: map[model.PaperID]bool{}}
}

func (w *valueWriter) count() error {
	w.written++
	if !w.unbounded && w.written > MaxValuesPerBatch {
		return goerr.Wrap(model.ErrInvalidArgument, "the batch writes too many property values; split it", goerr.V("max", MaxValuesPerBatch))
	}
	return nil
}

// clean drops the item's values that show nothing under the current
// definitions (a deleted property, or only deleted elements), once per
// item, and returns the item's current values by property.
func (w *valueWriter) clean(item model.PaperID) (map[model.PropertyID]*model.PropertyValue, error) {
	vals, err := w.tx.ListItemPropertyValues(item)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to list property values", goerr.V("paper_id", item))
	}
	out := map[model.PropertyID]*model.PropertyValue{}
	for _, v := range vals {
		if model.CurrentValue(w.db.Properties, v) == nil {
			if !w.cleaned[item] {
				if err := w.remove(item, v.PropertyID); err != nil {
					return nil, err
				}
			}
			continue
		}
		out[v.PropertyID] = v
	}
	w.cleaned[item] = true
	return out, nil
}

func (w *valueWriter) remove(item model.PaperID, prop model.PropertyID) error {
	if err := w.count(); err != nil {
		return err
	}
	if err := w.tx.DeletePropertyValue(item, prop); err != nil {
		return goerr.Wrap(err, "failed to delete property value")
	}
	w.events = append(w.events, valueEvents(w.db.WorkspaceID, w.db.ID, item, prop, nil)...)
	return nil
}

// set writes value (already normalized; nil clears) for the item and
// property, unless it is what is stored.
func (w *valueWriter) set(item model.PaperID, prop model.PropertyID, value any, current map[model.PropertyID]*model.PropertyValue) error {
	cur := current[prop]
	if value == nil {
		if cur == nil {
			return nil
		}
		delete(current, prop)
		return w.remove(item, prop)
	}
	if cur != nil && sameValue(cur.Value, value) {
		return nil
	}
	if err := w.count(); err != nil {
		return err
	}
	v := &model.PropertyValue{
		WorkspaceID: w.db.WorkspaceID, DatabaseID: w.db.ID, PaperID: item, PropertyID: prop,
		Value: value, UpdatedBy: w.actor, UpdatedAt: w.now,
	}
	if err := w.tx.PutPropertyValue(v); err != nil {
		return goerr.Wrap(err, "failed to write property value", goerr.V("property_id", prop))
	}
	current[prop] = v
	w.values = append(w.values, v)
	w.events = append(w.events, valueEvents(w.db.WorkspaceID, w.db.ID, item, prop, v)...)
	return nil
}

// dbApplier applies a database batch: changes to the property definitions,
// new items, item order and property values, in one transaction.
type dbApplier struct {
	u      *UseCases
	ctx    context.Context
	tx     interfaces.Tx
	db     *model.Paper
	actor  model.Actor
	now    time.Time
	refs   map[string]string
	values *valueWriter

	dbChanged bool
	// items caches the items the batch reads; created are the new ones.
	items   map[model.PaperID]*model.Paper
	created []*model.Paper
	// current caches each touched item's stored values.
	current map[model.PaperID]map[model.PropertyID]*model.PropertyValue
	// fetched holds the papers the ops name, read in one round trip before
	// the ops apply (nil for an ID that does not exist).
	fetched map[model.PaperID]*model.Paper
}

func (a *dbApplier) resolve(id string) (string, error) {
	if name, ok := strings.CutPrefix(id, RefPrefix); ok {
		v, ok := a.refs[name]
		if !ok {
			return "", goerr.Wrap(model.ErrInvalidArgument, "unknown ref", goerr.V("ref", name))
		}
		return v, nil
	}
	return id, nil
}

func (a *dbApplier) addRef(name, id string) error {
	if name == "" {
		return nil
	}
	if strings.HasPrefix(name, RefPrefix) || len(name) > 64 {
		return goerr.Wrap(model.ErrInvalidArgument, "invalid ref", goerr.V("ref", name))
	}
	if _, dup := a.refs[name]; dup {
		return goerr.Wrap(model.ErrInvalidArgument, "ref is used twice", goerr.V("ref", name))
	}
	a.refs[name] = id
	return nil
}

// place puts x into list after the entry named by after (nil: at the end,
// "": at the top), removing x from where it was.
func place[T any](a *dbApplier, list []T, x T, idOf func(T) string, after *string) ([]T, error) {
	self := idOf(x)
	list = slices.DeleteFunc(slices.Clone(list), func(e T) bool { return idOf(e) == self })
	if after == nil {
		return append(list, x), nil
	}
	target, err := a.resolve(*after)
	if err != nil {
		return nil, err
	}
	if target == "" {
		return slices.Insert(list, 0, x), nil
	}
	if target == self {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "cannot place something after itself")
	}
	i := slices.IndexFunc(list, func(e T) bool { return idOf(e) == target })
	if i < 0 {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "the after anchor is not in the list", goerr.V("after", *after))
	}
	return slices.Insert(list, i+1, x), nil
}

func propID(p model.Property) string { return string(p.ID) }
func elemID(e model.Element) string  { return string(e.ID) }
func itemID(id model.PaperID) string { return string(id) }

func (a *dbApplier) property(ref string) (*model.Property, error) {
	id, err := a.resolve(ref)
	if err != nil {
		return nil, err
	}
	prop := model.FindProperty(a.db.Properties, model.PropertyID(id))
	if prop == nil {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "unknown property "+id, goerr.V("property_id", id))
	}
	return prop, nil
}

func (a *dbApplier) element(prop *model.Property, ref string) (int, error) {
	id, err := a.resolve(ref)
	if err != nil {
		return -1, err
	}
	i := prop.ElementIndex(model.ElementID(id))
	if i < 0 {
		return -1, goerr.Wrap(model.ErrInvalidArgument, "unknown element "+id+" of property "+string(prop.ID), goerr.V("property_id", prop.ID), goerr.V("element_id", id))
	}
	return i, nil
}

func trimmedName(s *string, what string) (string, error) {
	if s == nil {
		return "", goerr.Wrap(model.ErrInvalidArgument, what+" is required")
	}
	name := strings.TrimSpace(*s)
	if err := model.ValidateName(name, what); err != nil {
		return "", err
	}
	return name, nil
}

func (a *dbApplier) addProperty(op Op) error {
	name, err := trimmedName(op.Name, "property name")
	if err != nil {
		return err
	}
	if err := op.PropertyType.Validate(); err != nil {
		return err
	}
	prop := model.Property{ID: model.NewPropertyID(), Name: name, Type: op.PropertyType}
	if a.db.Properties, err = place(a, a.db.Properties, prop, propID, op.After); err != nil {
		return err
	}
	if err := a.addRef(op.Ref, string(prop.ID)); err != nil {
		return err
	}
	a.dbChanged = true
	return nil
}

func (a *dbApplier) updateProperty(op Op) error {
	prop, err := a.property(op.Property)
	if err != nil {
		return err
	}
	name, err := trimmedName(op.Name, "property name")
	if err != nil {
		return err
	}
	if prop.Name != name {
		prop.Name, a.dbChanged = name, true
	}
	return nil
}

func (a *dbApplier) moveProperty(op Op) error {
	prop, err := a.property(op.Property)
	if err != nil {
		return err
	}
	moved, err := place(a, a.db.Properties, *prop, propID, op.After)
	if err != nil {
		return err
	}
	a.db.Properties, a.dbChanged = moved, true
	return nil
}

// deleteProperty removes the definition only; the values stay in storage,
// show nothing and are dropped when each item is written next.
func (a *dbApplier) deleteProperty(op Op) error {
	prop, err := a.property(op.Property)
	if err != nil {
		return err
	}
	id := prop.ID
	a.db.Properties = slices.DeleteFunc(a.db.Properties, func(p model.Property) bool { return p.ID == id })
	a.dbChanged = true
	return nil
}

func elementColor(c *string) (string, error) {
	if c == nil || *c == "" {
		return model.DefaultElementColor, nil
	}
	if !slices.Contains(model.ElementColors, *c) {
		return "", goerr.Wrap(model.ErrInvalidArgument, "unknown element color", goerr.V("color", *c))
	}
	return *c, nil
}

func (a *dbApplier) addElement(op Op) error {
	prop, err := a.property(op.Property)
	if err != nil {
		return err
	}
	if !prop.Type.HasElements() {
		return goerr.Wrap(model.ErrInvalidArgument, "only select and multi_select properties have elements", goerr.V("property_id", prop.ID))
	}
	value, err := trimmedName(op.Value, "element value")
	if err != nil {
		return err
	}
	color, err := elementColor(op.Color)
	if err != nil {
		return err
	}
	e := model.Element{ID: model.NewElementID(), Value: value, Color: color}
	if prop.Elements, err = place(a, prop.Elements, e, elemID, op.After); err != nil {
		return err
	}
	if err := a.addRef(op.Ref, string(e.ID)); err != nil {
		return err
	}
	a.dbChanged = true
	return nil
}

func (a *dbApplier) updateElement(op Op) error {
	prop, err := a.property(op.Property)
	if err != nil {
		return err
	}
	i, err := a.element(prop, op.Element)
	if err != nil {
		return err
	}
	e := &prop.Elements[i]
	if op.Value != nil {
		value, err := trimmedName(op.Value, "element value")
		if err != nil {
			return err
		}
		if e.Value != value {
			e.Value, a.dbChanged = value, true
		}
	}
	if op.Color != nil {
		color, err := elementColor(op.Color)
		if err != nil {
			return err
		}
		if e.Color != color {
			e.Color, a.dbChanged = color, true
		}
	}
	return nil
}

func (a *dbApplier) moveElement(op Op) error {
	prop, err := a.property(op.Property)
	if err != nil {
		return err
	}
	i, err := a.element(prop, op.Element)
	if err != nil {
		return err
	}
	moved, err := place(a, prop.Elements, prop.Elements[i], elemID, op.After)
	if err != nil {
		return err
	}
	prop.Elements, a.dbChanged = moved, true
	return nil
}

// deleteElement removes the element only; values that chose it show it no
// longer and are cleaned when each item is written next.
func (a *dbApplier) deleteElement(op Op) error {
	prop, err := a.property(op.Property)
	if err != nil {
		return err
	}
	i, err := a.element(prop, op.Element)
	if err != nil {
		return err
	}
	prop.Elements, a.dbChanged = slices.Delete(prop.Elements, i, i+1), true
	return nil
}

// prefetch reads every existing item the ops name in one round trip, so
// a batch over many items does not read them one by one.
func (a *dbApplier) prefetch(ops []Op) error {
	var ids []model.PaperID
	seen := map[model.PaperID]bool{}
	for _, op := range ops {
		id := model.PaperID(op.Item)
		if op.Item == "" || strings.HasPrefix(op.Item, RefPrefix) || !model.ValidID(op.Item) || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	got, err := a.tx.GetPapers(ids)
	if err != nil {
		return goerr.Wrap(err, "failed to get items", goerr.V("count", len(ids)))
	}
	a.fetched = make(map[model.PaperID]*model.Paper, len(ids))
	for _, id := range ids {
		a.fetched[id] = nil
	}
	for _, q := range got {
		a.fetched[q.ID] = q
	}
	return nil
}

// item returns an item of this database the batch may write.
func (a *dbApplier) item(ref string) (*model.Paper, error) {
	id, err := a.resolve(ref)
	if err != nil {
		return nil, err
	}
	if q, ok := a.items[model.PaperID(id)]; ok {
		return q, nil
	}
	if !model.ValidID(id) {
		return nil, notFound("item", id)
	}
	var q *model.Paper
	if got, ok := a.fetched[model.PaperID(id)]; ok {
		q = got
		if q == nil {
			err = model.ErrNotFound
		}
	} else {
		q, err = a.tx.GetPaper(model.PaperID(id))
	}
	if errors.Is(err, model.ErrNotFound) || (err == nil && q.DatabaseID != a.db.ID) {
		return nil, notFound("item", id)
	}
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get item", goerr.V("paper_id", id))
	}
	if q.IsTrashed() {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "the item is in the trash; restore it first", goerr.V("paper_id", id))
	}
	if q.IsBusy() {
		return nil, busy(q)
	}
	a.items[q.ID] = q
	return q, nil
}

func (a *dbApplier) insertItem(op Op) error {
	if len(a.db.Items) >= model.MaxDatabaseItems {
		return goerr.Wrap(model.ErrInvalidArgument, "the database has too many items", goerr.V("max", model.MaxDatabaseItems))
	}
	if len(a.created) >= MaxBlocksPerBatch {
		return goerr.Wrap(model.ErrInvalidArgument, "the batch creates too many items; split it", goerr.V("max", MaxBlocksPerBatch))
	}
	title := ""
	if op.Title != nil {
		title = strings.TrimSpace(*op.Title)
	}
	if err := model.ValidateTitle(title); err != nil {
		return err
	}
	q := &model.Paper{
		ID: model.NewPaperID(), WorkspaceID: a.db.WorkspaceID, Kind: model.PaperKindBlocks, Title: title, DatabaseID: a.db.ID,
		Visibility: model.VisibilityPublic,
		CreatedBy:  a.actor, CreatedAt: a.now, UpdatedBy: a.actor, UpdatedAt: a.now,
	}
	items, err := place(a, a.db.Items, q.ID, itemID, op.After)
	if err != nil {
		return err
	}
	if err := a.addRef(op.Ref, string(q.ID)); err != nil {
		return err
	}
	if err := a.tx.PutPaper(q); err != nil {
		return goerr.Wrap(err, "failed to create item")
	}
	a.db.Items, a.dbChanged = items, true
	a.items[q.ID] = q
	a.current[q.ID] = map[model.PropertyID]*model.PropertyValue{}
	a.values.cleaned[q.ID] = true
	a.created = append(a.created, q)
	return a.setValues(q, op.Set)
}

// value turns one entry of set into a stored value of prop, resolving refs
// of elements made earlier in the batch.
func (a *dbApplier) value(prop *model.Property, raw any) (any, error) {
	switch x := raw.(type) {
	case string:
		if prop.Type == model.PropertySelect {
			id, err := a.resolve(x)
			if err != nil {
				return nil, err
			}
			raw = id
		}
	case []any, []string:
		l, ok := (&model.PropertyValue{Value: x}).Strings()
		if !ok {
			return nil, goerr.Wrap(model.ErrInvalidArgument, "a multi_select value is a list of element ids", goerr.V("property_id", prop.ID))
		}
		for i, e := range l {
			id, err := a.resolve(e)
			if err != nil {
				return nil, err
			}
			l[i] = id
		}
		raw = l
	}
	return model.NormalizeValue(prop, raw)
}

func (a *dbApplier) setValues(q *model.Paper, set map[string]any) error {
	if len(set) == 0 {
		return nil
	}
	cur, ok := a.current[q.ID]
	if !ok {
		var err error
		if cur, err = a.values.clean(q.ID); err != nil {
			return err
		}
		a.current[q.ID] = cur
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		prop, err := a.property(k)
		if err != nil {
			return err
		}
		v, err := a.value(prop, set[k])
		if err != nil {
			return err
		}
		if err := a.values.set(q.ID, prop.ID, v, cur); err != nil {
			return err
		}
	}
	return nil
}

func (a *dbApplier) apply(op Op) error {
	switch op.Kind {
	case OpAddProperty:
		return a.addProperty(op)
	case OpUpdateProperty:
		return a.updateProperty(op)
	case OpMoveProperty:
		return a.moveProperty(op)
	case OpDeleteProperty:
		return a.deleteProperty(op)
	case OpAddElement:
		return a.addElement(op)
	case OpUpdateElement:
		return a.updateElement(op)
	case OpMoveElement:
		return a.moveElement(op)
	case OpDeleteElement:
		return a.deleteElement(op)
	case OpInsertItem:
		return a.insertItem(op)
	case OpSetValues:
		q, err := a.item(op.Item)
		if err != nil {
			return err
		}
		if len(op.Set) == 0 {
			return goerr.Wrap(model.ErrInvalidArgument, "set_values requires set")
		}
		return a.setValues(q, op.Set)
	case OpMoveItem:
		q, err := a.item(op.Item)
		if err != nil {
			return err
		}
		if !slices.Contains(a.db.Items, q.ID) {
			return goerr.Wrap(model.ErrInvalidArgument, "the item is not in the database's order", goerr.V("paper_id", q.ID))
		}
		items, err := place(a, a.db.Items, q.ID, itemID, op.After)
		if err != nil {
			return err
		}
		if !slices.Equal(items, a.db.Items) {
			a.db.Items, a.dbChanged = items, true
		}
		return nil
	case OpSetTitle:
		if op.Title == nil {
			return goerr.Wrap(model.ErrInvalidArgument, "set_title requires title")
		}
		title := strings.TrimSpace(*op.Title)
		if err := model.ValidateTitle(title); err != nil {
			return err
		}
		if title != a.db.Title {
			a.db.Title, a.dbChanged = title, true
		}
		return nil
	}
	return goerr.Wrap(model.ErrInvalidArgument, "a database has no body; this operation is not possible on it", goerr.V("op", op.Kind))
}

func (a *dbApplier) finish() (*BatchResult, error) {
	var evs []*model.Event
	if a.dbChanged {
		if err := model.ValidateProperties(a.db.Properties); err != nil {
			return nil, err
		}
		a.db.UpdatedBy, a.db.UpdatedAt = a.actor, a.now
		if err := a.tx.PutPaper(a.db); err != nil {
			return nil, goerr.Wrap(err, "failed to update database")
		}
		evs = append(evs, paperUpdatedEvents(a.db)...)
	}
	for _, q := range a.created {
		evs = append(evs, paperUpdatedEvents(q)...)
	}
	evs = append(evs, a.values.events...)
	for _, ev := range evs {
		ev.At = a.now
		if err := a.tx.AddEvent(ev); err != nil {
			return nil, goerr.Wrap(err, "failed to add event")
		}
	}
	return &BatchResult{Paper: a.db, Refs: a.refs, Items: a.created, Values: a.values.values}, nil
}

// applyDatabase runs a database batch within the transaction of Apply.
// applyDatabase applies a database's batch as actor (people, agents, or
// the system writing the sample papers).
func (u *UseCases) applyDatabase(ctx context.Context, tx interfaces.Tx, actor model.Actor, db *model.Paper, ops []Op, now time.Time) (*BatchResult, error) {
	a := &dbApplier{
		u: u, ctx: ctx, tx: tx, db: db, actor: actor, now: now, refs: map[string]string{},
		items: map[model.PaperID]*model.Paper{}, current: map[model.PaperID]map[model.PropertyID]*model.PropertyValue{},
	}
	a.values = newValueWriter(tx, db, a.actor, now)
	if err := a.prefetch(ops); err != nil {
		return nil, err
	}
	for i, op := range ops {
		if err := a.apply(op); err != nil {
			return nil, goerr.Wrap(err, fmt.Sprintf("op #%d (%s) failed", i, op.Kind), goerr.V("index", i))
		}
	}
	return a.finish()
}

// itemVersionValues are an item's property values as a version keeps them:
// only what the current definitions still show. It also returns those
// definitions, to compare a saved version's values under them.
func itemVersionValues(tx interfaces.Tx, item *model.Paper) ([]model.VersionValue, []model.Property, error) {
	db, err := tx.GetPaper(item.DatabaseID)
	if err != nil {
		return nil, nil, goerr.Wrap(err, "failed to get the item's database", goerr.V("database_id", item.DatabaseID))
	}
	vals, err := tx.ListItemPropertyValues(item.ID)
	if err != nil {
		return nil, nil, goerr.Wrap(err, "failed to list property values", goerr.V("paper_id", item.ID))
	}
	var out []model.VersionValue
	for _, v := range vals {
		if cur := model.CurrentValue(db.Properties, v); cur != nil {
			out = append(out, model.VersionValue{PropertyID: v.PropertyID, Value: cur})
		}
	}
	return out, db.Properties, nil
}

// sameVersionValues tells whether a saved version's values read the same as
// the current ones. The saved values are read under the current definitions
// too, so deleting a property or an element changes no item by itself.
func sameVersionValues(props []model.Property, saved, current []model.VersionValue) bool {
	var shown []model.VersionValue
	for _, vv := range saved {
		if cur := model.CurrentValue(props, &model.PropertyValue{PropertyID: vv.PropertyID, Value: vv.Value}); cur != nil {
			shown = append(shown, model.VersionValue{PropertyID: vv.PropertyID, Value: cur})
		}
	}
	return slices.EqualFunc(shown, current, func(x, y model.VersionValue) bool {
		return x.PropertyID == y.PropertyID && sameValue(x.Value, y.Value)
	})
}

// restoreValues sets an item's property values to those of a version.
// Values whose property or elements no longer exist are dropped quietly:
// the version is the past, the definitions are the present.
func restoreValues(tx interfaces.Tx, item *model.Paper, want []model.VersionValue, actor model.Actor, now time.Time) (*valueWriter, error) {
	db, err := tx.GetPaper(item.DatabaseID)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get the item's database", goerr.V("database_id", item.DatabaseID))
	}
	w := newValueWriter(tx, db, actor, now)
	w.unbounded = true
	cur, err := w.clean(item.ID)
	if err != nil {
		return nil, err
	}
	target := map[model.PropertyID]any{}
	for _, vv := range want {
		if v := model.CurrentValue(db.Properties, &model.PropertyValue{PropertyID: vv.PropertyID, Value: vv.Value}); v != nil {
			target[vv.PropertyID] = v
		}
	}
	for _, prop := range db.Properties {
		if err := w.set(item.ID, prop.ID, target[prop.ID], cur); err != nil {
			return nil, err
		}
	}
	return w, nil
}
