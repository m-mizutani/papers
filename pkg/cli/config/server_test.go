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

func TestServerMCPBaseURL(t *testing.T) {
	s := config.NewServerForTest("https://papers.example.com/")
	gt.Equal(t, s.MCPBaseURL(), "https://papers.example.com")

	s.SetMCPBaseURLForTest("https://papers-direct.example.com/")
	gt.NoError(t, s.Validate())
	gt.Equal(t, s.MCPBaseURL(), "https://papers-direct.example.com")

	for _, bad := range []string{"papers-direct.example.com", "ftp://x", "/mcp"} {
		s.SetMCPBaseURLForTest(bad)
		gt.Error(t, s.Validate())
	}
}

func parseServer(t *testing.T, args ...string) *config.Server {
	t.Helper()
	var x config.Server
	cmd := &cli.Command{Name: "t", Flags: x.Flags(), Action: func(context.Context, *cli.Command) error { return nil }}
	gt.NoError(t, cmd.Run(context.Background(), append([]string{"t"}, args...))).Required()
	return &x
}

func TestDefaultUseCaseConfigMatchesTheFlagDefaults(t *testing.T) {
	for _, name := range []string{"PAPERS_MAX_OPS_PER_BATCH", "PAPERS_PAPER_TOUCH_INTERVAL", "PAPERS_TRASH_RETENTION", "PAPERS_JOB_LEASE", "PAPERS_JOB_STEP"} {
		t.Setenv(name, "")
	}
	gt.Equal(t, parseServer(t).UseCaseConfig(), config.DefaultUseCaseConfig())
}
