// Package markdown converts between Markdown documents and papers blocks.
// It is what lets an AI write a whole section in one Markdown string and
// lets anyone export a paper as Markdown. It deliberately supports the subset
// of Markdown that maps one-to-one onto block types.
package markdown

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

// Draft is a block to be created, without identity or position.
type Draft struct {
	Type    model.BlockType
	Content string
	Props   model.BlockProps
	// Cells holds the cell contents of a table draft, row by row.
	Cells [][]string
}

var (
	reHeading  = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	reTodo     = regexp.MustCompile(`^(\s*)[-*+]\s+\[([ xX])\]\s+(.*)$`)
	reBullet   = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	reNumbered = regexp.MustCompile(`^(\s*)\d+[.)]\s+(.*)$`)
	reDivider  = regexp.MustCompile(`^\s*([-*_])(\s*[-*_]){2,}\s*$`)
	reImage    = regexp.MustCompile(`^!\[([^\]]*)\]\(([^)\s]+)\)\s*$`)
	reFence    = regexp.MustCompile("^(```+|~~~+)\\s*([\\w+#.-]*)\\s*$")
	reTableSep = regexp.MustCompile(`^\s*\|?\s*:?-{1,}:?\s*(\|\s*:?-{1,}:?\s*)*\|?\s*$`)
	reCallout  = regexp.MustCompile(`^>\s*\[!(\w+)\]\s*$`)
)

func indentOf(spaces string) int {
	n := 0
	for _, c := range spaces {
		if c == '\t' {
			n += 2
		} else {
			n++
		}
	}
	level := n / 2
	if level > model.MaxIndent {
		level = model.MaxIndent
	}
	return level
}

func splitRow(line string) []string {
	s := strings.TrimSpace(line)
	s = strings.TrimPrefix(s, "|")
	if strings.HasSuffix(s, "|") && !strings.HasSuffix(s, `\|`) {
		s = s[:len(s)-1]
	}
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] == '|' {
			cur.WriteByte('|')
			i++
			continue
		}
		if s[i] == '|' {
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(s[i])
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	return cells
}

var calloutIcons = map[string]string{
	"note": "💡", "tip": "✅", "important": "📌", "warning": "⚠️", "caution": "🚫",
}

func richType(lang string) (model.BlockType, bool) {
	switch strings.ToLower(lang) {
	case "mermaid":
		return model.BlockMermaid, true
	case "svg":
		return model.BlockSVG, true
	case "html":
		return model.BlockHTML, true
	}
	return "", false
}

// Parse converts a Markdown document into block drafts.
func Parse(src string) []Draft {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var out []Draft
	var para []string

	flush := func() {
		if len(para) > 0 {
			out = append(out, Draft{Type: model.BlockParagraph, Content: strings.Join(para, "\n")})
			para = nil
		}
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if m := reFence.FindStringSubmatch(trimmed); m != nil {
			flush()
			fence := m[1]
			lang := m[2]
			var body []string
			i++
			for ; i < len(lines); i++ {
				if strings.HasPrefix(strings.TrimSpace(lines[i]), fence) {
					break
				}
				body = append(body, lines[i])
			}
			content := strings.Join(body, "\n")
			if t, ok := richType(lang); ok {
				out = append(out, Draft{Type: t, Content: content})
			} else {
				out = append(out, Draft{Type: model.BlockCode, Content: content, Props: model.BlockProps{Language: lang}})
			}
			continue
		}

		if trimmed == "" {
			flush()
			continue
		}

		if m := reHeading.FindStringSubmatch(trimmed); m != nil {
			flush()
			level := len(m[1])
			if level > model.MaxHeadingLevel {
				level = model.MaxHeadingLevel
			}
			out = append(out, Draft{Type: model.BlockHeading, Content: strings.TrimSpace(m[2]), Props: model.BlockProps{Level: level}})
			continue
		}

		if reDivider.MatchString(trimmed) && len(para) == 0 {
			out = append(out, Draft{Type: model.BlockDivider})
			continue
		}

		if strings.HasPrefix(trimmed, ">") {
			flush()
			var body []string
			icon := ""
			isCallout := false
			if m := reCallout.FindStringSubmatch(trimmed); m != nil {
				isCallout = true
				icon = calloutIcons[strings.ToLower(m[1])]
				i++
			}
			for ; i < len(lines); i++ {
				t := strings.TrimSpace(lines[i])
				if !strings.HasPrefix(t, ">") {
					break
				}
				t = strings.TrimPrefix(t, ">")
				t = strings.TrimPrefix(t, " ")
				body = append(body, t)
			}
			i--
			if isCallout {
				out = append(out, Draft{Type: model.BlockCallout, Content: strings.Join(body, "\n"), Props: model.BlockProps{Icon: icon}})
			} else {
				out = append(out, Draft{Type: model.BlockQuote, Content: strings.Join(body, "\n")})
			}
			continue
		}

		if m := reTodo.FindStringSubmatch(line); m != nil {
			flush()
			out = append(out, Draft{Type: model.BlockTodo, Content: m[3], Props: model.BlockProps{Indent: indentOf(m[1]), Checked: m[2] != " "}})
			continue
		}
		if m := reBullet.FindStringSubmatch(line); m != nil && !reDivider.MatchString(trimmed) {
			flush()
			out = append(out, Draft{Type: model.BlockBullet, Content: m[2], Props: model.BlockProps{Indent: indentOf(m[1])}})
			continue
		}
		if m := reNumbered.FindStringSubmatch(line); m != nil {
			flush()
			out = append(out, Draft{Type: model.BlockNumbered, Content: m[2], Props: model.BlockProps{Indent: indentOf(m[1])}})
			continue
		}

		if m := reImage.FindStringSubmatch(trimmed); m != nil {
			flush()
			out = append(out, Draft{Type: model.BlockImage, Content: m[1], Props: model.BlockProps{URL: m[2]}})
			continue
		}

		if strings.HasPrefix(trimmed, "|") && i+1 < len(lines) && reTableSep.MatchString(lines[i+1]) {
			flush()
			rows := [][]string{splitRow(trimmed)}
			i += 2
			for ; i < len(lines); i++ {
				t := strings.TrimSpace(lines[i])
				if !strings.HasPrefix(t, "|") {
					break
				}
				rows = append(rows, splitRow(t))
			}
			i--
			cols := 0
			for _, r := range rows {
				if len(r) > cols {
					cols = len(r)
				}
			}
			if cols > model.MaxTableCols {
				cols = model.MaxTableCols
			}
			if len(rows) > model.MaxTableRows {
				rows = rows[:model.MaxTableRows]
			}
			for ri := range rows {
				for len(rows[ri]) < cols {
					rows[ri] = append(rows[ri], "")
				}
				rows[ri] = rows[ri][:cols]
			}
			out = append(out, Draft{Type: model.BlockTable, Props: model.BlockProps{HeaderRow: true}, Cells: rows})
			continue
		}

		para = append(para, trimmed)
	}
	flush()
	return out
}

func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.ReplaceAll(s, "\n", "<br>")
}

func prefixLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = strings.TrimRight(prefix, " ")
		} else {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}

// Render converts a paper's blocks (as returned by ListBlocks) into Markdown.
// titles gives the titles of child papers for paper blocks; a paper block
// renders as a link "[title](paper:ID)", and an attached image as
// "![caption](attachment:ID)".
func Render(title string, blocks []*model.Block, titles map[model.PaperID]string) string {
	children := map[model.BlockID][]*model.Block{}
	var top []*model.Block
	for _, b := range blocks {
		if b.ParentID == "" {
			top = append(top, b)
		} else {
			children[b.ParentID] = append(children[b.ParentID], b)
		}
	}

	var parts []string
	if title != "" {
		parts = append(parts, "# "+title)
	}
	numbered := 0
	for idx, b := range top {
		if b.Type == model.BlockNumbered {
			numbered++
		} else {
			numbered = 0
		}
		indent := strings.Repeat("  ", b.Props.Indent)
		var s string
		switch b.Type {
		case model.BlockHeading:
			s = strings.Repeat("#", b.Props.Level+1) + " " + b.Content
		case model.BlockQuote:
			s = prefixLines(b.Content, "> ")
		case model.BlockCallout:
			s = "> [!NOTE]\n" + prefixLines(b.Content, "> ")
		case model.BlockBullet:
			s = indent + "- " + b.Content
		case model.BlockNumbered:
			s = indent + strconv.Itoa(numbered) + ". " + b.Content
		case model.BlockTodo:
			mark := " "
			if b.Props.Checked {
				mark = "x"
			}
			s = indent + "- [" + mark + "] " + b.Content
		case model.BlockCode:
			s = "```" + b.Props.Language + "\n" + b.Content + "\n```"
		case model.BlockMermaid, model.BlockSVG, model.BlockHTML:
			s = "```" + string(b.Type) + "\n" + b.Content + "\n```"
		case model.BlockDivider:
			s = "---"
		case model.BlockImage:
			src := b.Props.URL
			if b.Props.AttachmentID != "" {
				src = "attachment:" + string(b.Props.AttachmentID)
			}
			s = "![" + b.Content + "](" + src + ")"
		case model.BlockPaper:
			name := titles[b.Props.PaperID]
			if name == "" {
				name = "Untitled"
			}
			s = "[" + name + "](paper:" + string(b.Props.PaperID) + ")"
		case model.BlockTable:
			s = renderTable(b, children[b.ID])
		default:
			s = b.Content
		}

		// Consecutive list items stay in one list (single newline).
		if idx > 0 && isListItem(b.Type) && isListItem(top[idx-1].Type) && len(parts) > 0 {
			parts[len(parts)-1] += "\n" + s
			continue
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n\n") + "\n"
}

func isListItem(t model.BlockType) bool {
	return t == model.BlockBullet || t == model.BlockNumbered || t == model.BlockTodo
}

func sortedLines(lines []model.TableLine) []model.TableLine {
	out := slices.Clone(lines)
	slices.SortStableFunc(out, func(x, y model.TableLine) int {
		if c := strings.Compare(x.Order, y.Order); c != 0 {
			return c
		}
		return strings.Compare(string(x.ID), string(y.ID))
	})
	return out
}

func renderTable(table *model.Block, cells []*model.Block) string {
	rowAt := map[model.LineID]int{}
	colAt := map[model.LineID]int{}
	rows, cols := sortedLines(table.Props.Rows), sortedLines(table.Props.Cols)
	for i, r := range rows {
		rowAt[r.ID] = i
	}
	for i, c := range cols {
		colAt[c.ID] = i
	}
	nCols := len(cols)
	grid := make([][]string, len(rows))
	for r := range grid {
		grid[r] = make([]string, nCols)
	}
	for _, c := range cells {
		r, okR := rowAt[c.Props.RowID]
		col, okC := colAt[c.Props.ColID]
		if okR && okC {
			grid[r][col] = escapeCell(c.Content)
		}
	}
	var lines []string
	row := func(cs []string) string { return "| " + strings.Join(cs, " | ") + " |" }
	start := 0
	if table.Props.HeaderRow && len(grid) > 0 {
		lines = append(lines, row(grid[0]))
		start = 1
	} else {
		lines = append(lines, row(make([]string, nCols)))
	}
	sep := make([]string, nCols)
	for i := range sep {
		sep[i] = "---"
	}
	lines = append(lines, row(sep))
	for _, r := range grid[start:] {
		lines = append(lines, row(r))
	}
	return strings.Join(lines, "\n")
}
