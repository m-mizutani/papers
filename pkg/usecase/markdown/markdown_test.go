package markdown_test

import (
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase/markdown"
)

func TestParse(t *testing.T) {
	src := "# Title\n\nHello **world**\nsecond line\n\n- a\n  - b\n1. one\n- [ ] todo\n- [x] done\n\n> quoted\n> more\n\n> [!WARNING]\n> careful\n\n```go\nfmt.Println(1)\n```\n\n```mermaid\ngraph TD; A-->B\n```\n\n---\n\n![cat](https://example.com/cat.png)\n\n| h1 | h2 |\n|----|:--:|\n| a \\| b | c |\n| d |\n\n#### deep"
	ds := markdown.Parse(src)

	want := []struct {
		typ     model.BlockType
		content string
	}{
		{model.BlockHeading, "Title"},
		{model.BlockParagraph, "Hello **world**\nsecond line"},
		{model.BlockBullet, "a"},
		{model.BlockBullet, "b"},
		{model.BlockNumbered, "one"},
		{model.BlockTodo, "todo"},
		{model.BlockTodo, "done"},
		{model.BlockQuote, "quoted\nmore"},
		{model.BlockCallout, "careful"},
		{model.BlockCode, "fmt.Println(1)"},
		{model.BlockMermaid, "graph TD; A-->B"},
		{model.BlockDivider, ""},
		{model.BlockImage, "cat"},
		{model.BlockTable, ""},
		{model.BlockHeading, "deep"},
	}
	gt.A(t, ds).Length(len(want))
	for i, w := range want {
		gt.Equal(t, ds[i].Type, w.typ)
		gt.Equal(t, ds[i].Content, w.content)
	}
	gt.Equal(t, ds[0].Props.Level, 1)
	gt.Equal(t, ds[3].Props.Indent, 1)
	gt.False(t, ds[5].Props.Checked)
	gt.True(t, ds[6].Props.Checked)
	gt.Equal(t, ds[8].Props.Icon, "⚠️")
	gt.Equal(t, ds[9].Props.Language, "go")
	gt.Equal(t, ds[12].Props.URL, "https://example.com/cat.png")
	gt.Equal(t, ds[13].Props, model.BlockProps{Rows: 3, Cols: 2, HeaderRow: true})
	gt.Equal(t, ds[13].Cells, [][]string{{"h1", "h2"}, {"a | b", "c"}, {"d", ""}})
	gt.Equal(t, ds[14].Props.Level, 3)
}

func TestParseEmpty(t *testing.T) {
	gt.A(t, markdown.Parse("")).Length(0)
	gt.A(t, markdown.Parse("\n\n  \n")).Length(0)
}

func TestParseUnterminatedFence(t *testing.T) {
	ds := markdown.Parse("```\nline")
	gt.A(t, ds).Length(1)
	gt.Equal(t, ds[0].Type, model.BlockCode)
	gt.Equal(t, ds[0].Content, "line")
}

func TestRenderRoundTrip(t *testing.T) {
	src := "## Section\n\nParagraph\n\n- a\n  - b\n- [x] done\n\n```sh\nls\n```\n\n| k | v |\n| --- | --- |\n| x | 1 |\n"
	ds := markdown.Parse(src)

	var blocks []*model.Block
	order := ""
	for i, d := range ds {
		k, err := model.KeyBetween(order, "")
		gt.NoError(t, err)
		order = k
		id := model.BlockID("b" + string(rune('a'+i)))
		blocks = append(blocks, &model.Block{ID: id, Type: d.Type, Content: d.Content, Props: d.Props, Order: k})
		if d.Type == model.BlockTable {
			for r, row := range d.Cells {
				for c, cell := range row {
					blocks = append(blocks, &model.Block{ID: model.BlockID(string(id) + "-cell"), ParentID: id, Type: model.BlockTableCell, Content: cell, Props: model.BlockProps{Row: r, Col: c}, Order: "V"})
				}
			}
		}
	}
	out := markdown.Render("Doc", blocks)
	gt.Equal(t, out, "# Doc\n\n### Section\n\nParagraph\n\n- a\n  - b\n- [x] done\n\n```sh\nls\n```\n\n| k | v |\n| --- | --- |\n| x | 1 |\n")

	// Re-parsing the output yields the same blocks (headings shift by one
	// level because the title takes the top level).
	again := markdown.Parse(out)
	gt.Equal(t, again[0].Type, model.BlockHeading)
	gt.Equal(t, again[0].Content, "Doc")
	gt.A(t, again).Length(len(ds) + 1)
}
