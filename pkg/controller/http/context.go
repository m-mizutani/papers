package http

import (
	"context"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

type principalKey struct{}

func withPrincipal(ctx context.Context, p *model.Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func principalFrom(ctx context.Context) *model.Principal {
	p, _ := ctx.Value(principalKey{}).(*model.Principal)
	return p
}
