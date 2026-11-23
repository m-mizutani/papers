package config_test

import (
	"context"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/cli/config"
	"github.com/m-mizutani/papers/pkg/repository/memory"
)

func TestAuthConfigure(t *testing.T) {
	ctx := context.Background()

	a := gt.R1(config.NewAuthForTest("", "", true, nil).Configure(ctx, memory.New(), "http://localhost")).NoError(t)
	gt.True(t, a.IsNoAuthn())
	p := gt.R1(a.DevPrincipal(ctx)).NoError(t)
	gt.Equal(t, p.User.Email, "dev@localhost")

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

func TestAuthConfigureAnonymous(t *testing.T) {
	ctx := context.Background()

	a := gt.R1(config.NewAnonymousAuthForTest("", false).Configure(ctx, memory.New(), "http://localhost")).NoError(t)
	gt.True(t, a.IsAnonymous())
	gt.False(t, a.HasProvider())
	gt.False(t, a.IsNoAuthn())

	_, err := config.NewAnonymousAuthForTest("", true).Configure(ctx, memory.New(), "http://localhost")
	gt.Error(t, err) // with --no-authn
	_, err = config.NewAnonymousAuthForTest("id", false).Configure(ctx, memory.New(), "https://p.example")
	gt.Error(t, err) // with Google sign-in
}

func TestAnonymousFromEnvironment(t *testing.T) {
	t.Setenv("PAPERS_ANONYMOUS", "true")
	var x config.Auth
	var got bool
	cmd := &cli.Command{
		Name: "t", Flags: x.Flags(),
		Action: func(ctx context.Context, _ *cli.Command) error {
			a, err := x.Configure(ctx, memory.New(), "http://localhost")
			if err != nil {
				return err
			}
			got = a.IsAnonymous()
			return nil
		},
	}
	gt.NoError(t, cmd.Run(context.Background(), []string{"t"}))
	gt.True(t, got)
}
