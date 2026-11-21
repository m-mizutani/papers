package usecase

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/utils/logging"
)

// --- creating papers

// CreateInput creates a paper. A public paper is placed under Parent (a
// paper ID or a path; default: the home) as a paper block; a private one
// stays out of the tree.
type CreateInput struct {
	Title      string
	Visibility model.Visibility
	Parent     string
	After      *string
	Markdown   string
}

// CreatePaper creates a paper. For a public paper this is the insert of a
// paper block into the parent (one batch), followed by the body when
// Markdown is given.
func (u *UseCases) CreatePaper(ctx context.Context, p *model.Principal, ws model.WorkspaceID, in CreateInput) (*model.Paper, error) {
	if _, err := u.writableWorkspace(ctx, p, ws); err != nil {
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	if err := model.ValidateTitle(title); err != nil {
		return nil, err
	}
	var paper *model.Paper
	switch in.Visibility {
	case "", model.VisibilityPublic:
		parent, err := u.parentFor(ctx, p, ws, in.Parent)
		if err != nil {
			return nil, err
		}
		res, err := u.Apply(ctx, p, ws, parent.ID, BatchInput{Ops: []Op{{Kind: OpInsert, Type: ptr(model.BlockPaper), Title: &title, After: in.After}}})
		if err != nil {
			return nil, err
		}
		for _, b := range res.Blocks {
			if b.Type == model.BlockPaper {
				if paper, err = u.repo.GetPaper(ctx, ws, b.Props.PaperID); err != nil {
					return nil, goerr.Wrap(err, "failed to read the new paper")
				}
			}
		}
	case model.VisibilityPrivate:
		now := u.now()
		actor := p.Actor()
		paper = &model.Paper{
			ID: model.NewPaperID(), WorkspaceID: ws, Title: title, Visibility: model.VisibilityPrivate,
			CreatedBy: actor, CreatedAt: now, UpdatedBy: actor, UpdatedAt: now,
		}
		if err := u.repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutPaper(paper) }); err != nil {
			return nil, goerr.Wrap(err, "failed to create paper")
		}
	default:
		return nil, goerr.Wrap(model.ErrInvalidArgument, "visibility must be public or private")
	}
	if paper == nil {
		return nil, goerr.New("the new paper was not created")
	}
	if in.Markdown != "" {
		if _, err := u.Apply(ctx, p, ws, paper.ID, BatchInput{Ops: []Op{{Kind: OpInsertMarkdown, Markdown: &in.Markdown}}}); err != nil {
			return nil, goerr.Wrap(err, "the paper was created but its body was not written", goerr.V("paper_id", paper.ID))
		}
	}
	return paper, nil
}

// --- pins and recents

func (u *UseCases) touchRecent(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID) {
	rec, err := u.repo.GetRecent(ctx, p.User.ID, ws)
	if err == nil {
		if len(rec.PaperIDs) > 0 && rec.PaperIDs[0] == id {
			return
		}
		rec.Touch(id, u.now())
		err = u.repo.PutRecent(ctx, rec)
	}
	if err != nil {
		// Losing a recent entry is harmless; reading the paper must not fail.
		logging.From(ctx).Warn("failed to record recent paper", slog.String("paper_id", string(id)), logging.ErrAttr(err))
	}
}

// Pin adds a paper to the caller's pins (at the end) or moves it after
// another pin (after: a paper ID, "" for the top).
func (u *UseCases) Pin(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, after *string) error {
	if _, _, err := u.paperFor(ctx, p, ws, id); err != nil {
		return err
	}
	pins, err := u.repo.ListPins(ctx, p.User.ID, ws)
	if err != nil {
		return goerr.Wrap(err, "failed to list pins")
	}
	var others []*model.Pin
	for _, pin := range pins {
		if pin.PaperID != id {
			others = append(others, pin)
		}
	}
	lo, hi := "", ""
	switch {
	case after == nil:
		if len(others) > 0 {
			lo = others[len(others)-1].Order
		}
	case *after == "":
		if len(others) > 0 {
			hi = others[0].Order
		}
	default:
		found := false
		for i, pin := range others {
			if string(pin.PaperID) == *after {
				lo, found = pin.Order, true
				if i+1 < len(others) {
					hi = others[i+1].Order
				}
			}
		}
		if !found {
			return goerr.Wrap(model.ErrInvalidArgument, "anchor pin not found")
		}
	}
	if lo == hi {
		hi = ""
	}
	key, err := model.KeyBetween(lo, hi)
	if err != nil {
		return goerr.Wrap(err, "failed to order pin")
	}
	return u.repo.PutPin(ctx, &model.Pin{UserID: p.User.ID, WorkspaceID: ws, PaperID: id, Order: key})
}

func (u *UseCases) Unpin(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID) error {
	if _, err := u.workspaceFor(ctx, p, ws); err != nil {
		return err
	}
	return u.repo.DeletePin(ctx, p.User.ID, ws, id)
}

// --- presence

// ReportPresence says the caller is editing a block (or stopped, with
// leaving). It goes to the paper's feed and lasts PresenceTTL.
func (u *UseCases) ReportPresence(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, block model.BlockID, leaving bool) error {
	if _, _, err := u.writablePaper(ctx, p, ws, id); err != nil {
		return err
	}
	if !model.ValidID(string(block)) {
		return goerr.Wrap(model.ErrInvalidArgument, "invalid block id")
	}
	now := u.now()
	return u.repo.PutEvent(ctx, &model.Event{
		Type: model.EventPresence, WorkspaceID: ws, PaperID: id, At: now,
		Presence: &model.Presence{BlockID: block, Actor: p.Actor(), Until: now.Add(model.PresenceTTL), Leaving: leaving},
	})
}

// --- feed

// Watch delivers the paper's change feed. The channel closes when ctx ends,
// when the paper is purged or when the reader falls behind (then reload).
func (u *UseCases) Watch(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID) (<-chan *model.Event, error) {
	if _, _, err := u.paperFor(ctx, p, ws, id); err != nil {
		return nil, err
	}
	ch, err := u.repo.Watch(ctx, ws, id)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to watch paper")
	}
	return ch, nil
}

// EventsSince lists the feed entries at or after since (resuming a feed).
func (u *UseCases) EventsSince(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, since time.Time) ([]*model.Event, error) {
	if _, _, err := u.paperFor(ctx, p, ws, id); err != nil {
		return nil, err
	}
	if u.now().Sub(since) > model.EventTTL {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "the feed does not reach back that far; reload the paper")
	}
	return u.repo.ListEvents(ctx, ws, id, since)
}

// --- attachments

// UploadInput is an image to attach to a paper.
type UploadInput struct {
	Name        string
	ContentType string
	Body        io.Reader
}

// Upload attaches an image to a paper. The same bytes attached twice to
// one paper yield the same attachment.
func (u *UseCases) Upload(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, in UploadInput) (*model.Attachment, error) {
	if _, _, err := u.writablePaper(ctx, p, ws, id); err != nil {
		return nil, err
	}
	if !model.AllowedAttachmentTypes[in.ContentType] {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "only images can be attached for now", goerr.V("content_type", in.ContentType))
	}
	data, err := io.ReadAll(io.LimitReader(in.Body, model.MaxAttachmentSize+1))
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read upload")
	}
	if len(data) > model.MaxAttachmentSize {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "the image is too large", goerr.V("max", model.MaxAttachmentSize))
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if a, err := u.repo.FindAttachmentByHash(ctx, ws, id, hash); err == nil {
		return a, nil
	} else if !errors.Is(err, model.ErrNotFound) {
		return nil, goerr.Wrap(err, "failed to look up attachment")
	}
	name := path.Base(strings.ReplaceAll(strings.TrimSpace(in.Name), "\\", "/"))
	if name == "." || name == "/" {
		name = ""
	}
	if len(name) > 256 {
		name = name[:256]
	}
	a := &model.Attachment{
		ID: model.NewAttachmentID(), WorkspaceID: ws, PaperID: id, Name: name,
		ContentType: in.ContentType, Size: int64(len(data)), SHA256: hash,
		CreatedBy: p.Actor(), CreatedAt: u.now(),
	}
	if err := a.Validate(); err != nil {
		return nil, err
	}
	if err := u.store.Put(ctx, a.StoreKey(), a.ContentType, bytes.NewReader(data)); err != nil {
		return nil, goerr.Wrap(err, "failed to store attachment")
	}
	if err := u.repo.PutAttachment(ctx, a); err != nil {
		return nil, goerr.Wrap(err, "failed to save attachment")
	}
	return a, nil
}

// AttachmentContent is how to deliver an attachment: a short-lived URL when
// the store can sign one, otherwise the bytes.
type AttachmentContent struct {
	Attachment *model.Attachment
	URL        string
	Body       io.ReadCloser
}

// OpenAttachment returns an attachment of a paper the caller may read.
func (u *UseCases) OpenAttachment(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID, att model.AttachmentID) (*AttachmentContent, error) {
	if _, _, err := u.paperFor(ctx, p, ws, id); err != nil {
		return nil, err
	}
	if !model.ValidID(string(att)) {
		return nil, notFound("attachment", att)
	}
	a, err := u.repo.GetAttachment(ctx, ws, id, att)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to get attachment")
	}
	url, err := u.store.SignedURL(ctx, a.StoreKey(), 5*time.Minute)
	if err != nil {
		return nil, goerr.Wrap(err, "failed to sign attachment URL")
	}
	if url != "" {
		return &AttachmentContent{Attachment: a, URL: url}, nil
	}
	body, err := u.store.Get(ctx, a.StoreKey())
	if err != nil {
		return nil, goerr.Wrap(err, "failed to read attachment")
	}
	return &AttachmentContent{Attachment: a, Body: body}, nil
}

// CanSee reports whether the principal may still see the paper (live feeds
// recheck after a paper changes).
func (u *UseCases) CanSee(ctx context.Context, p *model.Principal, ws model.WorkspaceID, id model.PaperID) (bool, error) {
	_, _, err := u.paperFor(ctx, p, ws, id)
	if errors.Is(err, model.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}
