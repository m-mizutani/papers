package model

import (
	"time"

	"github.com/m-mizutani/goerr/v2"
)

// BlockType is the kind of a block. Text-like blocks hold inline Markdown in
// Content; source-like blocks (code, html, svg, mermaid) hold source code.
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
	// BlockTable is a container; its cells are child blocks of type
	// BlockTableCell, each addressed by Props.Row / Props.Col. A cell is a
	// block of its own so it can be edited (and locked) independently.
	BlockTable     BlockType = "table"
	BlockTableCell BlockType = "table_cell"
	// Rich blocks. They are produced by AI through the API / MCP; the Web UI
	// renders them but does not offer an editor for them.
	BlockHTML    BlockType = "html"
	BlockSVG     BlockType = "svg"
	BlockMermaid BlockType = "mermaid"
)

type blockTypeSpec struct {
	// humanEditable is false for rich blocks that the UI only renders.
	humanEditable bool
	// container blocks may have children.
	container bool
	// child blocks must have a parent of this type.
	parentType BlockType
	// noContent blocks must have an empty Content.
	noContent bool
}

var blockTypeSpecs = map[BlockType]blockTypeSpec{
	BlockParagraph: {humanEditable: true},
	BlockHeading:   {humanEditable: true},
	BlockQuote:     {humanEditable: true},
	BlockBullet:    {humanEditable: true},
	BlockNumbered:  {humanEditable: true},
	BlockTodo:      {humanEditable: true},
	BlockCallout:   {humanEditable: true},
	BlockCode:      {humanEditable: true},
	BlockDivider:   {humanEditable: true, noContent: true},
	BlockImage:     {humanEditable: true},
	BlockTable:     {humanEditable: true, container: true, noContent: true},
	BlockTableCell: {humanEditable: true, parentType: BlockTable},
	BlockHTML:      {},
	BlockSVG:       {},
	BlockMermaid:   {},
}

// BlockTypes returns every known block type in a stable order.
func BlockTypes() []BlockType {
	return []BlockType{
		BlockParagraph, BlockHeading, BlockQuote, BlockBullet, BlockNumbered,
		BlockTodo, BlockCallout, BlockCode, BlockDivider, BlockImage,
		BlockTable, BlockTableCell, BlockHTML, BlockSVG, BlockMermaid,
	}
}

func (t BlockType) Validate() error {
	if _, ok := blockTypeSpecs[t]; !ok {
		return goerr.Wrap(ErrInvalidArgument, "unknown block type", goerr.V("type", t))
	}
	return nil
}

// HumanEditable reports whether the Web UI offers an editor for this type.
func (t BlockType) HumanEditable() bool { return blockTypeSpecs[t].humanEditable }

// IsContainer reports whether blocks of this type may have children.
func (t BlockType) IsContainer() bool { return blockTypeSpecs[t].container }

// RequiredParent returns the parent type a block of this type must have, or
// "" if it must be a top-level block.
func (t BlockType) RequiredParent() BlockType { return blockTypeSpecs[t].parentType }

// BlockProps holds the type-specific attributes of a block. Only the fields
// relevant to the type are meaningful; Normalize clears the rest so stored
// blocks never carry stale attributes after a type change.
type BlockProps struct {
	// heading: 1..3
	Level int `json:"level,omitempty"`
	// list items, todo: nesting depth 0..MaxIndent
	Indent int `json:"indent,omitempty"`
	// todo
	Checked bool `json:"checked,omitempty"`
	// code: highlight language
	Language string `json:"language,omitempty"`
	// callout: an emoji or short icon
	Icon string `json:"icon,omitempty"`
	// image: source URL. Content is the caption.
	URL string `json:"url,omitempty"`
	// table: shape and whether the first row is a header
	Rows      int  `json:"rows,omitempty"`
	Cols      int  `json:"cols,omitempty"`
	HeaderRow bool `json:"header_row,omitempty"`
	// table_cell: position in the parent table (0-origin)
	Row int `json:"row,omitempty"`
	Col int `json:"col,omitempty"`
	// html / svg / mermaid: rendered height hint in pixels (0 = auto)
	Height int `json:"height,omitempty"`
}

const (
	MaxIndent       = 6
	MaxHeadingLevel = 3
	MaxTableRows    = 100
	MaxTableCols    = 20
	// MaxTableCells bounds rows x cols so that creating a table fits in
	// one commit (see MaxChangesPerCommit).
	MaxTableCells    = 200
	MaxContentLength = 256 * 1024
	MaxRichHeight    = 4000
)

// Normalize clears props that do not apply to t.
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
		n.URL = p.URL
	case BlockTable:
		n.Rows, n.Cols, n.HeaderRow = p.Rows, p.Cols, p.HeaderRow
	case BlockTableCell:
		n.Row, n.Col = p.Row, p.Col
	case BlockHTML, BlockSVG, BlockMermaid:
		n.Height = p.Height
	}
	return n
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
	case BlockTable:
		if p.Rows < 1 || p.Rows > MaxTableRows || p.Cols < 1 || p.Cols > MaxTableCols || p.Rows*p.Cols > MaxTableCells {
			return goerr.Wrap(ErrInvalidArgument, "table shape out of range", goerr.V("rows", p.Rows), goerr.V("cols", p.Cols))
		}
	case BlockTableCell:
		if p.Row < 0 || p.Row >= MaxTableRows || p.Col < 0 || p.Col >= MaxTableCols {
			return goerr.Wrap(ErrInvalidArgument, "table cell position out of range", goerr.V("row", p.Row), goerr.V("col", p.Col))
		}
	case BlockHTML, BlockSVG, BlockMermaid:
		if p.Height < 0 || p.Height > MaxRichHeight {
			return goerr.Wrap(ErrInvalidArgument, "height out of range", goerr.V("height", p.Height))
		}
	}
	return nil
}

// Block is the unit of editing and of soft locking. Blocks are ordered among
// their siblings by Order, a fractional index key (see KeyBetween), so an
// insertion never rewrites the order of other blocks.
type Block struct {
	ID          BlockID     `json:"id"`
	WorkspaceID WorkspaceID `json:"workspace_id"`
	PaperID     PaperID     `json:"paper_id"`
	ParentID    BlockID     `json:"parent_id,omitempty"`
	Order       string      `json:"order"`
	Type        BlockType   `json:"type"`
	Content     string      `json:"content"`
	Props       BlockProps  `json:"props"`
	// Version increases by one on every change to this block. Writers may
	// send the version they based their edit on; a mismatch does not reject
	// the write (last writer wins) but is reported as an overwrite.
	Version   int64     `json:"version"`
	CreatedBy Actor     `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedBy Actor     `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (b *Block) Validate() error {
	if b.ID == "" || b.WorkspaceID == "" || b.PaperID == "" {
		return goerr.Wrap(ErrInvalidArgument, "block id, workspace id and paper id are required")
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
	if b.Version < 1 {
		return goerr.Wrap(ErrInvalidArgument, "block version must be positive")
	}
	return nil
}

// Clone returns a deep copy (Block has no reference fields beyond values).
func (b *Block) Clone() *Block {
	c := *b
	return &c
}
