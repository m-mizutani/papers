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
	var loggerCfg config.Logger
	var closer func()

	app := &cli.Command{
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

	if err := app.Run(ctx, args); err != nil {
		errutil.Handle(ctx, goerr.Wrap(err, "failed to run app"), "failed to run app")
		return err
	}
	return nil
}
