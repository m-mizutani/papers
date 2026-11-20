package model_test

import (
	"strings"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func validBlock() *model.Block {
	actor := model.Actor{Kind: model.ActorHuman, UserID: "u1"}
	return &model.Block{
		ID: "b1", WorkspaceID: "ws", PaperID: "p", Order: "V",
		Type: model.BlockParagraph, Content: "hello", Version: 1,
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
		gt.Error(t, b.Validate())
		b.ParentID = "table"
		gt.NoError(t, b.Validate())
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
		b.Props.Rows, b.Props.Cols = 2, 3
		gt.NoError(t, b.Validate())
	})
	t.Run("content size limit", func(t *testing.T) {
		b := validBlock()
		b.Content = strings.Repeat("a", model.MaxContentLength+1)
		gt.Error(t, b.Validate())
	})
	t.Run("version must be positive", func(t *testing.T) {
		b := validBlock()
		b.Version = 0
		gt.Error(t, b.Validate())
	})
}

func TestBlockPropsNormalize(t *testing.T) {
	p := model.BlockProps{Level: 2, Indent: 3, Checked: true, Language: "go", URL: "x"}
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
}
