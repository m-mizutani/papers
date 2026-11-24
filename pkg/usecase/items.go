package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

// Filter operators, by property type.
const (
	FilterIs         = "is"          // select: one of the elements
	FilterContains   = "contains"    // multi_select: any of the elements
	FilterOn         = "on"          // date
	FilterBefore     = "before"      // date, inclusive
	FilterAfter      = "after"       // date, inclusive
	FilterBetween    = "between"     // date, inclusive
	FilterStartsWith = "starts_with" // text, case-insensitive
	FilterIsEmpty    = "is_empty"
	FilterIsNotEmpty = "is_not_empty"
)

const (
	MaxFilterConditions = 5
	MaxFilterElements   = 10
	DefaultItemsLimit   = 50
	MaxItemsLimit       = 100
	// itemReadRounds caps the reads one page makes while conditions leave
	// it short.
	itemReadRounds = 5
)

// FilterCondition is one condition of an items query; all must hold.
type FilterCondition struct {
	Property model.PropertyID  `json:"property"`
	Op       string            `json:"op"`
	Elements []model.ElementID `json:"elements,omitempty"`
	Date     string            `json:"date,omitempty"`
	From     string            `json:"from,omitempty"`
	To       string            `json:"to,omitempty"`
	Text     string            `json:"text,omitempty"`
}

// ItemSortSpec orders an items query: by a property (not multi_select),
// or by title, created or updated (the last two newest first only). The
// zero value keeps the database's order (Items).
type ItemSortSpec struct {
	Property model.PropertyID
	Field    model.ItemSort
	Desc     bool
}

// ParseItemSort reads "{property id|title|created|updated}:{asc|desc}".
func ParseItemSort(s string) (ItemSortSpec, error) {
	if s == "" {
		return ItemSortSpec{}, nil
	}
	key, dir, ok := strings.Cut(s, ":")
	if !ok || (dir != "asc" && dir != "desc") {
		return ItemSortSpec{}, goerr.Wrap(model.ErrInvalidArgument, "sort is {property|title|created|updated}:{asc|desc}", goerr.V("sort", s))
	}
	out := ItemSortSpec{Desc: dir == "desc"}
	switch model.ItemSort(key) {
	case model.ItemSortTitle, model.ItemSortCreated, model.ItemSortUpdated:
		out.Field = model.ItemSort(key)
	default:
		out.Property = model.PropertyID(key)
	}
	return out, nil
}

// ParseFilter reads the JSON list of conditions.
func ParseFilter(s string) ([]FilterCondition, error) {
	if s == "" {
		return nil, nil
	}
	var out []FilterCondition
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "filter is a JSON list of conditions", goerr.V("cause", err.Error()))
	}
	return out, nil
}

// ItemsInput is one page of a database's items.
type ItemsInput struct {
	After    string
	Limit    int
	Archived bool
	Filter   []FilterCondition
	Sort     ItemSortSpec
}

// ItemRow is an item and its property values (as they show under the
// current definitions).
type ItemRow struct {
	Item   *model.Paper           `json:"item"`
	Values []*model.PropertyValue `json:"values"`
}

type ItemsPage struct {
	Items      []*ItemRow `json:"items"`
	NextCursor string     `json:"next_cursor"`
}

// itemsCursor is where a page of items ended: the phase (0: the sorted
// values or the papers query, 1: the empty values after them, in Items
// order), the last item and its position in Items, the element of a select
// sort and the store cursor. Hash ties it to the query it was made for.
type itemsCursor struct {
	Hash  string        `json:"h"`
	Phase int           `json:"p,omitempty"`
	Last  model.PaperID `json:"l,omitempty"`
	Pos   int           `json:"n,omitempty"`
	Elem  int           `json:"e,omitempty"`
	Store string        `json:"s,omitempty"`
}

func queryHash(in ItemsInput) string {
	raw, _ := json.Marshal(struct {
		F []FilterCondition
		S ItemSortSpec
		A bool
	}{in.Filter, in.Sort, in.Archived})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

func (c itemsCursor) encode() string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func parseItemsCursor(s, hash string) (itemsCursor, error) {
	var c itemsCursor
	if s == "" {
		return itemsCursor{Hash: hash}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || json.Unmarshal(raw, &c) != nil {
		return c, goerr.Wrap(model.ErrInvalidArgument, "invalid cursor")
	}
	if c.Hash != hash {
		return c, goerr.Wrap(model.ErrInvalidArgument, "the cursor belongs to another filter or sort; start from the first page")
	}
	return c, nil
}

func validateCondition(props []model.Property, c FilterCondition) error {
	prop := model.FindProperty(props, c.Property)
	if prop == nil {
		return goerr.Wrap(model.ErrInvalidArgument, "the condition names an unknown property "+string(c.Property), goerr.V("property_id", c.Property))
	}
	bad := func() error {
		return goerr.Wrap(model.ErrInvalidArgument, "the operator does not fit the property type", goerr.V("op", c.Op), goerr.V("type", prop.Type))
	}
	switch c.Op {
	case FilterIsEmpty, FilterIsNotEmpty:
		return nil
	case FilterIs, FilterContains:
		if (c.Op == FilterIs) != (prop.Type == model.PropertySelect) || !prop.Type.HasElements() {
			return bad()
		}
		if len(c.Elements) == 0 || len(c.Elements) > MaxFilterElements {
			return goerr.Wrap(model.ErrInvalidArgument, "a condition names 1 to 10 elements", goerr.V("count", len(c.Elements)))
		}
		for _, e := range c.Elements {
			if !prop.HasElement(e) {
				return goerr.Wrap(model.ErrInvalidArgument, "the condition names an unknown element "+string(e), goerr.V("element_id", e))
			}
		}
	case FilterOn, FilterBefore, FilterAfter:
		if prop.Type != model.PropertyDate {
			return bad()
		}
		if !model.ValidDate(c.Date) {
			return goerr.Wrap(model.ErrInvalidArgument, "date is YYYY-MM-DD", goerr.V("date", c.Date))
		}
	case FilterBetween:
		if prop.Type != model.PropertyDate {
			return bad()
		}
		if !model.ValidDate(c.From) || !model.ValidDate(c.To) {
			return goerr.Wrap(model.ErrInvalidArgument, "from and to are YYYY-MM-DD")
		}
	case FilterStartsWith:
		if prop.Type != model.PropertyText {
			return bad()
		}
		if c.Text == "" {
			return goerr.Wrap(model.ErrInvalidArgument, "starts_with needs text")
		}
	default:
		return goerr.Wrap(model.ErrInvalidArgument, "unknown operator", goerr.V("op", c.Op))
	}
	return nil
}

// holds reports whether an item's current value v (nil when empty)
// satisfies the condition.
func (c FilterCondition) holds(v any) bool {
	switch c.Op {
	case FilterIsEmpty:
		return v == nil
	case FilterIsNotEmpty:
		return v != nil
	}
	if v == nil {
		return false
	}
	s, _ := v.(string)
	switch c.Op {
	case FilterIs:
		return slices.Contains(c.Elements, model.ElementID(s))
	case FilterContains:
		l, _ := (&model.PropertyValue{Value: v}).Strings()
		return slices.ContainsFunc(c.Elements, func(e model.ElementID) bool { return slices.Contains(l, string(e)) })
	case FilterOn:
		return s == c.Date
	case FilterBefore:
		return s <= c.Date
	case FilterAfter:
		return s >= c.Date
	case FilterBetween:
		return c.From <= s && s <= c.To
	case FilterStartsWith:
		return strings.HasPrefix(strings.ToLower(s), strings.ToLower(c.Text))
	}
	return false
}

// itemsReader reads one page of a database's items.
type itemsReader struct {
	u     *UseCases
	ctx   context.Context
	p     *model.Principal
	db    *model.Paper
	in    ItemsInput
	limit int
	out   []*ItemRow
	seen  map[model.PaperID]bool
	// rounds counts the store reads of this page.
	rounds int
}

// candidate is an item a read produced, and where the page would continue
// if it stopped after it.
type candidate struct {
	id    model.PaperID
	after itemsCursor
}

// take reads the candidates' papers and values in two batch reads and
// keeps those that pass. It stops when the page is full and returns the
// cursor after the last one it looked at, or false when it used them all.
func (r *itemsReader) take(cands []candidate, keep func(vals map[model.PropertyID]any) bool) (itemsCursor, bool, error) {
	if len(cands) == 0 {
		return itemsCursor{}, false, nil
	}
	ids := make([]model.PaperID, len(cands))
	for i, c := range cands {
		ids[i] = c.id
	}
	papers, err := r.u.repo.GetPapers(r.ctx, r.db.WorkspaceID, ids)
	if err != nil {
		return itemsCursor{}, false, goerr.Wrap(err, "failed to read items")
	}
	byID := make(map[model.PaperID]*model.Paper, len(papers))
	for _, q := range papers {
		byID[q.ID] = q
	}
	vals, err := r.values(ids)
	if err != nil {
		return itemsCursor{}, false, err
	}
	for _, c := range cands {
		q := byID[c.id]
		ok := q != nil && q.DatabaseID == r.db.ID && !q.IsTrashed() && !q.IsBusy() &&
			(r.in.Archived || !q.IsArchived()) && !r.seen[q.ID]
		if ok {
			cur := map[model.PropertyID]any{}
			for _, v := range vals[q.ID] {
				cur[v.PropertyID] = v.Value
			}
			ok = keep(cur)
			for _, cond := range r.in.Filter {
				ok = ok && cond.holds(cur[cond.Property])
			}
		}
		if ok {
			r.seen[q.ID] = true
			r.out = append(r.out, &ItemRow{Item: q, Values: append([]*model.PropertyValue{}, vals[q.ID]...)})
			if len(r.out) >= r.limit {
				return c.after, true, nil
			}
		}
	}
	return cands[len(cands)-1].after, false, nil
}

// values reads the items' values of every defined property in one batch
// read, keeping what each shows under the current definitions.
func (r *itemsReader) values(ids []model.PaperID) (map[model.PaperID][]*model.PropertyValue, error) {
	out := map[model.PaperID][]*model.PropertyValue{}
	if len(r.db.Properties) == 0 {
		return out, nil
	}
	refs := make([]model.PropertyValueRef, 0, len(ids)*len(r.db.Properties))
	for _, id := range ids {
		for _, prop := range r.db.Properties {
			refs = append(refs, model.PropertyValueRef{PaperID: id, PropertyID: prop.ID})
		}
	}
	got, err := r.u.repo.GetPropertyValues(r.ctx, r.db.WorkspaceID, refs)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read property values")
	}
	order := map[model.PropertyID]int{}
	for i, prop := range r.db.Properties {
		order[prop.ID] = i
	}
	for _, v := range got {
		if cur := model.CurrentValue(r.db.Properties, v); cur != nil {
			c := v.Clone()
			c.Value = cur
			out[v.PaperID] = append(out[v.PaperID], c)
		}
	}
	for _, l := range out {
		slices.SortFunc(l, func(a, b *model.PropertyValue) int { return order[a.PropertyID] - order[b.PropertyID] })
	}
	return out, nil
}

// scanItems reads the database's order (Items) from the cursor, keeping
// items keep accepts, until the page is full or the rounds are used.
func (r *itemsReader) scanItems(c itemsCursor, keep func(map[model.PropertyID]any) bool) (string, error) {
	start := c.Pos
	if c.Last != "" {
		if i := slices.Index(r.db.Items, c.Last); i >= 0 {
			start = i + 1
		}
	}
	for start < len(r.db.Items) && r.rounds < itemReadRounds {
		r.rounds++
		end := min(start+r.limit, len(r.db.Items))
		cands := make([]candidate, 0, end-start)
		for i := start; i < end; i++ {
			after := c
			after.Last, after.Pos, after.Store = r.db.Items[i], i+1, ""
			cands = append(cands, candidate{id: r.db.Items[i], after: after})
		}
		at, full, err := r.take(cands, keep)
		if err != nil {
			return "", err
		}
		if full {
			if at.Pos >= len(r.db.Items) {
				return "", nil
			}
			return at.encode(), nil
		}
		start = end
	}
	if start >= len(r.db.Items) {
		return "", nil
	}
	c.Last, c.Pos, c.Store = "", start, ""
	if start > 0 {
		c.Last = r.db.Items[start-1]
	}
	return c.encode(), nil
}

// ListItems reads one page of a database's items: in the database's order,
// or sorted by a property (empty values last) or a paper field, keeping
// only those that meet every condition.
func (u *UseCases) ListItems(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, in ItemsInput) (*ItemsPage, error) {
	_, db, err := u.paperFor(ctx, p, ws, id)
	if err != nil {
		return nil, err
	}
	if !db.IsDatabase() {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "the paper is not a database", goerr.V("paper_id", id))
	}
	if in.Limit == 0 {
		in.Limit = DefaultItemsLimit
	}
	if in.Limit < 1 || in.Limit > MaxItemsLimit {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "limit out of range", goerr.V("limit", in.Limit), goerr.V("max", MaxItemsLimit))
	}
	if len(in.Filter) > MaxFilterConditions {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "too many conditions", goerr.V("max", MaxFilterConditions))
	}
	for _, c := range in.Filter {
		if err := validateCondition(db.Properties, c); err != nil {
			return nil, err
		}
	}
	var sortProp *model.Property
	switch {
	case in.Sort.Property != "":
		if sortProp = model.FindProperty(db.Properties, in.Sort.Property); sortProp == nil {
			return nil, goerr.Wrap(model.ErrInvalidArgument, "sort names an unknown property "+string(in.Sort.Property), goerr.V("property_id", in.Sort.Property))
		}
		if sortProp.Type == model.PropertyMultiSelect {
			return nil, goerr.Wrap(model.ErrInvalidArgument, "items cannot be sorted by a multi_select property")
		}
	case in.Sort.Field != "":
		if in.Sort.Field != model.ItemSortTitle && !in.Sort.Desc {
			return nil, goerr.Wrap(model.ErrInvalidArgument, "created and updated sort newest first only (desc)")
		}
	}
	hash := queryHash(in)
	c, err := parseItemsCursor(in.After, hash)
	if err != nil {
		return nil, err
	}
	r := &itemsReader{u: u, ctx: ctx, p: p, db: db, in: in, limit: in.Limit, seen: map[model.PaperID]bool{}}
	all := func(map[model.PropertyID]any) bool { return true }
	var next string
	switch {
	case sortProp != nil:
		next, err = r.byProperty(c, sortProp)
	case in.Sort.Field != "":
		next, err = r.byField(c)
	default:
		next, err = r.scanItems(c, all)
	}
	if err != nil {
		return nil, err
	}
	if r.out == nil {
		r.out = []*ItemRow{}
	}
	return &ItemsPage{Items: r.out, NextCursor: next}, nil
}

// byField reads the items in order of a paper field.
func (r *itemsReader) byField(c itemsCursor) (string, error) {
	store := c.Store
	for r.rounds < itemReadRounds {
		r.rounds++
		papers, next, err := r.u.repo.ListItems(r.ctx, r.db.WorkspaceID, r.db.ID, model.ItemQuery{Sort: r.in.Sort.Field, Desc: r.in.Sort.Desc, Limit: r.limit, Cursor: store})
		if err != nil {
			return "", goerr.Wrap(err, "failed to read items")
		}
		cands := make([]candidate, len(papers))
		for i, q := range papers {
			after := c
			after.Store = model.ItemCursorOf(r.in.Sort.Field, q)
			cands[i] = candidate{id: q.ID, after: after}
		}
		at, full, err := r.take(cands, func(map[model.PropertyID]any) bool { return true })
		if err != nil {
			return "", err
		}
		if full {
			if next == "" && at.Store == cands[len(cands)-1].after.Store {
				return "", nil
			}
			return at.encode(), nil
		}
		if next == "" {
			return "", nil
		}
		store = next
	}
	c.Store = store
	return c.encode(), nil
}

// byProperty reads the values of the sort property in order (a select by
// its elements' order, one element at a time), then the items whose value
// is empty in the database's order.
func (r *itemsReader) byProperty(c itemsCursor, prop *model.Property) (string, error) {
	if c.Phase == 0 {
		elems := []model.Element{{}}
		if prop.Type == model.PropertySelect {
			elems = slices.Clone(prop.Elements)
			if r.in.Sort.Desc {
				slices.Reverse(elems)
			}
		}
		for c.Elem < len(elems) {
			if r.rounds >= itemReadRounds {
				return c.encode(), nil
			}
			r.rounds++
			q := model.PropertyValueQuery{Property: prop.ID, Desc: r.in.Sort.Desc, Limit: r.limit, Cursor: c.Store}
			if prop.Type == model.PropertySelect {
				eq := string(elems[c.Elem].ID)
				q.Equals, q.Desc = &eq, false
			}
			vals, next, err := r.u.repo.QueryPropertyValues(r.ctx, r.db.WorkspaceID, r.db.ID, q)
			if err != nil {
				return "", goerr.Wrap(err, "failed to read sorted values")
			}
			cands := make([]candidate, len(vals))
			for i, v := range vals {
				after := c
				after.Store = model.EncodeValueCursor(v)
				if i == len(vals)-1 && next == "" {
					after.Elem, after.Store = c.Elem+1, ""
				}
				cands[i] = candidate{id: v.PaperID, after: after}
			}
			at, full, err := r.take(cands, func(cur map[model.PropertyID]any) bool { return cur[prop.ID] != nil })
			if err != nil {
				return "", err
			}
			if full {
				if at.Elem >= len(elems) {
					at = itemsCursor{Hash: c.Hash, Phase: 1}
				}
				return at.encode(), nil
			}
			if next == "" {
				c.Elem, c.Store = c.Elem+1, ""
			} else {
				c.Store = next
			}
		}
		c = itemsCursor{Hash: c.Hash, Phase: 1}
	}
	return r.scanItems(c, func(cur map[model.PropertyID]any) bool { return cur[prop.ID] == nil })
}
