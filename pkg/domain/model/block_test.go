package model_test

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func validBlock() *model.Block {
	actor := model.Actor{Kind: model.ActorHuman, UserID: "u1"}
	return &model.Block{
		ID: model.NewBlockID(), WorkspaceID: "ws", PaperID: "p", Order: "V",
		Type: model.BlockParagraph, Content: "hello", Rev: 1,
		CreatedBy: actor, UpdatedBy: actor,
	}
}

func TestBlockValidate(t *testing.T) {
	t.Run("valid paragraph", func(t *testing.T) {
		gt.NoError(t, validBlock().Validate())
	})
	t.Run("unknown type", func(t *testing.T) {
		b := validBlock()
		b.Type = "video"
		gt.Error(t, b.Validate())
	})
	t.Run("invalid order", func(t *testing.T) {
		b := validBlock()
		b.Order = ""
		gt.Error(t, b.Validate())
	})
	t.Run("heading requires level", func(t *testing.T) {
		b := validBlock()
		b.Type = model.BlockHeading
		gt.Error(t, b.Validate())
		b.Props.Level = 2
		gt.NoError(t, b.Validate())
		b.Props.Level = 4
		gt.Error(t, b.Validate())
	})
	t.Run("divider has no content", func(t *testing.T) {
		b := validBlock()
		b.Type = model.BlockDivider
		gt.Error(t, b.Validate())
		b.Content = ""
		gt.NoError(t, b.Validate())
	})
	t.Run("table cell needs parent", func(t *testing.T) {
		b := validBlock()
		b.Type = model.BlockTableCell
		b.Props = model.BlockProps{RowID: model.NewLineID(), ColID: model.NewLineID()}
		gt.Error(t, b.Validate())
		b.ParentID = model.NewBlockID()
		gt.NoError(t, b.Validate())
		b.Props.RowID = ""
		gt.Error(t, b.Validate())
	})
	t.Run("top-level type rejects parent", func(t *testing.T) {
		b := validBlock()
		b.ParentID = "x"
		gt.Error(t, b.Validate())
	})
	t.Run("table shape", func(t *testing.T) {
		b := validBlock()
		b.Type = model.BlockTable
		b.Content = ""
		gt.Error(t, b.Validate())
		line := func() model.TableLine { return model.TableLine{ID: model.NewLineID(), Order: "V"} }
		b.Props.Rows = []model.TableLine{line(), line()}
		b.Props.Cols = []model.TableLine{line(), line(), line()}
		gt.NoError(t, b.Validate())
		b.Props.Cols[1].ID = b.Props.Cols[0].ID
		gt.Error(t, b.Validate())
	})
	t.Run("content size limit", func(t *testing.T) {
		b := validBlock()
		b.Content = strings.Repeat("a", model.MaxContentLength+1)
		gt.Error(t, b.Validate())
	})
	t.Run("rev must be positive", func(t *testing.T) {
		b := validBlock()
		b.Rev = 0
		gt.Error(t, b.Validate())
	})
	t.Run("paper block needs a paper id", func(t *testing.T) {
		b := validBlock()
		b.Type, b.Content = model.BlockPaper, ""
		gt.Error(t, b.Validate())
		b.Props.PaperID = model.NewPaperID()
		gt.NoError(t, b.Validate())
		b.Content = "x"
		gt.Error(t, b.Validate())
	})
	t.Run("image uses an attachment or a url", func(t *testing.T) {
		b := validBlock()
		b.Type = model.BlockImage
		b.Props = model.BlockProps{URL: "https://example.com/a.png", AttachmentID: model.NewAttachmentID()}
		gt.Error(t, b.Validate())
		b.Props.URL = ""
		gt.NoError(t, b.Validate())
	})
}

func TestBlockPropsNormalize(t *testing.T) {
	p := model.BlockProps{Level: 2, Indent: 3, Checked: true, Language: "go", URL: "x", PaperID: "p"}
	gt.True(t, p.Normalize(model.BlockPaper).Equal(model.BlockProps{PaperID: "p"}))
	gt.True(t, p.Normalize(model.BlockImage).Equal(model.BlockProps{URL: "x"}))
	gt.Equal(t, p.Normalize(model.BlockHeading), model.BlockProps{Level: 2})
	gt.Equal(t, p.Normalize(model.BlockTodo), model.BlockProps{Indent: 3, Checked: true})
	gt.Equal(t, p.Normalize(model.BlockCode), model.BlockProps{Language: "go"})
	gt.Equal(t, p.Normalize(model.BlockParagraph), model.BlockProps{})
}

func TestBlockTypeTraits(t *testing.T) {
	for _, bt := range model.BlockTypes() {
		gt.NoError(t, bt.Validate())
	}
	gt.True(t, model.BlockParagraph.HumanEditable())
	gt.False(t, model.BlockHTML.HumanEditable())
	gt.False(t, model.BlockMermaid.HumanEditable())
	gt.True(t, model.BlockTable.IsContainer())
	gt.Equal(t, model.BlockTableCell.RequiredParent(), model.BlockTable)
	gt.True(t, model.BlockParagraph.UsesMarkup())
	gt.False(t, model.BlockCode.UsesMarkup())
	gt.True(t, model.BlockPaper.HumanEditable())
}

func TestBlockPropsFieldsNormalize(t *testing.T) {
	// T10: fields stay on html and svg and are dropped elsewhere
	p := model.BlockProps{Height: 300, Fields: map[string]string{"rto": "15m", "rpo": "5m"}}
	for _, bt := range []model.BlockType{model.BlockHTML, model.BlockSVG} {
		n := p.Normalize(bt)
		gt.Equal(t, n.Fields, p.Fields)
		gt.True(t, n.Equal(p))
		gt.True(t, bt.HasFields())
	}
	for _, bt := range model.BlockTypes() {
		if bt.HasFields() {
			continue
		}
		gt.Nil(t, p.Normalize(bt).Fields)
	}
	// a copy: changing the result leaves the input alone
	n := p.Normalize(model.BlockHTML)
	n.Fields["rto"] = "10m"
	gt.Equal(t, p.Fields["rto"], "15m")
	// empty fields are no fields
	gt.Nil(t, model.BlockProps{Fields: map[string]string{}}.Normalize(model.BlockHTML).Fields)
	// a changed value is a change of contents
	gt.False(t, n.Equal(p))
	gt.True(t, model.BlockProps{}.Equal(model.BlockProps{Fields: map[string]string{}}))

	b := validBlock()
	b.Type, b.Content, b.Props = model.BlockHTML, "<p data-field=\"rto\">15m</p>", p.Normalize(model.BlockHTML)
	c := b.Clone()
	c.Props.Fields["rto"] = "1m"
	gt.Equal(t, b.Props.Fields["rto"], "15m")
	gt.False(t, model.SameContents(b, c))
}

func TestBlockPropsFieldsValidate(t *testing.T) {
	// T11: bad names, too many fields, long values and line breaks
	html := func(fields map[string]string) *model.Block {
		b := validBlock()
		b.Type, b.Content, b.Props = model.BlockSVG, "<svg></svg>", model.BlockProps{Fields: fields}
		return b
	}
	gt.NoError(t, html(map[string]string{"rto": "15m", "a_1": "", "z" + strings.Repeat("9", 31): "x"}).Validate())
	many := map[string]string{}
	for i := range model.MaxFields {
		many["f"+strconv.Itoa(i)] = "v"
	}
	gt.NoError(t, html(many).Validate())
	gt.NoError(t, html(map[string]string{"v": strings.Repeat("x", model.MaxFieldValueLength)}).Validate())
	gt.NoError(t, html(map[string]string{"v": strings.Repeat("あ", model.MaxFieldValueLength/3)}).Validate())

	many["f32"] = "v"
	bad := []map[string]string{
		{"Bad-Key": "x"},
		{"1abc": "x"},
		{"": "x"},
		{"a" + strings.Repeat("b", 32): "x"},
		many,
		{"v": strings.Repeat("x", model.MaxFieldValueLength+1)},
		{"v": "two\nlines"},
		{"v": "carriage\rreturn"},
		{"v": "\xff"},
	}
	for _, f := range bad {
		err := html(f).Validate()
		gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	}
}

func TestHTMLPageBlock(t *testing.T) { // T37
	b := validBlock()
	b.Type = model.BlockHTMLPage
	b.Props = model.BlockProps{AttachmentID: model.NewAttachmentID()}
	gt.NoError(t, b.Validate())
	gt.False(t, model.BlockHTMLPage.HumanEditable())

	b.Props.AttachmentID = ""
	gt.True(t, errors.Is(b.Validate(), model.ErrInvalidArgument))
	b.Props.AttachmentID = "nope"
	gt.True(t, errors.Is(b.Validate(), model.ErrInvalidArgument))

	att := model.NewAttachmentID()
	n := model.BlockProps{AttachmentID: att, Height: 30, URL: "https://x", Fields: map[string]string{"a": "b"}, Level: 2}.Normalize(model.BlockHTMLPage)
	gt.Equal(t, n, model.BlockProps{AttachmentID: att})
}

func TestBlockPropsDatabasesNormalize(t *testing.T) {
	// T1: databases stay on html and html_page, are dropped elsewhere, and
	// order and write are part of the contents
	links := []model.DatabaseLink{{ID: model.NewPaperID(), Write: true}, {ID: model.NewPaperID()}}
	p := model.BlockProps{Height: 200, AttachmentID: model.NewAttachmentID(), Databases: links}
	for _, bt := range []model.BlockType{model.BlockHTML, model.BlockHTMLPage} {
		gt.True(t, bt.HasDatabases())
		gt.Equal(t, p.Normalize(bt).Databases, links)
	}
	for _, bt := range model.BlockTypes() {
		if bt.HasDatabases() {
			continue
		}
		gt.Nil(t, p.Normalize(bt).Databases)
	}
	gt.Nil(t, model.BlockProps{Databases: []model.DatabaseLink{}}.Normalize(model.BlockHTML).Databases)

	n := p.Normalize(model.BlockHTML)
	n.Databases[0].Write = false
	gt.True(t, p.Databases[0].Write)
	gt.False(t, n.Equal(p.Normalize(model.BlockHTML)))
	swapped := p.Normalize(model.BlockHTML)
	swapped.Databases = []model.DatabaseLink{links[1], links[0]}
	gt.False(t, swapped.Equal(p.Normalize(model.BlockHTML)))

	b := validBlock()
	b.Type, b.Content, b.Props = model.BlockHTML, "<p>x</p>", p.Normalize(model.BlockHTML)
	c := b.Clone()
	c.Props.Databases[0].Write = false
	gt.True(t, b.Props.Databases[0].Write)
	gt.False(t, model.SameContents(b, c))
}

func TestBlockPropsDatabasesValidate(t *testing.T) {
	// T2: at most ten, each once, valid ids
	block := func(bt model.BlockType, links []model.DatabaseLink) *model.Block {
		b := validBlock()
		b.Type, b.Content, b.Props = bt, "<p>x</p>", model.BlockProps{Databases: links}
		if bt == model.BlockHTMLPage {
			b.Props.AttachmentID = model.NewAttachmentID()
		}
		return b
	}
	ten := make([]model.DatabaseLink, model.MaxDatabaseLinks)
	for i := range ten {
		ten[i] = model.DatabaseLink{ID: model.NewPaperID(), Write: i%2 == 0}
	}
	for _, bt := range []model.BlockType{model.BlockHTML, model.BlockHTMLPage} {
		gt.NoError(t, block(bt, ten).Validate())
		gt.NoError(t, block(bt, nil).Validate())
		eleven := append(slices.Clone(ten), model.DatabaseLink{ID: model.NewPaperID()})
		gt.True(t, errors.Is(block(bt, eleven).Validate(), model.ErrInvalidArgument))
		twice := []model.DatabaseLink{ten[0], {ID: ten[0].ID, Write: !ten[0].Write}}
		gt.True(t, errors.Is(block(bt, twice).Validate(), model.ErrInvalidArgument))
		gt.True(t, errors.Is(block(bt, []model.DatabaseLink{{ID: "not a valid id"}}).Validate(), model.ErrInvalidArgument))
	}
}
