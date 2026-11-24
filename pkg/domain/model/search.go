package model

import (
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/m-mizutani/goerr/v2"
	"github.com/m-mizutani/papers/pkg/domain/markup"
	"golang.org/x/net/html"
)

// SearchEntry is the searchable form of a paper title or of one block. The
// repository keeps one entry per paper title and per block with searchable
// text, and updates them together with the paper and its blocks.
type SearchEntry struct {
	WorkspaceID WorkspaceID `json:"workspace_id"`
	PaperID     PaperID     `json:"paper_id"`
	// BlockID is empty for the entry of the paper title.
	BlockID   BlockID   `json:"block_id,omitempty"`
	BlockType BlockType `json:"block_type,omitempty"`
	// Text is plain text: markup of rich blocks is stripped and the length
	// is capped at MaxSearchTextLength bytes.
	Text      string    `json:"text"`
	UpdatedAt time.Time `json:"updated_at"`
}

const (
	// MaxSearchTextLength bounds the text indexed per entry, so that a large
	// rich block does not dominate index size or result payloads.
	MaxSearchTextLength = 8 * 1024
	MaxSearchTerms      = 8
	MaxSearchTermLength = 64
	MaxSearchLimit      = 100
)

// NewTitleSearchEntry returns the entry for a paper's title, or nil if the
// title is empty.
func NewTitleSearchEntry(p *Paper) *SearchEntry {
	text := capText(strings.TrimSpace(p.Title))
	if text == "" {
		return nil
	}
	return &SearchEntry{WorkspaceID: p.WorkspaceID, PaperID: p.ID, Text: text, UpdatedAt: p.UpdatedAt}
}

// NewBlockSearchEntry returns the entry for a block, or nil if the block
// has no searchable text.
func NewBlockSearchEntry(b *Block) *SearchEntry {
	text := capText(SearchText(b))
	if text == "" {
		return nil
	}
	return &SearchEntry{
		WorkspaceID: b.WorkspaceID,
		PaperID:     b.PaperID,
		BlockID:     b.ID,
		BlockType:   b.Type,
		Text:        text,
		UpdatedAt:   b.UpdatedAt,
	}
}

// SearchText returns the plain text of a block that search matches against.
func SearchText(b *Block) string {
	switch {
	case b.Type == BlockDivider || b.Type == BlockTable || b.Type == BlockPaper:
		return ""
	case b.Type.HasFields():
		return strings.TrimSpace(markupText(b.Content) + " " + fieldText(b.Props.Fields))
	case b.Type.UsesMarkup():
		// index only what readers see: [always]{.red} is "always"
		return strings.TrimSpace(markup.PlainText(b.Content))
	default:
		return strings.TrimSpace(b.Content)
	}
}

// markupText extracts the text nodes of HTML or SVG, dropping scripts and
// styles. Attributes (SVG paths, inline styles) are not text and are dropped.
func markupText(src string) string {
	z := html.NewTokenizer(strings.NewReader(src))
	var sb strings.Builder
	skip := 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return strings.Join(strings.Fields(sb.String()), " ")
		case html.StartTagToken:
			if name, _ := z.TagName(); isHiddenTag(name) {
				skip++
			}
		case html.EndTagToken:
			if name, _ := z.TagName(); isHiddenTag(name) && skip > 0 {
				skip--
			}
		case html.TextToken:
			if skip == 0 {
				sb.Write(z.Text())
				sb.WriteByte(' ')
			}
		}
	}
}

// fieldText joins the values of an html or svg block's fields, in the
// order of their names so that the entry is the same on every write.
func fieldText(fields map[string]string) string {
	names := slices.Sorted(maps.Keys(fields))
	values := make([]string, 0, len(names))
	for _, n := range names {
		if v := strings.Join(strings.Fields(fields[n]), " "); v != "" {
			values = append(values, v)
		}
	}
	return strings.Join(values, " ")
}

// HTMLText is the text an HTML paper's document shows, for search: its
// text nodes outside script, style, template and noscript, with runs of
// white space joined, capped at MaxSearchTextLength bytes.
func HTMLText(src string) string { return capText(markupText(src)) }

func isHiddenTag(name []byte) bool {
	switch string(name) {
	case "script", "style", "template", "noscript":
		return true
	}
	return false
}

func capText(s string) string {
	if len(s) <= MaxSearchTextLength {
		return s
	}
	end := MaxSearchTextLength
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

// SearchQuery is a parsed search query. A document matches when it
// contains every term. Each term is matched as an exact token sequence;
// quoting a phrase in the raw query keeps its words together as one term.
type SearchQuery struct {
	Terms []string
}

// ParseSearchQuery splits a raw query into terms. Whitespace separates
// terms; a double-quoted span is one term. The query language of the
// backing search engine (operators, field prefixes) is deliberately not
// exposed, so every backend gives the same meaning to the same input.
func ParseSearchQuery(raw string) (SearchQuery, error) {
	var terms []string
	var cur strings.Builder
	quoted := false
	flush := func() {
		t := strings.Join(strings.Fields(cur.String()), " ")
		cur.Reset()
		if t != "" {
			terms = append(terms, t)
		}
	}
	for _, r := range raw {
		switch {
		case r == '"':
			flush()
			quoted = !quoted
		case unicode.IsSpace(r) && !quoted:
			flush()
		case unicode.IsControl(r):
			cur.WriteRune(' ')
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	if len(terms) == 0 {
		return SearchQuery{}, goerr.Wrap(ErrInvalidArgument, "query is required")
	}
	if len(terms) > MaxSearchTerms {
		return SearchQuery{}, goerr.Wrap(ErrInvalidArgument, "too many search terms", goerr.V("count", len(terms)), goerr.V("max", MaxSearchTerms))
	}
	for _, t := range terms {
		if utf8.RuneCountInString(t) > MaxSearchTermLength {
			return SearchQuery{}, goerr.Wrap(ErrInvalidArgument, "search term is too long", goerr.V("max", MaxSearchTermLength))
		}
	}
	return SearchQuery{Terms: terms}, nil
}

// Matches reports whether text contains every term, ignoring case. It is
// the reference semantics; search engines may additionally match
// inflections or normalized forms.
func (q SearchQuery) Matches(text string) bool {
	lower := strings.ToLower(text)
	for _, t := range q.Terms {
		if !strings.Contains(lower, strings.ToLower(t)) {
			return false
		}
	}
	return len(q.Terms) > 0
}
