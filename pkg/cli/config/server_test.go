package config_test

import (
	"context"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/cli/config"
)

func TestServer(t *testing.T) {
	s := config.NewServerForTest("https://papers.example.com/")
	gt.NoError(t, s.Validate())
	gt.True(t, s.SecureCookie())
	gt.Equal(t, s.BaseURL(), "https://papers.example.com")
	gt.Equal(t, s.UseCaseConfig().MaxOpsPerBatch, 1)

	gt.False(t, config.NewServerForTest("http://localhost:8080").SecureCookie())
	gt.Error(t, config.NewServerForTest("papers.example.com").Validate())
	gt.Error(t, config.NewServerForTest("ftp://x").Validate())
}

func parseServer(t *testing.T, args ...string) *config.Server {
	t.Helper()
	var x config.Server
	cmd := &cli.Command{Name: "t", Flags: x.Flags(), Action: func(context.Context, *cli.Command) error { return nil }}
	gt.NoError(t, cmd.Run(context.Background(), append([]string{"t"}, args...))).Required()
	return &x
}

func TestDemoIsOffByDefault(t *testing.T) {
	t.Setenv("PAPERS_DEMO", "")
	gt.False(t, parseServer(t).Demo())
}

func TestDemoFromFlagOrEnvironment(t *testing.T) {
	gt.True(t, parseServer(t, "--demo").Demo())
	t.Setenv("PAPERS_DEMO", "true")
	gt.True(t, parseServer(t).Demo())
}
