package firestore

import (
	"cmp"
	"context"
	"slices"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func (r *Repository) AcquireLock(ctx context.Context, lock *model.BlockLock, now time.Time) (*model.BlockLock, error) {
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	if validID(string(lock.WorkspaceID)) != nil || validID(string(lock.PaperID)) != nil {
		return nil, notFound("paper", lock.PaperID)
	}
	if err := validID(string(lock.BlockID)); err != nil {
		return nil, err
	}
	doc := r.locksCol(lock.WorkspaceID, lock.PaperID).Doc(string(lock.BlockID))
	var current *model.BlockLock
	err := r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		current = nil
		if _, err := tx.Get(r.paperDoc(lock.WorkspaceID, lock.PaperID)); err != nil {
			if isNotFound(err) {
				return notFound("paper", lock.PaperID)
			}
			return goerr.Wrap(err, "failed to get paper")
		}
		snap, err := tx.Get(doc)
		if err == nil {
			var cur model.BlockLock
			if err := snap.DataTo(&cur); err != nil {
				return goerr.Wrap(err, "failed to decode lock")
			}
			if cur.Active(now) && cur.HolderKey != lock.HolderKey {
				current = &cur
				return goerr.Wrap(model.ErrBlockLocked, "block is locked", goerr.V("block_id", lock.BlockID))
			}
		} else if !isNotFound(err) {
			return goerr.Wrap(err, "failed to get lock")
		}
		return tx.Set(doc, lock)
	})
	if err != nil {
		return current, err
	}
	out := *lock
	return &out, nil
}

func (r *Repository) ReleaseLock(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, block model.BlockID, holderKey string) (bool, error) {
	if validID(string(ws)) != nil || validID(string(paper)) != nil {
		return false, notFound("paper", paper)
	}
	if validID(string(block)) != nil {
		return false, nil
	}
	doc := r.locksCol(ws, paper).Doc(string(block))
	released := false
	err := r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		released = false
		snap, err := tx.Get(doc)
		if err != nil {
			if isNotFound(err) {
				return nil
			}
			return goerr.Wrap(err, "failed to get lock")
		}
		var cur model.BlockLock
		if err := snap.DataTo(&cur); err != nil {
			return goerr.Wrap(err, "failed to decode lock")
		}
		if cur.HolderKey != holderKey {
			return nil
		}
		released = true
		return tx.Delete(doc)
	})
	if err != nil {
		return false, goerr.Wrap(err, "failed to release lock")
	}
	return released, nil
}

// ListLocks returns active locks. Expired lock documents are left for the
// TTL policy on ExpiresAt to remove.
func (r *Repository) ListLocks(ctx context.Context, ws model.WorkspaceID, paper model.PaperID, now time.Time) ([]*model.BlockLock, error) {
	if _, err := r.GetPaper(ctx, ws, paper); err != nil {
		return nil, err
	}
	locks, err := all[model.BlockLock](r.locksCol(ws, paper).Documents(ctx), "locks")
	if err != nil {
		return nil, err
	}
	out := locks[:0]
	for _, l := range locks {
		if l.Active(now) {
			out = append(out, l)
		}
	}
	slices.SortFunc(out, func(a, b *model.BlockLock) int { return cmp.Compare(a.BlockID, b.BlockID) })
	return out, nil
}
