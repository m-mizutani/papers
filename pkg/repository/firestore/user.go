package firestore

import (
	"context"

	"cloud.google.com/go/firestore"
	"github.com/m-mizutani/goerr/v2"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

type emailIndex struct {
	UserID model.UserID
}

func (r *Repository) GetUser(ctx context.Context, id model.UserID) (*model.User, error) {
	return get[model.User](ctx, r.client.Collection(colUsers).Doc(string(id)), "user")
}

func (r *Repository) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	idx, err := get[emailIndex](ctx, r.client.Collection(colEmails).Doc(emailKey(email)), "user")
	if err != nil {
		return nil, err
	}
	return r.GetUser(ctx, idx.UserID)
}

func (r *Repository) ListUsers(ctx context.Context) ([]*model.User, error) {
	return all[model.User](r.client.Collection(colUsers).OrderBy(firestore.DocumentID, firestore.Asc).Documents(ctx), "users")
}

// PutUser stores the user and keeps the email index unique in one
// transaction.
func (r *Repository) PutUser(ctx context.Context, user *model.User) error {
	if err := user.Validate(); err != nil {
		return err
	}
	if err := validID(string(user.ID)); err != nil {
		return err
	}
	userDoc := r.client.Collection(colUsers).Doc(string(user.ID))
	emailDoc := r.client.Collection(colEmails).Doc(emailKey(user.Email))

	err := r.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		var old *model.User
		if snap, err := tx.Get(userDoc); err == nil {
			old = &model.User{}
			if err := snap.DataTo(old); err != nil {
				return goerr.Wrap(err, "failed to decode user")
			}
		} else if !isNotFound(err) {
			return goerr.Wrap(err, "failed to get user")
		}

		if snap, err := tx.Get(emailDoc); err == nil {
			var idx emailIndex
			if err := snap.DataTo(&idx); err != nil {
				return goerr.Wrap(err, "failed to decode email index")
			}
			if idx.UserID != user.ID {
				return goerr.Wrap(model.ErrAlreadyExists, "email is already used by another user", goerr.V("email", user.Email))
			}
		} else if !isNotFound(err) {
			return goerr.Wrap(err, "failed to get email index")
		}

		if err := tx.Set(userDoc, user); err != nil {
			return goerr.Wrap(err, "failed to set user")
		}
		if err := tx.Set(emailDoc, emailIndex{UserID: user.ID}); err != nil {
			return goerr.Wrap(err, "failed to set email index")
		}
		if old != nil && emailKey(old.Email) != emailKey(user.Email) {
			if err := tx.Delete(r.client.Collection(colEmails).Doc(emailKey(old.Email))); err != nil {
				return goerr.Wrap(err, "failed to delete old email index")
			}
		}
		return nil
	})
	if err != nil {
		return goerr.Wrap(err, "failed to put user", goerr.V("user_id", user.ID))
	}
	return nil
}

func (r *Repository) identityDoc(provider model.IdentityProvider, subject string) *firestore.DocumentRef {
	return r.client.Collection(colIdentities).Doc(string(provider)).Collection(colSubjects).Doc(subject)
}

func (r *Repository) GetIdentity(ctx context.Context, provider model.IdentityProvider, subject string) (*model.Identity, error) {
	if err := validID(string(provider)); err != nil {
		return nil, notFound("identity", subject)
	}
	return get[model.Identity](ctx, r.identityDoc(provider, subject), "identity")
}

func (r *Repository) PutIdentity(ctx context.Context, identity *model.Identity) error {
	if err := identity.Validate(); err != nil {
		return err
	}
	if err := validID(string(identity.Provider)); err != nil {
		return err
	}
	if err := validID(identity.Subject); err != nil {
		return err
	}
	if _, err := r.identityDoc(identity.Provider, identity.Subject).Set(ctx, identity); err != nil {
		return goerr.Wrap(err, "failed to put identity")
	}
	return nil
}

// --- sessions / tokens -------------------------------------------------------

func (r *Repository) GetSession(ctx context.Context, id model.SessionID) (*model.Session, error) {
	return get[model.Session](ctx, r.client.Collection(colSessions).Doc(string(id)), "session")
}

func (r *Repository) PutSession(ctx context.Context, session *model.Session) error {
	if err := session.Validate(); err != nil {
		return err
	}
	if err := validID(string(session.ID)); err != nil {
		return err
	}
	if _, err := r.client.Collection(colSessions).Doc(string(session.ID)).Set(ctx, session); err != nil {
		return goerr.Wrap(err, "failed to put session")
	}
	return nil
}

func (r *Repository) DeleteSession(ctx context.Context, id model.SessionID) error {
	if validID(string(id)) != nil {
		return nil
	}
	if _, err := r.client.Collection(colSessions).Doc(string(id)).Delete(ctx); err != nil {
		return goerr.Wrap(err, "failed to delete session")
	}
	return nil
}

func (r *Repository) GetToken(ctx context.Context, id model.TokenID) (*model.APIToken, error) {
	return get[model.APIToken](ctx, r.client.Collection(colTokens).Doc(string(id)), "token")
}

func (r *Repository) ListTokens(ctx context.Context, userID model.UserID) ([]*model.APIToken, error) {
	return all[model.APIToken](r.client.Collection(colTokens).Where("UserID", "==", string(userID)).Documents(ctx), "tokens")
}

func (r *Repository) PutToken(ctx context.Context, token *model.APIToken) error {
	if err := token.Validate(); err != nil {
		return err
	}
	if err := validID(string(token.ID)); err != nil {
		return err
	}
	if _, err := r.client.Collection(colTokens).Doc(string(token.ID)).Set(ctx, token); err != nil {
		return goerr.Wrap(err, "failed to put token")
	}
	return nil
}

func (r *Repository) DeleteToken(ctx context.Context, id model.TokenID) error {
	if validID(string(id)) != nil {
		return nil
	}
	if _, err := r.client.Collection(colTokens).Doc(string(id)).Delete(ctx); err != nil {
		return goerr.Wrap(err, "failed to delete token")
	}
	return nil
}

// GetUsers reads many users in one round trip. Missing ones are omitted.
func (r *Repository) GetUsers(ctx context.Context, ids []model.UserID) ([]*model.User, error) {
	var refs []*firestore.DocumentRef
	for _, id := range ids {
		if validID(string(id)) == nil {
			refs = append(refs, r.client.Collection(colUsers).Doc(string(id)))
		}
	}
	return getAll[model.User](ctx, r.client, refs, "users")
}
