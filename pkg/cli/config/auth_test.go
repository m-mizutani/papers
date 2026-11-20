package config_test

import (
	"context"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/cli/config"
	"github.com/m-mizutani/papers/pkg/repository/memory"
)

func TestAuthConfigure(t *testing.T) {
	ctx := context.Background()

	a := gt.R1(config.NewAuthForTest("", "", true, nil).Configure(ctx, memory.New(), "http://localhost")).NoError(t)
	gt.True(t, a.IsNoAuthn())
	p := gt.R1(a.DevPrincipal(ctx)).NoError(t)
	gt.True(t, p.SystemAdmin)

	g := gt.R1(config.NewAuthForTest("id", "secret", false, []string{"example.com"}).Configure(ctx, memory.New(), "https://p.example")).NoError(t)
	gt.True(t, g.HasProvider())
	gt.False(t, g.IsNoAuthn())

	_, err := config.NewAuthForTest("id", "secret", true, nil).Configure(ctx, memory.New(), "x")
	gt.Error(t, err)
	_, err = config.NewAuthForTest("", "", false, nil).Configure(ctx, memory.New(), "x")
	gt.Error(t, err)
	_, err = config.NewAuthForTest("id", "secret", false, nil).Configure(ctx, memory.New(), "https://p.example")
	gt.Error(t, err) // no allow rule
	_, err = config.NewAuthForTest("id", "", false, []string{"example.com"}).Configure(ctx, memory.New(), "https://p.example")
	gt.Error(t, err) // no secret
}
