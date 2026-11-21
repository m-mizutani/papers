package config

import (
	"context"
	"log/slog"

	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/papers/pkg/service/workspace"
)

// Workspaces points at the workspace configuration that is deployed with
// the server: one TOML file per workspace in a directory, and an optional
// global file for display groups.
type Workspaces struct {
	dir    string
	global string
}

func (x *Workspaces) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name: "config", Aliases: []string{"c"}, Category: "workspaces",
			Usage:   "Directory of workspace definitions (one TOML file per workspace)",
			Sources: cli.EnvVars("PAPERS_CONFIG"), Destination: &x.dir,
		},
		&cli.StringFlag{
			Name: "global-config", Category: "workspaces", Usage: "TOML file grouping workspaces for display",
			Sources: cli.EnvVars("PAPERS_GLOBAL_CONFIG"), Destination: &x.global,
		},
	}
}

func (x Workspaces) LogValue() slog.Value {
	return slog.GroupValue(slog.String("config", x.dir), slog.String("global_config", x.global))
}

// Configure loads and validates every workspace; any error stops startup.
func (x *Workspaces) Configure(ctx context.Context) (*workspace.Registry, error) {
	if x.dir == "" {
		return nil, goerr.New("--config (the workspace configuration directory) is required")
	}
	return workspace.Load(ctx, x.dir, x.global)
}
