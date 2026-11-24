package model

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/m-mizutani/goerr/v2"
)

type (
	// PropertyID identifies a property within its database.
	PropertyID string
	// ElementID identifies an element (a choice of a select or
	// multi-select property) within its database.
	ElementID string
)

func NewPropertyID() PropertyID { return PropertyID(newID()) }
func NewElementID() ElementID   { return ElementID(newID()) }

// PropertyType is the type of a property.
type PropertyType string

const (
	PropertyText        PropertyType = "text"
	PropertyDate        PropertyType = "date"
	PropertySelect      PropertyType = "select"
	PropertyMultiSelect PropertyType = "multi_select"
)

func (t PropertyType) Validate() error {
	switch t {
	case PropertyText, PropertyDate, PropertySelect, PropertyMultiSelect:
		return nil
	}
	return goerr.Wrap(ErrInvalidArgument, "property type must be text, date, select or multi_select", goerr.V("type", t))
}

// HasElements reports whether properties of the type choose from elements.
func (t PropertyType) HasElements() bool { return t == PropertySelect || t == PropertyMultiSelect }

// Element is a choice of a select or multi-select property. Values of the
// property hold only its ID; Value is the text people see.
type Element struct {
	ID    ElementID `json:"id"`
	Value string    `json:"value"`
	Color string    `json:"color"`
}

// Property is one column of a database.
type Property struct {
	ID       PropertyID   `json:"id"`
	Name     string       `json:"name"`
	Type     PropertyType `json:"type"`
	Elements []Element    `json:"elements,omitempty"`
}

const (
	MaxProperties          = 100
	MaxElementsPerProperty = 100
	MaxElementsPerDatabase = 1000
	MaxDatabaseItems       = 10000
	MaxNameLength          = 100
	MaxTextValueLength     = 2000
	MaxMultiSelectValues   = 100
	DefaultElementColor    = "gray"
)

// ElementColors are the palette names an element may use (D10).
var ElementColors = []string{"gray", "brown", "orange", "yellow", "green", "blue", "purple", "pink", "red"}

// ValidateName checks a property name or an element value: 1 to
// MaxNameLength bytes on one line, compared without surrounding spaces.
func ValidateName(name, what string) error {
	if strings.TrimSpace(name) != name || name == "" {
		return goerr.Wrap(ErrInvalidArgument, what+" must not be empty or start or end with spaces", goerr.V("name", name))
	}
	if len(name) > MaxNameLength {
		return goerr.Wrap(ErrInvalidArgument, what+" is too long", goerr.V("length", len(name)), goerr.V("max", MaxNameLength))
	}
	if strings.ContainsAny(name, "\n\r") || !utf8.ValidString(name) {
		return goerr.Wrap(ErrInvalidArgument, what+" must be one line", goerr.V("name", name))
	}
	return nil
}

// ValidateProperties checks a database's property definitions.
func ValidateProperties(props []Property) error {
	if len(props) > MaxProperties {
		return goerr.Wrap(ErrInvalidArgument, "the database has too many properties", goerr.V("count", len(props)), goerr.V("max", MaxProperties))
	}
	propIDs := map[PropertyID]bool{}
	names := map[string]bool{}
	elementIDs := map[ElementID]bool{}
	for _, p := range props {
		if !ValidID(string(p.ID)) || propIDs[p.ID] {
			return goerr.Wrap(ErrInvalidArgument, "invalid or duplicate property id", goerr.V("property_id", p.ID))
		}
		propIDs[p.ID] = true
		if err := ValidateName(p.Name, "property name"); err != nil {
			return err
		}
		if names[p.Name] {
			return goerr.Wrap(ErrInvalidArgument, "another property has the same name", goerr.V("name", p.Name))
		}
		names[p.Name] = true
		if err := p.Type.Validate(); err != nil {
			return err
		}
		if !p.Type.HasElements() && len(p.Elements) > 0 {
			return goerr.Wrap(ErrInvalidArgument, "only select and multi_select properties have elements", goerr.V("property_id", p.ID))
		}
		if len(p.Elements) > MaxElementsPerProperty {
			return goerr.Wrap(ErrInvalidArgument, "the property has too many elements", goerr.V("property_id", p.ID), goerr.V("max", MaxElementsPerProperty))
		}
		values := map[string]bool{}
		for _, e := range p.Elements {
			if !ValidID(string(e.ID)) || elementIDs[e.ID] {
				return goerr.Wrap(ErrInvalidArgument, "invalid or duplicate element id", goerr.V("element_id", e.ID))
			}
			elementIDs[e.ID] = true
			if err := ValidateName(e.Value, "element value"); err != nil {
				return err
			}
			if values[e.Value] {
				return goerr.Wrap(ErrInvalidArgument, "another element of the property has the same value", goerr.V("value", e.Value))
			}
			values[e.Value] = true
			if !slices.Contains(ElementColors, e.Color) {
				return goerr.Wrap(ErrInvalidArgument, "unknown element color", goerr.V("color", e.Color))
			}
		}
	}
	if len(elementIDs) > MaxElementsPerDatabase {
		return goerr.Wrap(ErrInvalidArgument, "the database has too many elements", goerr.V("count", len(elementIDs)), goerr.V("max", MaxElementsPerDatabase))
	}
	return nil
}

// CloneProperties deep-copies property definitions.
func CloneProperties(props []Property) []Property {
	if props == nil {
		return nil
	}
	out := make([]Property, len(props))
	for i, p := range props {
		out[i] = p
		out[i].Elements = slices.Clone(p.Elements)
	}
	return out
}

// FindProperty returns the property with the ID, or nil.
func FindProperty(props []Property, id PropertyID) *Property {
	for i := range props {
		if props[i].ID == id {
			return &props[i]
		}
	}
	return nil
}

// HasElement reports whether the property has the element.
func (p *Property) HasElement(id ElementID) bool {
	return slices.ContainsFunc(p.Elements, func(e Element) bool { return e.ID == id })
}

// ElementIndex is the position of the element in the property, or -1.
func (p *Property) ElementIndex(id ElementID) int {
	return slices.IndexFunc(p.Elements, func(e Element) bool { return e.ID == id })
}

// PropertyValue is what one item holds for one property. It is stored at
// papers/{item}/properties/{property}; DatabaseID and PropertyID repeat
// what the path says so that the sort query can filter on them.
type PropertyValue struct {
	WorkspaceID WorkspaceID `json:"workspace_id"`
	DatabaseID  PaperID     `json:"database_id"`
	PaperID     PaperID     `json:"paper_id"`
	PropertyID  PropertyID  `json:"property_id"`
	// Value is a string (text, date, select: an element ID) or a list of
	// element IDs (multi_select). Decoding may yield []any; use Strings.
	Value     any       `json:"value"`
	UpdatedBy Actor     `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PropertyValueRef names a property value: an item and a property.
type PropertyValueRef struct {
	PaperID    PaperID
	PropertyID PropertyID
}

func (v *PropertyValue) Ref() PropertyValueRef {
	return PropertyValueRef{PaperID: v.PaperID, PropertyID: v.PropertyID}
}

// Str returns the value when it is a string.
func (v *PropertyValue) Str() (string, bool) {
	s, ok := v.Value.(string)
	return s, ok
}

// Strings returns the value when it is a list of strings.
func (v *PropertyValue) Strings() ([]string, bool) { return asStrings(v.Value) }

func asStrings(raw any) ([]string, bool) {
	switch x := raw.(type) {
	case []string:
		return slices.Clone(x), true
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}

func (v *PropertyValue) Clone() *PropertyValue {
	c := *v
	if l, ok := v.Strings(); ok {
		c.Value = l
	}
	return &c
}

// Validate checks the form of the value; whether the property and its
// elements exist is the use case's concern.
func (v *PropertyValue) Validate() error {
	if !ValidID(string(v.PaperID)) || !ValidID(string(v.DatabaseID)) || !ValidID(string(v.PropertyID)) || v.WorkspaceID == "" {
		return goerr.Wrap(ErrInvalidArgument, "property value needs workspace, database, item and property", goerr.V("paper_id", v.PaperID), goerr.V("property_id", v.PropertyID))
	}
	if s, ok := v.Str(); ok {
		if s == "" || len(s) > MaxTextValueLength || strings.ContainsAny(s, "\n\r") || !utf8.ValidString(s) {
			return goerr.Wrap(ErrInvalidArgument, "invalid property value", goerr.V("property_id", v.PropertyID))
		}
	} else if l, ok := v.Strings(); ok {
		if len(l) == 0 || len(l) > MaxMultiSelectValues {
			return goerr.Wrap(ErrInvalidArgument, "invalid property value", goerr.V("property_id", v.PropertyID))
		}
		for _, e := range l {
			if !ValidID(e) {
				return goerr.Wrap(ErrInvalidArgument, "invalid element id in property value", goerr.V("element_id", e))
			}
		}
	} else {
		return goerr.Wrap(ErrInvalidArgument, "property value must be a string or a list of strings", goerr.V("property_id", v.PropertyID))
	}
	if err := v.UpdatedBy.Validate(); err != nil {
		return goerr.Wrap(err, "invalid property value updater")
	}
	return nil
}

// ValidDate reports whether s is an existing date written YYYY-MM-DD.
func ValidDate(s string) bool {
	if len(s) != len("2006-01-02") {
		return false
	}
	t, err := time.Parse("2006-01-02", s)
	return err == nil && t.Format("2006-01-02") == s
}

// NormalizeValue checks raw as a value of prop and returns its stored
// form: a string, or the sorted, de-duplicated element IDs. An empty value
// ("", an empty list, nil) returns nil: the value is cleared.
func NormalizeValue(prop *Property, raw any) (any, error) {
	if raw == nil {
		return nil, nil
	}
	bad := func(why string) error {
		return goerr.Wrap(ErrInvalidArgument, why+" (property "+string(prop.ID)+")", goerr.V("property_id", prop.ID), goerr.V("property", prop.Name))
	}
	if prop.Type == PropertyMultiSelect {
		l, ok := asStrings(raw)
		if !ok {
			if s, isStr := raw.(string); isStr && s == "" {
				return nil, nil
			}
			return nil, bad("a multi_select value is a list of element ids")
		}
		if len(l) == 0 {
			return nil, nil
		}
		slices.Sort(l)
		l = slices.Compact(l)
		if len(l) > MaxMultiSelectValues {
			return nil, bad("too many elements in a multi_select value")
		}
		for _, e := range l {
			if !prop.HasElement(ElementID(e)) {
				return nil, goerr.Wrap(ErrInvalidArgument, "unknown element "+e+" of property "+string(prop.ID), goerr.V("property_id", prop.ID), goerr.V("element_id", e))
			}
		}
		return l, nil
	}
	s, ok := raw.(string)
	if !ok {
		return nil, bad("this property's value is a string")
	}
	if s == "" {
		return nil, nil
	}
	switch prop.Type {
	case PropertyText:
		if len(s) > MaxTextValueLength || strings.ContainsAny(s, "\n\r") || !utf8.ValidString(s) {
			return nil, bad("a text value is one line of at most 2000 bytes")
		}
	case PropertyDate:
		if !ValidDate(s) {
			return nil, bad("a date value is an existing date written YYYY-MM-DD")
		}
	case PropertySelect:
		if !prop.HasElement(ElementID(s)) {
			return nil, goerr.Wrap(ErrInvalidArgument, "unknown element "+s+" of property "+string(prop.ID), goerr.V("property_id", prop.ID), goerr.V("element_id", s))
		}
	}
	return s, nil
}

// CurrentValue is what a stored value shows under the current property
// definitions: nil when its property is gone or nothing of it remains (an
// element deleted since). Values of deleted properties and elements are
// left in storage and dropped when the item is written next.
func CurrentValue(props []Property, v *PropertyValue) any {
	prop := FindProperty(props, v.PropertyID)
	if prop == nil {
		return nil
	}
	switch prop.Type {
	case PropertyMultiSelect:
		l, ok := v.Strings()
		if !ok {
			return nil
		}
		var keep []string
		for _, e := range l {
			if prop.HasElement(ElementID(e)) {
				keep = append(keep, e)
			}
		}
		if len(keep) == 0 {
			return nil
		}
		return keep
	case PropertySelect:
		s, ok := v.Str()
		if !ok || !prop.HasElement(ElementID(s)) {
			return nil
		}
		return s
	case PropertyDate:
		s, ok := v.Str()
		if !ok || !ValidDate(s) {
			return nil
		}
		return s
	default:
		s, ok := v.Str()
		if !ok {
			return nil
		}
		return s
	}
}

// VersionValue is a property value kept in a version of an item.
type VersionValue struct {
	PropertyID PropertyID `json:"property_id"`
	Value      any        `json:"value"`
}

// MaxPropertyValueQueryLimit bounds one read of property values.
const MaxPropertyValueQueryLimit = 500

// PropertyValueQuery reads the values of one property of a database in
// Value order, then by item ID (both descending when Desc), or only those
// whose Value equals *Equals (by item ID). Cursor continues after the value
// a previous read ended with.
type PropertyValueQuery struct {
	Property PropertyID
	Desc     bool
	Equals   *string
	Limit    int
	Cursor   string
}

// ValueCursor is a position in a read of property values: the value and
// the item of the last one read.
type ValueCursor struct {
	Value string  `json:"v"`
	Item  PaperID `json:"i"`
}

// EncodeValueCursor makes the cursor that continues after v.
func EncodeValueCursor(v *PropertyValue) string {
	s, _ := v.Str()
	raw, _ := json.Marshal(ValueCursor{Value: s, Item: v.PaperID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// ParseValueCursor reads a cursor made by EncodeValueCursor.
func ParseValueCursor(cursor string) (ValueCursor, error) {
	var c ValueCursor
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || json.Unmarshal(raw, &c) != nil || !ValidID(string(c.Item)) {
		return ValueCursor{}, goerr.Wrap(ErrInvalidArgument, "invalid cursor", goerr.V("cursor", cursor))
	}
	return c, nil
}

// Validate checks the query's form.
func (q PropertyValueQuery) Validate() error {
	if !ValidID(string(q.Property)) {
		return goerr.Wrap(ErrInvalidArgument, "invalid property id", goerr.V("property_id", q.Property))
	}
	if q.Limit < 1 || q.Limit > MaxPropertyValueQueryLimit {
		return goerr.Wrap(ErrInvalidArgument, "limit out of range", goerr.V("limit", q.Limit))
	}
	if q.Cursor != "" {
		if _, err := ParseValueCursor(q.Cursor); err != nil {
			return err
		}
	}
	return nil
}

// ItemSort is a paper field a database's items can be read in order of.
type ItemSort string

const (
	ItemSortTitle   ItemSort = "title"
	ItemSortCreated ItemSort = "created"
	ItemSortUpdated ItemSort = "updated"
)

// ItemQuery reads a database's items (papers whose ParentID is the
// database) in order of Sort, then by ID, both descending when Desc.
// Created and updated are read newest first only.
type ItemQuery struct {
	Sort   ItemSort
	Desc   bool
	Limit  int
	Cursor string
}

// ItemCursor is a position in an ItemQuery read.
type ItemCursor struct {
	Title string    `json:"s,omitempty"`
	At    time.Time `json:"t,omitempty"`
	ID    PaperID   `json:"i"`
}

func (q ItemQuery) Validate() error {
	switch q.Sort {
	case ItemSortTitle:
	case ItemSortCreated, ItemSortUpdated:
		if !q.Desc {
			return goerr.Wrap(ErrInvalidArgument, "created and updated are read newest first only")
		}
	default:
		return goerr.Wrap(ErrInvalidArgument, "unknown item sort", goerr.V("sort", q.Sort))
	}
	if q.Limit < 1 || q.Limit > MaxPropertyValueQueryLimit {
		return goerr.Wrap(ErrInvalidArgument, "limit out of range", goerr.V("limit", q.Limit))
	}
	if q.Cursor != "" {
		if _, err := ParseItemCursor(q.Cursor); err != nil {
			return err
		}
	}
	return nil
}

// ItemCursorOf is the cursor that continues a read sorted by s after p.
func ItemCursorOf(s ItemSort, p *Paper) string {
	c := ItemCursor{ID: p.ID}
	switch s {
	case ItemSortTitle:
		c.Title = p.Title
	case ItemSortCreated:
		c.At = p.CreatedAt
	default:
		c.At = p.UpdatedAt
	}
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func ParseItemCursor(cursor string) (ItemCursor, error) {
	var c ItemCursor
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || json.Unmarshal(raw, &c) != nil || !ValidID(string(c.ID)) {
		return ItemCursor{}, goerr.Wrap(ErrInvalidArgument, "invalid cursor", goerr.V("cursor", cursor))
	}
	return c, nil
}
