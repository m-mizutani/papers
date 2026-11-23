// Package cli is the command line entry point.
package cli

import (
	"context"

	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/papers/pkg/cli/config"
	"github.com/m-mizutani/papers/pkg/utils/errutil"
	"github.com/m-mizutani/papers/pkg/utils/logging"
)

func Run(ctx context.Context, args []string, version string) error {
	if err := newApp(version).Run(ctx, args); err != nil {
		errutil.Handle(ctx, goerr.Wrap(err, "failed to run app"), "failed to run app")
		return err
	}
	return nil
}

// newApp builds the command tree. Every flag of every command must also be
// settable through a PAPERS_* environment variable (enforced by a test).
func newApp(version string) *cli.Command {
	var loggerCfg config.Logger
	var closer func()

	return &cli.Command{
		Name:    "papers",
		Usage:   "AI-native collaborative knowledge canvas",
		Version: version,
		Flags:   loggerCfg.Flags(),
		Before: func(ctx context.Context, c *cli.Command) (context.Context, error) {
			f, err := loggerCfg.Configure()
			if err != nil {
				return ctx, err
			}
			closer = f
			logging.Default().Info("Starting papers", "version", version, "logger", loggerCfg)
			return ctx, nil
		},
		After: func(ctx context.Context, c *cli.Command) error {
			if closer != nil {
				closer()
			}
			return nil
		},
		Commands: []*cli.Command{
			cmdServe(),
			cmdFirestore(),
		},
	}
}
