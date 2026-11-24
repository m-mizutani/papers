package model_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func selectProp(values ...string) model.Property {
	p := model.Property{ID: model.NewPropertyID(), Name: "Status", Type: model.PropertySelect}
	for _, v := range values {
		p.Elements = append(p.Elements, model.Element{ID: model.NewElementID(), Value: v, Color: "gray"})
	}
	return p
}

func invalid(t *testing.T, err error) {
	t.Helper()
	gt.True(t, errors.Is(err, model.ErrInvalidArgument)).Describef("want invalid argument, got %v", err)
}

func TestValidateProperties(t *testing.T) {
	// T6: the four types validate; unknown types, elements on the wrong
	// type and bad ids do not.
	good := []model.Property{
		{ID: model.NewPropertyID(), Name: "Memo", Type: model.PropertyText},
		{ID: model.NewPropertyID(), Name: "Due", Type: model.PropertyDate},
		selectProp("Todo", "Doing"),
		{ID: model.NewPropertyID(), Name: "Tags", Type: model.PropertyMultiSelect, Elements: []model.Element{{ID: model.NewElementID(), Value: "a", Color: "red"}}},
	}
	gt.NoError(t, model.ValidateProperties(good))

	invalid(t, model.ValidateProperties([]model.Property{{ID: model.NewPropertyID(), Name: "X", Type: "number"}}))
	text := model.Property{ID: model.NewPropertyID(), Name: "Memo", Type: model.PropertyText, Elements: []model.Element{{ID: model.NewElementID(), Value: "a", Color: "gray"}}}
	invalid(t, model.ValidateProperties([]model.Property{text}))
	invalid(t, model.ValidateProperties([]model.Property{{ID: "bad", Name: "X", Type: model.PropertyText}}))
	dup := good[0]
	dup.Name = "Other"
	invalid(t, model.ValidateProperties([]model.Property{good[0], dup}))
	s := selectProp("a")
	s.Elements = append(s.Elements, model.Element{ID: s.Elements[0].ID, Value: "b", Color: "gray"})
	invalid(t, model.ValidateProperties([]model.Property{s}))
	s = selectProp("a")
	s.Elements[0].Color = "teal"
	invalid(t, model.ValidateProperties([]model.Property{s}))
}

func TestPropertyLimits(t *testing.T) {
	// T7: names are unique, 1 to 100 bytes on one line; 100 properties and
	// 100 elements per property (1,000 per database) at most.
	a := model.Property{ID: model.NewPropertyID(), Name: "Same", Type: model.PropertyText}
	b := model.Property{ID: model.NewPropertyID(), Name: "Same", Type: model.PropertyDate}
	invalid(t, model.ValidateProperties([]model.Property{a, b}))
	invalid(t, model.ValidateProperties([]model.Property{selectProp("x", "x")}))
	for _, name := range []string{"", " lead", "two\nlines", strings.Repeat("a", 101)} {
		invalid(t, model.ValidateName(name, "property name"))
	}
	gt.NoError(t, model.ValidateName(strings.Repeat("a", 100), "property name"))

	many := func(n int) []model.Property {
		out := make([]model.Property, n)
		for i := range out {
			out[i] = model.Property{ID: model.NewPropertyID(), Name: "p" + strings.Repeat("x", i%50) + string(rune('A'+i/50)), Type: model.PropertyText}
		}
		return out
	}
	gt.NoError(t, model.ValidateProperties(many(100)))
	invalid(t, model.ValidateProperties(many(101)))

	values := func(n int, prefix string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = prefix + strings.Repeat("v", i%60) + string(rune('A'+i/60))
		}
		return out
	}
	gt.NoError(t, model.ValidateProperties([]model.Property{selectProp(values(100, "")...)}))
	invalid(t, model.ValidateProperties([]model.Property{selectProp(values(101, "")...)}))
	var ten []model.Property
	for i := range 10 {
		p := selectProp(values(100, "")...)
		p.Name = "p" + string(rune('A'+i))
		ten = append(ten, p)
	}
	gt.NoError(t, model.ValidateProperties(ten))
	extra := selectProp("one more")
	extra.Name = "extra"
	invalid(t, model.ValidateProperties(append(ten, extra)))
}

func TestNormalizeValue(t *testing.T) {
	// T12, T13
	status := selectProp("Todo", "Doing")
	tags := model.Property{ID: model.NewPropertyID(), Name: "Tags", Type: model.PropertyMultiSelect, Elements: []model.Element{
		{ID: model.NewElementID(), Value: "a", Color: "gray"}, {ID: model.NewElementID(), Value: "b", Color: "gray"},
	}}
	due := model.Property{ID: model.NewPropertyID(), Name: "Due", Type: model.PropertyDate}
	memo := model.Property{ID: model.NewPropertyID(), Name: "Memo", Type: model.PropertyText}

	v := gt.R1(model.NormalizeValue(&status, string(status.Elements[1].ID))).NoError(t)
	gt.Equal(t, v, any(string(status.Elements[1].ID)))
	ea, eb := string(tags.Elements[0].ID), string(tags.Elements[1].ID)
	l := gt.R1(model.NormalizeValue(&tags, []any{eb, ea, eb})).NoError(t)
	want := []string{ea, eb}
	if want[0] > want[1] {
		want[0], want[1] = want[1], want[0]
	}
	gt.Equal(t, l, any(want))
	for _, empty := range []any{nil, "", []any{}, []string{}} {
		p := &memo
		if _, isList := empty.([]any); isList {
			p = &tags
		}
		if _, isList := empty.([]string); isList {
			p = &tags
		}
		gt.Nil(t, gt.R1(model.NormalizeValue(p, empty)).NoError(t))
	}

	_, err := model.NormalizeValue(&status, string(model.NewElementID()))
	invalid(t, err)
	_, err = model.NormalizeValue(&tags, []any{string(model.NewElementID())})
	invalid(t, err)
	_, err = model.NormalizeValue(&due, "2026-02-30")
	invalid(t, err)
	_, err = model.NormalizeValue(&due, "x")
	invalid(t, err)
	gt.R1(model.NormalizeValue(&due, "2028-02-29")).NoError(t)
	_, err = model.NormalizeValue(&status, []any{string(status.Elements[0].ID)})
	invalid(t, err)
	_, err = model.NormalizeValue(&memo, 12)
	invalid(t, err)
}

func TestValueLimits(t *testing.T) {
	// T48: a text value is at most 2,000 bytes on one line; a multi_select
	// at most 100 elements.
	memo := model.Property{ID: model.NewPropertyID(), Name: "Memo", Type: model.PropertyText}
	gt.R1(model.NormalizeValue(&memo, strings.Repeat("a", 2000))).NoError(t)
	_, err := model.NormalizeValue(&memo, strings.Repeat("a", 2001))
	invalid(t, err)
	_, err = model.NormalizeValue(&memo, "a\nb")
	invalid(t, err)

	tags := model.Property{ID: model.NewPropertyID(), Name: "Tags", Type: model.PropertyMultiSelect}
	var ids []any
	for range 101 {
		e := model.Element{ID: model.NewElementID(), Value: string(model.NewElementID()), Color: "gray"}
		tags.Elements = append(tags.Elements, e)
		ids = append(ids, string(e.ID))
	}
	gt.R1(model.NormalizeValue(&tags, ids[:100])).NoError(t)
	_, err = model.NormalizeValue(&tags, ids)
	invalid(t, err)
}

func TestCurrentValue(t *testing.T) {
	status := selectProp("Todo", "Doing")
	tags := model.Property{ID: model.NewPropertyID(), Name: "Tags", Type: model.PropertyMultiSelect, Elements: []model.Element{{ID: model.NewElementID(), Value: "a", Color: "gray"}}}
	props := []model.Property{status, tags}
	gone := string(model.NewElementID())
	gt.Nil(t, model.CurrentValue(props, &model.PropertyValue{PropertyID: status.ID, Value: gone}))
	gt.Equal(t, model.CurrentValue(props, &model.PropertyValue{PropertyID: status.ID, Value: string(status.Elements[0].ID)}), any(string(status.Elements[0].ID)))
	gt.Equal(t, model.CurrentValue(props, &model.PropertyValue{PropertyID: tags.ID, Value: []any{gone, string(tags.Elements[0].ID)}}), any([]string{string(tags.Elements[0].ID)}))
	gt.Nil(t, model.CurrentValue(props, &model.PropertyValue{PropertyID: tags.ID, Value: []string{gone}}))
	gt.Nil(t, model.CurrentValue(props, &model.PropertyValue{PropertyID: model.NewPropertyID(), Value: "x"}))
}

func TestDatabaseAndItem(t *testing.T) {
	actor := model.Actor{Kind: model.ActorHuman, UserID: model.NewUserID(), Name: "a"}
	base := func() *model.Paper {
		return &model.Paper{ID: model.NewPaperID(), WorkspaceID: "ws", Visibility: model.VisibilityPublic, CreatedBy: actor, UpdatedBy: actor}
	}
	db := base()
	db.Kind = model.PaperKindDatabase
	db.Properties = []model.Property{selectProp("a")}
	db.Items = []model.PaperID{model.NewPaperID()}
	gt.NoError(t, db.Validate())
	c := db.Clone()
	c.Properties[0].Elements[0].Value = "changed"
	c.Items[0] = model.NewPaperID()
	gt.Equal(t, db.Properties[0].Elements[0].Value, "a")
	gt.NotEqual(t, db.Items[0], c.Items[0])

	private := db.Clone()
	private.Visibility = model.VisibilityPrivate
	invalid(t, private.Validate())
	dupItems := db.Clone()
	dupItems.Items = append(dupItems.Items, dupItems.Items[0])
	invalid(t, dupItems.Validate())
	tooMany := db.Clone()
	tooMany.Items = make([]model.PaperID, model.MaxDatabaseItems+1)
	for i := range tooMany.Items {
		tooMany.Items[i] = model.NewPaperID()
	}
	invalid(t, tooMany.Validate())

	plain := base()
	plain.Properties = db.Properties
	invalid(t, plain.Validate())
	item := base()
	item.DatabaseID = db.ID
	gt.NoError(t, item.Validate())
	gt.True(t, item.IsItem())
	gt.Equal(t, item.Above(), db.ID)
	gt.False(t, item.IsUnfiled())
	html := item.Clone()
	html.Kind = model.PaperKindHTML // an item may have any kind of body
	gt.NoError(t, html.Validate())
	parented := item.Clone()
	parented.ParentID = model.NewPaperID() // an item has no parent but its database
	invalid(t, parented.Validate())
	privateItem := item.Clone()
	privateItem.Visibility = model.VisibilityPrivate
	invalid(t, privateItem.Validate())
	unknown := base()
	unknown.Kind = "row"
	invalid(t, unknown.Validate())
}

func TestPropertyValueValidate(t *testing.T) {
	actor := model.Actor{Kind: model.ActorHuman, UserID: model.NewUserID(), Name: "a"}
	v := &model.PropertyValue{WorkspaceID: "ws", DatabaseID: model.NewPaperID(), PaperID: model.NewPaperID(), PropertyID: model.NewPropertyID(), Value: "x", UpdatedBy: actor, UpdatedAt: time.Now()}
	gt.NoError(t, v.Validate())
	for _, bad := range []any{"", 3, []string{}, []string{"nope"}, nil} {
		c := *v
		c.Value = bad
		invalid(t, c.Validate())
	}
	c := *v
	c.Value = []any{string(model.NewElementID())}
	gt.NoError(t, c.Validate())
}

func TestValidDate(t *testing.T) {
	gt.True(t, model.ValidDate("2026-12-10"))
	gt.True(t, model.ValidDate("2028-02-29"))
	gt.False(t, model.ValidDate("2027-02-29"))
	gt.False(t, model.ValidDate("2026-1-1"))
}
