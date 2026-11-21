package firestore

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"cloud.google.com/go/firestore"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func (r *Repository) GetWorkspaceRecord(ctx context.Context, ws model.WorkspaceID) (*model.WorkspaceRecord, error) {
	if !validKeys(string(ws)) {
		return nil, notFound("workspace", ws)
	}
	return get[model.WorkspaceRecord](ctx, r.workspaceDoc(ws), "workspace")
}

func (r *Repository) EnsureHome(ctx context.Context, ws model.WorkspaceID, home *model.Paper) (*model.WorkspaceRecord, error) {
	if err := home.Validate(); err != nil {
		return nil, err
	}
	if !validKeys(string(ws), string(home.ID)) || home.WorkspaceID != ws {
		return nil, goerr.Wrap(model.ErrInvalidArgument, "invalid home paper", goerr.V("workspace_id", ws))
	}
	var out *model.WorkspaceRecord
	err := r.client.RunTransaction(ctx, func(_ context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(r.workspaceDoc(ws))
		if err == nil {
			out, err = decode[model.WorkspaceRecord](snap, "workspace")
			return err
		}
		if !isNotFound(err) {
			return goerr.Wrap(err, "failed to get workspace record")
		}
		out = &model.WorkspaceRecord{ID: ws, HomePaperID: home.ID}
		if err := tx.Create(r.workspaceDoc(ws), out); err != nil {
			return goerr.Wrap(err, "failed to stage workspace record")
		}
		if err := tx.Create(r.paperDoc(ws, home.ID), home); err != nil {
			return goerr.Wrap(err, "failed to stage home paper")
		}
		return stageSearchEntry(tx, r.searchDoc(ws, home.ID, ""), model.NewTitleSearchEntry(home))
	})
	if err != nil {
		return nil, goerr.Wrap(err, "failed to ensure home paper", goerr.V("workspace_id", ws))
	}
	return out, nil
}

func (r *Repository) ListWorkspaceRecords(ctx context.Context) ([]*model.WorkspaceRecord, error) {
	return all[model.WorkspaceRecord](r.client.Collection(colWorkspaces).Documents(ctx), "workspaces")
}

func (r *Repository) forwardDoc(ws model.WorkspaceID, paper model.PaperID) *firestore.DocumentRef {
	return r.workspaceDoc(ws).Collection(colForwards).Doc(string(paper))
}

func (r *Repository) GetForward(ctx context.Context, ws model.WorkspaceID, paper model.PaperID) (*model.Forward, error) {
	if !validKeys(string(ws), string(paper)) {
		return nil, notFound("forward", paper)
	}
	return get[model.Forward](ctx, r.forwardDoc(ws, paper), "forward")
}

// --- pins and recents

func (r *Repository) pinsCol(user model.UserID) *firestore.CollectionRef {
	return r.client.Collection(colUsers).Doc(string(user)).Collection(colPins)
}

func pinKey(ws model.WorkspaceID, paper model.PaperID) string {
	return string(ws) + "_" + string(paper)
}

func (r *Repository) ListPins(ctx context.Context, user model.UserID, ws model.WorkspaceID) ([]*model.Pin, error) {
	if !validKeys(string(user), string(ws)) {
		return nil, nil
	}
	col := r.pinsCol(user)
	// Workspace IDs never contain '_' or '`', so this ID range covers
	// exactly the pins of the workspace and needs no index.
	it := col.Where(firestore.DocumentID, ">=", col.Doc(string(ws)+"_")).
		Where(firestore.DocumentID, "<", col.Doc(string(ws)+"`")).Documents(ctx)
	pins, err := all[model.Pin](it, "pins")
	if err != nil {
		return nil, err
	}
	slices.SortFunc(pins, func(a, b *model.Pin) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.PaperID, b.PaperID))
	})
	return pins, nil
}

func (r *Repository) PutPin(ctx context.Context, pin *model.Pin) error {
	if !validKeys(string(pin.UserID), string(pin.WorkspaceID), string(pin.PaperID)) {
		return goerr.Wrap(model.ErrInvalidArgument, "invalid pin")
	}
	if _, err := r.pinsCol(pin.UserID).Doc(pinKey(pin.WorkspaceID, pin.PaperID)).Set(ctx, pin); err != nil {
		return goerr.Wrap(err, "failed to put pin")
	}
	return nil
}

func (r *Repository) DeletePin(ctx context.Context, user model.UserID, ws model.WorkspaceID, paper model.PaperID) error {
	if !validKeys(string(user), string(ws), string(paper)) {
		return nil
	}
	if _, err := r.pinsCol(user).Doc(pinKey(ws, paper)).Delete(ctx); err != nil {
		return goerr.Wrap(err, "failed to delete pin")
	}
	return nil
}

func (r *Repository) recentDoc(user model.UserID, ws model.WorkspaceID) *firestore.DocumentRef {
	return r.client.Collection(colUsers).Doc(string(user)).Collection(colRecents).Doc(string(ws))
}

func (r *Repository) GetRecent(ctx context.Context, user model.UserID, ws model.WorkspaceID) (*model.Recent, error) {
	empty := &model.Recent{UserID: user, WorkspaceID: ws, PaperIDs: []model.PaperID{}}
	if !validKeys(string(user), string(ws)) {
		return empty, nil
	}
	rec, err := get[model.Recent](ctx, r.recentDoc(user, ws), "recent")
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return empty, nil
		}
		return nil, err
	}
	if rec.PaperIDs == nil {
		rec.PaperIDs = []model.PaperID{}
	}
	return rec, nil
}

func (r *Repository) PutRecent(ctx context.Context, rec *model.Recent) error {
	if !validKeys(string(rec.UserID), string(rec.WorkspaceID)) {
		return goerr.Wrap(model.ErrInvalidArgument, "invalid recent")
	}
	if _, err := r.recentDoc(rec.UserID, rec.WorkspaceID).Set(ctx, rec); err != nil {
		return goerr.Wrap(err, "failed to put recent")
	}
	return nil
}

// --- attachments

func (r *Repository) attachmentsCol(ws model.WorkspaceID, paper model.PaperID) *firestore.CollectionRef {
	return r.paperDoc(ws, paper).Collection(colAttachments)
}

func (r *Repository) GetAttachment(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, id model.AttachmentID) (*model.Attachment, error) {
	if !validKeys(string(ws), string(paper), string(id)) {
		return nil, notFound("attachment", id)
	}
	return get[model.Attachment](ctx, r.attachmentsCol(ws, paper).Doc(string(id)), "attachment")
}

func (r *Repository) FindAttachmentByHash(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, sum string) (*model.Attachment, error) {
	if !validKeys(string(ws), string(paper)) {
		return nil, notFound("attachment", sum)
	}
	got, err := all[model.Attachment](r.attachmentsCol(ws, paper).Where("SHA256", "==", sum).Limit(1).Documents(ctx), "attachments")
	if err != nil {
		return nil, err
	}
	if len(got) == 0 {
		return nil, notFound("attachment", sum)
	}
	return got[0], nil
}

func (r *Repository) PutAttachment(ctx context.Context, a *model.Attachment) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if !validKeys(string(a.WorkspaceID), string(a.PaperID), string(a.ID)) {
		return goerr.Wrap(model.ErrInvalidArgument, "invalid attachment")
	}
	if _, err := r.attachmentsCol(a.WorkspaceID, a.PaperID).Doc(string(a.ID)).Create(ctx, a); err != nil {
		if isAlreadyExists(err) {
			return goerr.Wrap(model.ErrAlreadyExists, "attachment already exists", goerr.V("id", a.ID))
		}
		return goerr.Wrap(err, "failed to put attachment")
	}
	return nil
}
