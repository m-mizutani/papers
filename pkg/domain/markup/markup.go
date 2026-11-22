// Package markup reads and writes the inline text of papers' blocks:
// Markdown emphasis, code and links, plus Pandoc-style spans for colour
// ([text]{.red}, [text]{.bg-yellow}).
//
// The grammar is closed: only the constructs below are recognised and
// anything else is plain text. Normalize writes the text back in one
// canonical spelling so that equal-looking text is an equal string, which
// conflict detection relies on. The frontend has a twin implementation
// (frontend/src/markup) and both run testdata/markup/cases.json.
package markup

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/m-mizutani/goerr/v2"
)

// Kind is the kind of a node.
type Kind string

const (
	KindText   Kind = "text"
	KindBold   Kind = "bold"
	KindItalic Kind = "italic"
	KindStrike Kind = "strike"
	KindCode   Kind = "code"
	KindLink   Kind = "link"
	KindSpan   Kind = "span"
)

// Node is one piece of inline text.
type Node struct {
	Kind     Kind    `json:"kind"`
	Text     string  `json:"text,omitempty"`     // text and code
	Children []*Node `json:"children,omitempty"` // formatting nodes
	URL      string  `json:"url,omitempty"`      // link
	Color    string  `json:"color,omitempty"`    // span
	Bg       string  `json:"bg,omitempty"`       // span
}

// Palette is the set of named colours. Each theme maps a name to a colour
// that reads well on it.
var Palette = []string{"gray", "brown", "orange", "yellow", "green", "blue", "purple", "pink", "red"}

func inPalette(name string) bool {
	for _, p := range Palette {
		if p == name {
			return true
		}
	}
	return false
}

// ErrInvalid is returned for text that uses an unknown colour.
var ErrInvalid = goerr.New("invalid inline markup")

// Parse reads src. It fails only on spans with classes outside the palette;
// unmatched delimiters are plain text.
func Parse(src string) ([]*Node, error) {
	p := &parser{}
	nodes := p.parse(src)
	if p.err != nil {
		return nil, p.err
	}
	return merge(nodes), nil
}

// Normalize returns the canonical spelling of src.
func Normalize(src string) (string, error) {
	nodes, err := Parse(src)
	if err != nil {
		return "", err
	}
	return Render(nodes), nil
}

// PlainText returns only the visible characters of src. Invalid markup is
// returned as is.
func PlainText(src string) string {
	nodes, err := Parse(src)
	if err != nil {
		return src
	}
	var sb strings.Builder
	plain(&sb, nodes)
	return sb.String()
}

func plain(sb *strings.Builder, nodes []*Node) {
	for _, n := range nodes {
		switch n.Kind {
		case KindText, KindCode:
			sb.WriteString(n.Text)
		default:
			plain(sb, n.Children)
		}
	}
}

type parser struct{ err error }

func isPunct(c byte) bool {
	return c < utf8.RuneSelf && unicode.IsPunct(rune(c)) || strings.IndexByte("$+<=>^`|~", c) >= 0
}

func runeBefore(s string, i int) rune {
	if i <= 0 {
		return ' '
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return r
}

func runeAt(s string, i int) rune {
	if i >= len(s) {
		return ' '
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return r
}

func isAlnum(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// codeEnd returns the index just after the code span that starts at i (a
// run of n backticks), or -1.
func codeEnd(s string, i int) (int, int) {
	n := 0
	for i+n < len(s) && s[i+n] == '`' {
		n++
	}
	j := i + n
	for j < len(s) {
		if s[j] != '`' {
			j++
			continue
		}
		m := 0
		for j+m < len(s) && s[j+m] == '`' {
			m++
		}
		if m == n {
			return n, j + m
		}
		j += m
	}
	return n, -1
}

// findClose finds delim after from, skipping escapes and code spans. For
// single-character emphasis, it applies the flanking rules.
func findClose(s string, from int, delim string) int {
	for j := from; j < len(s); {
		switch {
		case s[j] == '\\' && j+1 < len(s) && isPunct(s[j+1]):
			j += 2
			continue
		case s[j] == '`':
			_, end := codeEnd(s, j)
			if end > 0 {
				j = end
				continue
			}
		case strings.HasPrefix(s[j:], delim):
			if canClose(s, j, delim) {
				return j
			}
		}
		j++
	}
	return -1
}

func canOpen(s string, i int, delim string) bool {
	after := runeAt(s, i+len(delim))
	if unicode.IsSpace(after) || i+len(delim) >= len(s) {
		return false
	}
	if delim[0] == '_' && isAlnum(runeBefore(s, i)) {
		return false
	}
	if len(delim) == 1 && strings.HasPrefix(s[i:], delim+delim) {
		return false // part of a double delimiter
	}
	return true
}

func canClose(s string, j int, delim string) bool {
	if unicode.IsSpace(runeBefore(s, j)) {
		return false
	}
	if delim[0] == '_' && isAlnum(runeAt(s, j+len(delim))) {
		return false
	}
	if len(delim) == 1 && strings.HasPrefix(s[j:], delim+delim) {
		return false
	}
	return true
}

// findBracket finds the "]" that closes the "[" at i.
func findBracket(s string, i int) int {
	depth := 0
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '\\':
			if j+1 < len(s) && isPunct(s[j+1]) {
				j++
			}
		case '`':
			if _, end := codeEnd(s, j); end > 0 {
				j = end - 1
			}
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

func (p *parser) parse(s string) []*Node {
	var out []*Node
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			out = append(out, &Node{Kind: KindText, Text: text.String()})
			text.Reset()
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && isPunct(s[i+1]):
			text.WriteByte(s[i+1])
			i += 2
			continue
		case c == '`':
			n, end := codeEnd(s, i)
			if end < 0 {
				text.WriteString(s[i : i+n])
				i += n
				continue
			}
			inner := s[i+n : end-n]
			if len(inner) >= 2 && inner[0] == ' ' && inner[len(inner)-1] == ' ' && strings.Trim(inner, " ") != "" {
				inner = inner[1 : len(inner)-1]
			}
			flush()
			out = append(out, &Node{Kind: KindCode, Text: inner})
			i = end
			continue
		case strings.HasPrefix(s[i:], "**") || strings.HasPrefix(s[i:], "~~") || strings.HasPrefix(s[i:], "__"):
			delim := s[i : i+2]
			if canOpen(s, i, delim) {
				if j := findClose(s, i+2, delim); j > i+2 {
					flush()
					kind := KindBold
					if delim == "~~" {
						kind = KindStrike
					}
					out = append(out, &Node{Kind: kind, Children: p.parse(s[i+2 : j])})
					i = j + 2
					continue
				}
			}
			text.WriteString(delim)
			i += 2
			continue
		case c == '*' || c == '_':
			delim := s[i : i+1]
			if canOpen(s, i, delim) {
				if j := findClose(s, i+1, delim); j > i+1 {
					flush()
					out = append(out, &Node{Kind: KindItalic, Children: p.parse(s[i+1 : j])})
					i = j + 1
					continue
				}
			}
		case c == '[':
			if j := findBracket(s, i); j > i {
				label := s[i+1 : j]
				if j+1 < len(s) && s[j+1] == '(' {
					if k := strings.IndexByte(s[j+2:], ')'); k >= 0 {
						url := strings.TrimSpace(s[j+2 : j+2+k])
						angled := strings.HasPrefix(url, "<") && strings.HasSuffix(url, ">")
						if angled {
							url = url[1 : len(url)-1]
						}
						if url != "" && (angled || !strings.ContainsAny(url, " \t\n")) && !strings.ContainsAny(url, "<>\n") {
							flush()
							out = append(out, &Node{Kind: KindLink, URL: url, Children: p.parse(label)})
							i = j + 3 + k
							continue
						}
					}
				}
				if j+1 < len(s) && s[j+1] == '{' {
					if k := strings.IndexByte(s[j+2:], '}'); k >= 0 {
						if n, ok := p.span(s[j+2 : j+2+k]); ok {
							flush()
							n.Children = p.parse(label)
							out = append(out, n)
							i = j + 3 + k
							continue
						}
					}
				}
			}
		}
		text.WriteByte(c)
		i++
	}
	flush()
	return out
}

// span reads the attribute list of a span: ".red .bg-yellow".
func (p *parser) span(attrs string) (*Node, bool) {
	fields := strings.Fields(attrs)
	if len(fields) == 0 {
		return nil, false
	}
	n := &Node{Kind: KindSpan}
	for _, f := range fields {
		if !strings.HasPrefix(f, ".") {
			return nil, false
		}
		name := f[1:]
		target := &n.Color
		if strings.HasPrefix(name, "bg-") {
			name, target = name[3:], &n.Bg
		}
		if !inPalette(name) {
			if p.err == nil {
				p.err = goerr.Wrap(ErrInvalid, "unknown colour; use one of the palette", goerr.V("class", f), goerr.V("palette", Palette))
			}
			return nil, false
		}
		*target = name
	}
	return n, true
}

// merge drops empty formatting, flattens nested duplicates and joins
// neighbours that look the same.
func merge(nodes []*Node) []*Node {
	var out []*Node
	for _, n := range nodes {
		if n.Kind != KindText && n.Kind != KindCode {
			n.Children = merge(n.Children)
			if len(n.Children) == 0 {
				continue
			}
			if n.Kind == KindSpan && n.Color == "" && n.Bg == "" {
				out = appendNodes(out, n.Children...)
				continue
			}
			// **a **b** c** -> one bold
			var flat []*Node
			for _, c := range n.Children {
				if c.Kind == n.Kind && n.Kind != KindLink && n.Kind != KindSpan {
					flat = append(flat, c.Children...)
				} else {
					flat = append(flat, c)
				}
			}
			n.Children = merge2(flat)
		}
		if n.Kind == KindText && n.Text == "" {
			continue
		}
		out = appendNodes(out, n)
	}
	return out
}

func merge2(nodes []*Node) []*Node {
	var out []*Node
	return appendNodes(out, nodes...)
}

func appendNodes(out []*Node, nodes ...*Node) []*Node {
	for _, n := range nodes {
		if len(out) > 0 {
			last := out[len(out)-1]
			if last.Kind == n.Kind && sameFormat(last, n) {
				switch n.Kind {
				case KindText:
					last.Text += n.Text
					continue
				case KindCode:
					// adjacent code spans stay separate
				default:
					last.Children = merge2(append(last.Children, n.Children...))
					continue
				}
			}
		}
		out = append(out, n)
	}
	return out
}

func sameFormat(a, b *Node) bool {
	switch a.Kind {
	case KindLink:
		return a.URL == b.URL
	case KindSpan:
		return a.Color == b.Color && a.Bg == b.Bg
	}
	return true
}

// Render writes nodes in canonical form.
func Render(nodes []*Node) string {
	var sb strings.Builder
	render(&sb, nodes)
	return sb.String()
}

func render(sb *strings.Builder, nodes []*Node) {
	for i, n := range nodes {
		switch n.Kind {
		case KindText:
			escapeText(sb, n.Text, i > 0, i < len(nodes)-1)
		case KindCode:
			fence := "`"
			for strings.Contains(n.Text, fence) {
				fence += "`"
			}
			sb.WriteString(fence)
			if strings.HasPrefix(n.Text, "`") || strings.HasSuffix(n.Text, "`") || (strings.HasPrefix(n.Text, " ") && strings.HasSuffix(n.Text, " ") && strings.Trim(n.Text, " ") != "") {
				sb.WriteString(" " + n.Text + " ")
			} else {
				sb.WriteString(n.Text)
			}
			sb.WriteString(fence)
		case KindBold:
			sb.WriteString("**")
			render(sb, n.Children)
			sb.WriteString("**")
		case KindItalic:
			sb.WriteString("*")
			render(sb, n.Children)
			sb.WriteString("*")
		case KindStrike:
			sb.WriteString("~~")
			render(sb, n.Children)
			sb.WriteString("~~")
		case KindLink:
			sb.WriteString("[")
			render(sb, n.Children)
			sb.WriteString("](")
			sb.WriteString(strings.NewReplacer(" ", "%20", ")", "%29").Replace(n.URL))
			sb.WriteString(")")
		case KindSpan:
			sb.WriteString("[")
			render(sb, n.Children)
			sb.WriteString("]{")
			var cls []string
			if n.Color != "" {
				cls = append(cls, "."+n.Color)
			}
			if n.Bg != "" {
				cls = append(cls, ".bg-"+n.Bg)
			}
			sb.WriteString(strings.Join(cls, " "))
			sb.WriteString("}")
		}
	}
}

// escapeText escapes the characters that would otherwise start markup.
// Underscores inside words (snake_case) stay as they are.
func escapeText(sb *strings.Builder, s string, _, _ bool) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			if i+1 >= len(s) || isPunct(s[i+1]) {
				sb.WriteByte('\\')
			}
		case '*', '`', '[':
			sb.WriteByte('\\')
		case '~':
			if i+1 < len(s) && s[i+1] == '~' || i > 0 && s[i-1] == '~' {
				sb.WriteByte('\\')
			}
		case '_':
			if !isAlnum(runeBefore(s, i)) || !isAlnum(runeAt(s, i+1)) {
				sb.WriteByte('\\')
			}
		}
		sb.WriteByte(c)
	}
}
