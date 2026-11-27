package model

import (
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/m-mizutani/goerr/v2"
)

// BlockType is the kind of a block.
type BlockType string

const (
	BlockParagraph BlockType = "paragraph"
	BlockHeading   BlockType = "heading"
	BlockQuote     BlockType = "quote"
	BlockBullet    BlockType = "bulleted_list_item"
	BlockNumbered  BlockType = "numbered_list_item"
	BlockTodo      BlockType = "todo"
	BlockCallout   BlockType = "callout"
	BlockCode      BlockType = "code"
	BlockDivider   BlockType = "divider"
	BlockImage     BlockType = "image"
	BlockTable     BlockType = "table"
	BlockTableCell BlockType = "table_cell"
	// BlockPaper places a child paper in its parent's body.
	BlockPaper BlockType = "paper"

	// Rich blocks are drawn in a sandboxed frame; only agents edit them.
	BlockHTML    BlockType = "html"
	BlockSVG     BlockType = "svg"
	BlockMermaid BlockType = "mermaid"

	// BlockHTMLPage is the whole body of an HTML paper: it points to the
	// paper's text/html attachment and its content is the HTML's text
	// nodes, kept for search. Only the server creates it.
	BlockHTMLPage BlockType = "html_page"
)

type blockTypeSpec struct {
	humanEditable bool
	container     bool
	parentType    BlockType
	noContent     bool
	// markup means the content is inline markup (Markdown + colour spans).
	markup bool
}

var blockTypeSpecs = map[BlockType]blockTypeSpec{
	BlockParagraph: {humanEditable: true, markup: true},
	BlockHeading:   {humanEditable: true, markup: true},
	BlockQuote:     {humanEditable: true, markup: true},
	BlockBullet:    {humanEditable: true, markup: true},
	BlockNumbered:  {humanEditable: true, markup: true},
	BlockTodo:      {humanEditable: true, markup: true},
	BlockCallout:   {humanEditable: true, markup: true},
	BlockCode:      {humanEditable: true},
	BlockDivider:   {humanEditable: true, noContent: true},
	BlockImage:     {humanEditable: true, markup: true},
	BlockTable:     {humanEditable: true, container: true, noContent: true},
	BlockTableCell: {humanEditable: true, parentType: BlockTable, markup: true},
	BlockPaper:     {humanEditable: true, noContent: true},
	BlockHTML:      {},
	BlockSVG:       {},
	BlockMermaid:   {},
	BlockHTMLPage:  {},
}

// BlockTypes lists every block type in display order.
func BlockTypes() []BlockType {
	return []BlockType{
		BlockParagraph, BlockHeading, BlockQuote, BlockBullet, BlockNumbered,
		BlockTodo, BlockCallout, BlockCode, BlockDivider, BlockImage,
		BlockTable, BlockTableCell, BlockPaper, BlockHTML, BlockSVG, BlockMermaid, BlockHTMLPage,
	}
}

func (t BlockType) Validate() error {
	if _, ok := blockTypeSpecs[t]; !ok {
		return goerr.Wrap(ErrInvalidArgument, "unknown block type", goerr.V("type", t))
	}
	return nil
}

// HumanEditable reports whether the Web UI offers an editor for the type.
func (t BlockType) HumanEditable() bool { return blockTypeSpecs[t].humanEditable }

// IsContainer reports whether the type owns child blocks.
func (t BlockType) IsContainer() bool { return blockTypeSpecs[t].container }

// RequiredParent is the parent type the block must live under, if any.
func (t BlockType) RequiredParent() BlockType { return blockTypeSpecs[t].parentType }

// UsesMarkup reports whether the content is inline markup.
func (t BlockType) UsesMarkup() bool { return blockTypeSpecs[t].markup }

// TableLine is a row or a column of a table: an ID and an order key.
type TableLine struct {
	ID    LineID `json:"id"`
	Order string `json:"order"`
}

// BlockProps holds the type-specific values of a block. Fields that do not
// belong to the type are dropped by Normalize.
type BlockProps struct {
	Level    int    `json:"level,omitempty"`
	Indent   int    `json:"indent,omitempty"`
	Checked  bool   `json:"checked,omitempty"`
	Language string `json:"language,omitempty"`
	Icon     string `json:"icon,omitempty"`

	// image: an attachment of the same paper, or an external URL.
	// html_page: the paper's text/html attachment (always set).
	URL          string       `json:"url,omitempty"`
	AttachmentID AttachmentID `json:"attachment_id,omitempty"`

	// table
	Rows      []TableLine `json:"rows,omitempty"`
	Cols      []TableLine `json:"cols,omitempty"`
	HeaderRow bool        `json:"header_row,omitempty"`
	// table_cell
	RowID LineID `json:"row_id,omitempty"`
	ColID LineID `json:"col_id,omitempty"`

	// paper: the child paper this block places.
	PaperID PaperID `json:"paper_id,omitempty"`

	// rich blocks
	Height int `json:"height,omitempty"`
	// Fields holds the values of the elements an html or svg block marks
	// with data-field="name". People change only these values; the markup
	// itself stays as the agent wrote it.
	Fields map[string]string `json:"fields,omitempty"`
	// Databases are the databases the HTML of an html or html_page block
	// may read and write through papers.db in its sandboxed frame, as the
	// person viewing it. The agent writes them; the frame cannot.
	Databases []DatabaseLink `json:"databases,omitempty"`
}

// DatabaseLink names one database an HTML document is connected to.
type DatabaseLink struct {
	ID PaperID `json:"id"`
	// Write lets the document set values, add items and edit items' bodies;
	// without it the document only reads.
	Write bool `json:"write,omitempty"`
}

const (
	MaxIndent        = 6
	MaxHeadingLevel  = 3
	MaxTableRows     = 100
	MaxTableCols     = 20
	MaxTableCells    = 2000
	MaxContentLength = 256 * 1024
	MaxRichHeight    = 4000
	// MaxFields bounds the fields of one html or svg block, and
	// MaxFieldValueLength the bytes of one value.
	MaxFields           = 32
	MaxFieldValueLength = 200
	// MaxDatabaseLinks bounds the databases one HTML document connects to.
	MaxDatabaseLinks = 10
)

// fieldName is the form of a field name (the data-field attribute).
var fieldName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// HasDatabases reports whether the type keeps props.databases.
func (t BlockType) HasDatabases() bool { return t == BlockHTML || t == BlockHTMLPage }

// HasFields reports whether the type keeps props.fields.
func (t BlockType) HasFields() bool { return t == BlockHTML || t == BlockSVG }

// Normalize keeps only the props that belong to t.
func (p BlockProps) Normalize(t BlockType) BlockProps {
	var n BlockProps
	switch t {
	case BlockHeading:
		n.Level = p.Level
	case BlockBullet, BlockNumbered:
		n.Indent = p.Indent
	case BlockTodo:
		n.Indent = p.Indent
		n.Checked = p.Checked
	case BlockCode:
		n.Language = p.Language
	case BlockCallout:
		n.Icon = p.Icon
	case BlockImage:
		n.URL, n.AttachmentID = p.URL, p.AttachmentID
	case BlockTable:
		n.Rows = slices.Clone(p.Rows)
		n.Cols = slices.Clone(p.Cols)
		n.HeaderRow = p.HeaderRow
	case BlockTableCell:
		n.RowID, n.ColID = p.RowID, p.ColID
	case BlockPaper:
		n.PaperID = p.PaperID
	case BlockHTML, BlockSVG:
		n.Height = p.Height
		if len(p.Fields) > 0 {
			n.Fields = maps.Clone(p.Fields)
		}
		if t == BlockHTML && len(p.Databases) > 0 {
			n.Databases = slices.Clone(p.Databases)
		}
	case BlockMermaid:
		n.Height = p.Height
	case BlockHTMLPage:
		n.AttachmentID = p.AttachmentID
		if len(p.Databases) > 0 {
			n.Databases = slices.Clone(p.Databases)
		}
	}
	return n
}

// Equal compares props including table lines.
func (p BlockProps) Equal(o BlockProps) bool {
	return p.Level == o.Level && p.Indent == o.Indent && p.Checked == o.Checked &&
		p.Language == o.Language && p.Icon == o.Icon && p.URL == o.URL &&
		p.AttachmentID == o.AttachmentID && slices.Equal(p.Rows, o.Rows) &&
		slices.Equal(p.Cols, o.Cols) && p.HeaderRow == o.HeaderRow &&
		p.RowID == o.RowID && p.ColID == o.ColID && p.PaperID == o.PaperID &&
		p.Height == o.Height && maps.Equal(p.Fields, o.Fields) &&
		slices.Equal(p.Databases, o.Databases)
}

// ValidateDatabaseLinks checks the shape of an HTML document's database
// connections: valid IDs, at most MaxDatabaseLinks, each database once.
// Whether each one is a database of the same workspace is checked by the
// use case, which can read them.
func ValidateDatabaseLinks(links []DatabaseLink) error {
	if len(links) > MaxDatabaseLinks {
		return goerr.Wrap(ErrInvalidArgument, "too many databases", goerr.V("count", len(links)), goerr.V("max", MaxDatabaseLinks))
	}
	seen := map[PaperID]bool{}
	for _, l := range links {
		if !ValidID(string(l.ID)) {
			return goerr.Wrap(ErrInvalidArgument, "invalid database id", goerr.V("id", l.ID))
		}
		if seen[l.ID] {
			return goerr.Wrap(ErrInvalidArgument, "a database is listed twice", goerr.V("id", l.ID))
		}
		seen[l.ID] = true
	}
	return nil
}

func validFields(fields map[string]string) error {
	if len(fields) > MaxFields {
		return goerr.Wrap(ErrInvalidArgument, "too many fields", goerr.V("count", len(fields)), goerr.V("max", MaxFields))
	}
	for name, value := range fields {
		if !fieldName.MatchString(name) {
			return goerr.Wrap(ErrInvalidArgument, "invalid field name", goerr.V("name", name))
		}
		if len(value) > MaxFieldValueLength {
			return goerr.Wrap(ErrInvalidArgument, "field value is too long", goerr.V("name", name), goerr.V("length", len(value)), goerr.V("max", MaxFieldValueLength))
		}
		if !utf8.ValidString(value) || strings.ContainsAny(value, "\n\r") {
			return goerr.Wrap(ErrInvalidArgument, "field value must be one line of UTF-8 text", goerr.V("name", name))
		}
	}
	return nil
}

func validLines(lines []TableLine, max int, what string) error {
	if len(lines) < 1 || len(lines) > max {
		return goerr.Wrap(ErrInvalidArgument, "table "+what+" out of range", goerr.V("count", len(lines)))
	}
	seen := map[LineID]bool{}
	for _, l := range lines {
		if !ValidID(string(l.ID)) || seen[l.ID] || !ValidOrderKey(l.Order) {
			return goerr.Wrap(ErrInvalidArgument, "invalid table "+what, goerr.V("id", l.ID), goerr.V("order", l.Order))
		}
		seen[l.ID] = true
	}
	return nil
}

func (p BlockProps) validate(t BlockType) error {
	switch t {
	case BlockHeading:
		if p.Level < 1 || p.Level > MaxHeadingLevel {
			return goerr.Wrap(ErrInvalidArgument, "heading level must be between 1 and 3", goerr.V("level", p.Level))
		}
	case BlockBullet, BlockNumbered, BlockTodo:
		if p.Indent < 0 || p.Indent > MaxIndent {
			return goerr.Wrap(ErrInvalidArgument, "indent out of range", goerr.V("indent", p.Indent))
		}
	case BlockImage:
		if p.URL != "" && p.AttachmentID != "" {
			return goerr.Wrap(ErrInvalidArgument, "an image uses either an attachment or a URL")
		}
		if p.AttachmentID != "" && !ValidID(string(p.AttachmentID)) {
			return goerr.Wrap(ErrInvalidArgument, "invalid attachment id", goerr.V("attachment_id", p.AttachmentID))
		}
	case BlockTable:
		if err := validLines(p.Rows, MaxTableRows, "rows"); err != nil {
			return err
		}
		if err := validLines(p.Cols, MaxTableCols, "columns"); err != nil {
			return err
		}
		if len(p.Rows)*len(p.Cols) > MaxTableCells {
			return goerr.Wrap(ErrInvalidArgument, "table has too many cells", goerr.V("rows", len(p.Rows)), goerr.V("cols", len(p.Cols)))
		}
	case BlockTableCell:
		if !ValidID(string(p.RowID)) || !ValidID(string(p.ColID)) {
			return goerr.Wrap(ErrInvalidArgument, "table cell needs a row id and a column id")
		}
	case BlockPaper:
		if !ValidID(string(p.PaperID)) {
			return goerr.Wrap(ErrInvalidArgument, "paper block needs a paper id", goerr.V("paper_id", p.PaperID))
		}
	case BlockHTMLPage:
		if !ValidID(string(p.AttachmentID)) {
			return goerr.Wrap(ErrInvalidArgument, "an html_page block needs an attachment id", goerr.V("attachment_id", p.AttachmentID))
		}
		return ValidateDatabaseLinks(p.Databases)
	case BlockHTML, BlockSVG, BlockMermaid:
		if p.Height < 0 || p.Height > MaxRichHeight {
			return goerr.Wrap(ErrInvalidArgument, "height out of range", goerr.V("height", p.Height))
		}
		if t.HasFields() {
			if err := validFields(p.Fields); err != nil {
				return err
			}
		}
		if t.HasDatabases() {
			return ValidateDatabaseLinks(p.Databases)
		}
	}
	return nil
}

// Block is the unit of editing and conflict detection. Its contents are
// Type, Content and Props; its position is ParentID and Order.
type Block struct {
	ID          BlockID     `json:"id"`
	WorkspaceID WorkspaceID `json:"workspace_id"`
	PaperID     PaperID     `json:"paper_id"`
	// ParentID is set only for table cells.
	ParentID BlockID    `json:"parent_id,omitempty"`
	Order    string     `json:"order"`
	Type     BlockType  `json:"type"`
	Content  string     `json:"content"`
	Props    BlockProps `json:"props"`
	// Rev is an internal revision: it starts at 1 and grows by one each
	// time the contents change. Moving a block does not change it.
	Rev int64 `json:"rev"`

	CreatedBy Actor     `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedBy Actor     `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (b *Block) Validate() error {
	if !ValidID(string(b.ID)) || b.WorkspaceID == "" || b.PaperID == "" {
		return goerr.Wrap(ErrInvalidArgument, "block id, workspace id and paper id are required", goerr.V("id", b.ID))
	}
	if err := b.Type.Validate(); err != nil {
		return err
	}
	if !ValidOrderKey(b.Order) {
		return goerr.Wrap(ErrInvalidArgument, "invalid order key", goerr.V("order", b.Order))
	}
	if len(b.Content) > MaxContentLength {
		return goerr.Wrap(ErrInvalidArgument, "content is too large", goerr.V("length", len(b.Content)))
	}
	if blockTypeSpecs[b.Type].noContent && b.Content != "" {
		return goerr.Wrap(ErrInvalidArgument, "this block type has no content", goerr.V("type", b.Type))
	}
	if b.Type.RequiredParent() != "" && b.ParentID == "" {
		return goerr.Wrap(ErrInvalidArgument, "this block type requires a parent", goerr.V("type", b.Type))
	}
	if b.Type.RequiredParent() == "" && b.ParentID != "" {
		return goerr.Wrap(ErrInvalidArgument, "this block type must be top-level", goerr.V("type", b.Type))
	}
	if err := b.Props.validate(b.Type); err != nil {
		return err
	}
	if err := b.CreatedBy.Validate(); err != nil {
		return goerr.Wrap(err, "invalid block creator")
	}
	if err := b.UpdatedBy.Validate(); err != nil {
		return goerr.Wrap(err, "invalid block updater")
	}
	if b.Rev < 1 {
		return goerr.Wrap(ErrInvalidArgument, "block rev must be positive")
	}
	return nil
}

func (b *Block) Clone() *Block {
	c := *b
	c.Props.Rows = slices.Clone(b.Props.Rows)
	c.Props.Cols = slices.Clone(b.Props.Cols)
	c.Props.Fields = maps.Clone(b.Props.Fields)
	c.Props.Databases = slices.Clone(b.Props.Databases)
	return &c
}

// SameContents reports whether two blocks look the same (type, content and
// props); position and authorship are ignored.
func SameContents(x, y *Block) bool {
	return x.Type == y.Type && x.Content == y.Content && x.Props.Equal(y.Props)
}
