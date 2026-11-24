package usecase

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/utils/safe"
)

// An HTML paper's body is one html_page block pointing to a text/html
// attachment of the paper. Writing a document is two steps: its bytes go to
// the AttachmentStore before the transaction (stageHTML), and the
// attachment's metadata, the html_page block and everything else of the
// request are written in one transaction. When the transaction fails,
// nothing refers to the stored bytes, so to the API the write did not
// happen at all.

// stagedHTML is an HTML document whose bytes are already stored.
type stagedHTML struct {
	attachment *model.Attachment
	// fresh means the attachment's metadata is still to be written; an
	// attachment with the same bytes that already exists is reused.
	fresh bool
	// text is the document's text nodes, the html_page block's content.
	text string
}

// checkHTML validates a document before anything is stored.
func checkHTML(html string) error {
	if html == "" {
		return goerr.Wrap(model.ErrInvalidArgument, "html is required and must not be empty")
	}
	if len(html) > model.MaxHTMLSize {
		return goerr.Wrap(model.ErrInvalidArgument, "the HTML document is too large", goerr.V("bytes", len(html)), goerr.V("max", model.MaxHTMLSize))
	}
	if !utf8.ValidString(html) {
		return goerr.Wrap(model.ErrInvalidArgument, "the HTML document must be UTF-8")
	}
	return nil
}

// stageHTML stores html as an attachment of the paper, before the
// transaction that refers to it. The paper need not exist yet.
func (u *UseCases) stageHTML(ctx context.Context, p *model.Principal, ws model.WorkspaceID, paper model.PaperID, html string) (*stagedHTML, error) {
	if err := checkHTML(html); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(html))
	hash := hex.EncodeToString(sum[:])
	text := model.HTMLText(html)
	if a, err := u.repo.FindAttachmentByHash(ctx, ws, paper, hash); err == nil && a.IsHTML() {
		return &stagedHTML{attachment: a, text: text}, nil
	} else if err != nil && !errors.Is(err, model.ErrNotFound) {
		return nil, goerr.Wrap(err, "failed to look up attachment")
	}
	a := &model.Attachment{
		ID: model.NewAttachmentID(), WorkspaceID: ws, PaperID: paper,
		ContentType: model.HTMLContentType, Size: int64(len(html)), SHA256: hash,
		CreatedBy: p.Actor(), CreatedAt: u.now(),
	}
	if err := a.Validate(); err != nil {
		return nil, err
	}
	if err := u.store.Put(ctx, a.StoreKey(), model.HTMLObjectContentType, bytes.NewReader([]byte(html))); err != nil {
		return nil, goerr.Wrap(err, "failed to store the HTML document")
	}
	return &stagedHTML{attachment: a, fresh: true, text: text}, nil
}

// writeMeta writes the staged attachment's metadata in the transaction when
// it is new.
func (s *stagedHTML) writeMeta(tx interfaces.Tx) error {
	if !s.fresh {
		return nil
	}
	if err := tx.PutAttachment(s.attachment); err != nil {
		return goerr.Wrap(err, "failed to save the HTML document's attachment")
	}
	return nil
}

// newHTMLPage writes the html_page block of a new HTML paper.
func newHTMLPage(tx interfaces.Tx, paper *model.Paper, s *stagedHTML, actor model.Actor) (*model.Block, error) {
	order, err := model.KeyBetween("", "")
	if err != nil {
		return nil, err
	}
	b := &model.Block{
		ID: model.NewBlockID(), WorkspaceID: paper.WorkspaceID, PaperID: paper.ID,
		Order: order, Type: model.BlockHTMLPage, Content: s.text,
		Props: model.BlockProps{AttachmentID: s.attachment.ID}, Rev: 1,
		CreatedBy: actor, CreatedAt: paper.CreatedAt, UpdatedBy: actor, UpdatedAt: paper.CreatedAt,
	}
	if err := s.writeMeta(tx); err != nil {
		return nil, err
	}
	if err := tx.PutBlock(b); err != nil {
		return nil, goerr.Wrap(err, "failed to write the html_page block")
	}
	return b, nil
}

// pageOf returns the html_page block among a paper's blocks.
func pageOf(blocks []*model.Block) *model.Block {
	for _, b := range blocks {
		if b.Type == model.BlockHTMLPage {
			return b
		}
	}
	return nil
}

// setHTML replaces the document of the batch's HTML paper. The base
// revision rules are those of an update of the html_page block.
func (a *applier) setHTML(op Op) error {
	if !a.paper.IsHTML() {
		return goerr.Wrap(model.ErrInvalidArgument, "set_html is only for HTML papers", goerr.V("paper_id", a.paper.ID))
	}
	s := a.html
	if s == nil {
		return goerr.New("set_html ran without a stored document")
	}
	blocks, err := a.tx.Children(a.paper.ID, "")
	if err != nil {
		return goerr.Wrap(err, "failed to read the html_page block")
	}
	page := pageOf(blocks)
	if page == nil {
		return goerr.New("the HTML paper has no html_page block", goerr.V("paper_id", a.paper.ID))
	}
	before, err := a.get(page.ID)
	if err != nil {
		return goerr.Wrap(err, "failed to read the html_page block")
	}
	if err := a.checkBase(before.ID, op.BaseRev); err != nil {
		return err
	}
	b := before.Clone()
	b.Content = s.text
	b.Props = model.BlockProps{AttachmentID: s.attachment.ID}
	if model.SameContents(b, before) {
		return nil
	}
	if err := s.writeMeta(a.tx); err != nil {
		return err
	}
	a.bump(b)
	return a.put(b)
}

// HTMLView is an HTML paper's document in some state.
type HTMLView struct {
	BlockID      model.BlockID      `json:"block_id"`
	Rev          int64              `json:"rev"`
	AttachmentID model.AttachmentID `json:"attachment_id"`
	Size         int64              `json:"size"`
	HTML         string             `json:"html"`
}

// ReadHTML reads an HTML paper's document: the current state, or a version
// (at as for ParseAt).
func (u *UseCases) ReadHTML(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, at string) (*HTMLView, error) {
	n, err := ParseAt(at)
	if err != nil {
		return nil, err
	}
	_, paper, err := u.paperFor(ctx, p, ws, id)
	if err != nil {
		return nil, err
	}
	if !paper.IsHTML() {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "the paper is not an HTML paper", goerr.V("paper_id", id))
	}
	var blocks []*model.Block
	if n == 0 {
		if blocks, err = u.repo.ListBlocks(ctx, ws, id); err != nil {
			return nil, goerr.Wrap(err, "failed to list blocks")
		}
	} else {
		v, err := u.GetVersion(ctx, p, ws, id, n)
		if err != nil {
			return nil, err
		}
		blocks = v.Blocks
	}
	page := pageOf(blocks)
	if page == nil {
		return nil, goerr.New("the HTML paper has no html_page block", goerr.V("paper_id", id))
	}
	a, err := u.repo.GetAttachment(ctx, ws, id, page.Props.AttachmentID)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get the HTML document's attachment")
	}
	body, err := u.store.Get(ctx, a.StoreKey())
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read the HTML document")
	}
	defer safe.Close(ctx, body)
	data, err := io.ReadAll(io.LimitReader(body, model.MaxHTMLSize+1))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read the HTML document")
	}
	return &HTMLView{BlockID: page.ID, Rev: page.Rev, AttachmentID: a.ID, Size: a.Size, HTML: string(data)}, nil
}

// HTMLContent is an HTML document to serve, with whether the workspace lets
// it load external resources. Open reads its bytes; a caller that answers
// "not modified" never opens them.
type HTMLContent struct {
	Attachment        *model.Attachment
	ExternalResources bool
	Open              func(ctx context.Context) (io.ReadCloser, error)
}

// OpenHTML returns one HTML document (an attachment) of an HTML paper the
// caller may read. Image attachments are not served this way.
func (u *UseCases) OpenHTML(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, att model.AttachmentID) (*HTMLContent, error) {
	w, _, err := u.paperFor(ctx, p, ws, id)
	if err != nil {
		return nil, err
	}
	if !model.ValidID(string(att)) {
		return nil, notFound("attachment", att)
	}
	a, err := u.repo.GetAttachment(ctx, ws, id, att)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get attachment")
	}
	if !a.IsHTML() {
		return nil, notFound("attachment", att)
	}
	open := func(ctx context.Context) (io.ReadCloser, error) {
		body, err := u.store.Get(ctx, a.StoreKey())
		if err != nil {
			return nil, goerr.Wrap(err, "failed to read the HTML document")
		}
		return body, nil
	}
	return &HTMLContent{Attachment: a, ExternalResources: w.Sandbox.ExternalResources, Open: open}, nil
}
