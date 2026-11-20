package interfaces

import (
	"context"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

// IdentityProvider is an external login provider (Google OIDC today).
type IdentityProvider interface {
	Name() model.IdentityProvider
	// AuthURL returns the URL to send the browser to.
	AuthURL(state, nonce string) string
	// Exchange redeems an authorization code and returns the verified
	// claims. Implementations must verify the ID token signature, issuer,
	// audience, expiry and nonce.
	Exchange(ctx context.Context, code, nonce string) (*model.ExternalClaims, error)
}
