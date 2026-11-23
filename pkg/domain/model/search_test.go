package model_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func TestParseSearchQuery(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"incident", []string{"incident"}},
		{"  incident   report ", []string{"incident", "report"}},
		{`"incident report" owner`, []string{"incident report", "owner"}},
		{`障害対応　手順`, []string{"障害対応", "手順"}}, // ideographic space separates
		{`-draft OR title:x`, []string{"-draft", "OR", "title:x"}},
		{`"unterminated phrase`, []string{"unterminated phrase"}},
		{"tab\tseparated", []string{"tab", "separated"}},
	}
	for _, c := range cases {
		q, err := model.ParseSearchQuery(c.raw)
		gt.NoError(t, err).Required()
		gt.Equal(t, q.Terms, c.want)
	}

	for _, raw := range []string{"", "   ", `""`} {
		_, err := model.ParseSearchQuery(raw)
		gt.True(t, errors.Is(err, model.ErrInvalidArgument)).Describef("raw %q", raw)
	}
	_, err := model.ParseSearchQuery(strings.Repeat("a ", model.MaxSearchTerms+1))
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	_, err = model.ParseSearchQuery(strings.Repeat("あ", model.MaxSearchTermLength+1))
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestSearchQueryMatches(t *testing.T) {
	q := gt.R1(model.ParseSearchQuery(`Incident "on call"`)).NoError(t)
	gt.True(t, q.Matches("The incident was handled by the ON CALL engineer"))
	gt.False(t, q.Matches("The incident was handled by the call-on engineer"))
	gt.False(t, q.Matches("on call only"))
}

func TestSearchText(t *testing.T) {
	block := func(typ model.BlockType, content string) *model.Block {
		return &model.Block{Type: typ, Content: content}
	}
	gt.Equal(t, model.SearchText(block(model.BlockParagraph, "  hello **world** and [red]{.red} ")), "hello world and red")
	gt.Equal(t, model.SearchText(block(model.BlockCode, "  a**b  ")), "a**b")
	gt.Equal(t, model.SearchText(block(model.BlockPaper, "")), "")
	gt.Equal(t, model.SearchText(block(model.BlockDivider, "")), "")
	gt.Equal(t, model.SearchText(block(model.BlockTable, "")), "")
	gt.Equal(t, model.SearchText(block(model.BlockHTML,
		`<div style="color:red"><h1>Title</h1><p>Body &amp; more</p><script>var x = "hidden";</script><style>.a{}</style></div>`)),
		"Title Body & more")
	gt.Equal(t, model.SearchText(block(model.BlockSVG,
		`<svg viewBox="0 0 10 10"><path d="M0 0L10 10"/><text x="1">Load balancer</text></svg>`)),
		"Load balancer")
	gt.Equal(t, model.SearchText(block(model.BlockMermaid, "graph TD; A-->B")), "graph TD; A-->B")

	// the values of html and svg fields follow the text nodes, in name order
	withFields := block(model.BlockHTML, `<p>RTO <b data-field="rto">15m</b></p>`)
	withFields.Props.Fields = map[string]string{"rto": "10m", "owner": "  sre   team "}
	gt.Equal(t, model.SearchText(withFields), "RTO 15m sre team 10m")
	onlyFields := block(model.BlockSVG, `<svg></svg>`)
	onlyFields.Props.Fields = map[string]string{"a": "x"}
	gt.Equal(t, model.SearchText(onlyFields), "x")
}

func TestSearchEntries(t *testing.T) {
	p := &model.Paper{ID: model.NewPaperID(), WorkspaceID: "ws", Title: " Runbook "}
	e := model.NewTitleSearchEntry(p)
	gt.Equal(t, e.Text, "Runbook")
	gt.Equal(t, e.BlockID, "")
	p.Title = ""
	gt.Nil(t, model.NewTitleSearchEntry(p))

	gt.Nil(t, model.NewBlockSearchEntry(&model.Block{Type: model.BlockDivider}))

	// Long text is capped on a rune boundary.
	long := strings.Repeat("あ", model.MaxSearchTextLength)
	be := model.NewBlockSearchEntry(&model.Block{ID: model.NewBlockID(), Type: model.BlockParagraph, Content: long})
	gt.True(t, len(be.Text) <= model.MaxSearchTextLength)
	gt.True(t, utf8.ValidString(be.Text))
	gt.True(t, len(be.Text) > model.MaxSearchTextLength-4)
}

func TestHTMLText(t *testing.T) {
	got := model.HTMLText("<html><head><title>T</title><style>p{}</style></head><body><h1>Weekly</h1>\n<p>nine   incidents</p><script>var s = 'x'</script><template>t</template></body></html>")
	gt.Equal(t, got, "T Weekly nine incidents")

	long := model.HTMLText("<p>" + strings.Repeat("あ", model.MaxSearchTextLength) + "</p>")
	gt.True(t, len(long) <= model.MaxSearchTextLength)
	gt.True(t, utf8.ValidString(long))

	// the html_page block's content is its search text
	b := &model.Block{Type: model.BlockHTMLPage, Content: "Weekly nine incidents"}
	gt.Equal(t, model.SearchText(b), "Weekly nine incidents")
}
