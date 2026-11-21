package cli

import (
	"context"
	"errors"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/papers/pkg/cli/config"
	httpctrl "github.com/m-mizutani/papers/pkg/controller/http"
	"github.com/m-mizutani/papers/pkg/usecase"
	"github.com/m-mizutani/papers/pkg/utils/errutil"
	"github.com/m-mizutani/papers/pkg/utils/logging"
)

func cmdServe() *cli.Command {
	var (
		server      config.Server
		auth        config.Auth
		repo        config.Repository
		workspaces  config.Workspaces
		attachments config.Attachments
		sentry      config.Sentry
	)
	flags := append([]cli.Flag{}, server.Flags()...)
	flags = append(flags, workspaces.Flags()...)
	flags = append(flags, attachments.Flags()...)
	flags = append(flags, auth.Flags()...)
	flags = append(flags, repo.Flags()...)
	flags = append(flags, sentry.Flags()...)

	return &cli.Command{
		Name:  "serve",
		Usage: "Run the HTTP server (Web UI, REST API, WebSocket, MCP)",
		Flags: flags,
		Action: func(ctx context.Context, _ *cli.Command) error {
			if err := server.Validate(); err != nil {
				return err
			}
			sentry.Configure(ctx)
			defer errutil.FlushSentry(2 * time.Second)

			logger := logging.Default()
			logger.Info("Configuration", "server", server, "workspaces", workspaces, "attachments", attachments, "auth", auth, "repository", repo)

			registry, err := workspaces.Configure(ctx)
			if err != nil {
				return err
			}
			registry.LogOpen(logger)
			store, err := attachments.Configure(ctx)
			if err != nil {
				return err
			}

			repository, err := repo.Configure(ctx)
			if err != nil {
				return err
			}
			defer func() {
				if err := repository.Close(); err != nil {
					errutil.Handle(ctx, goerr.Wrap(err, "failed to close repository"), "failed to close repository")
				}
			}()

			uc, err := usecase.New(repository, registry, store, server.UseCaseConfig())
			if err != nil {
				return goerr.Wrap(err, "failed to build use cases")
			}
			if err := uc.Init(logging.With(ctx, logger)); err != nil {
				return err
			}
			authUC, err := auth.Configure(ctx, repository, server.BaseURL())
			if err != nil {
				return err
			}

			handler, err := httpctrl.New(uc, authUC,
				httpctrl.WithSecureCookie(server.SecureCookie()),
				httpctrl.WithWebSocketOrigins(server.WebSocketOrigins()),
				httpctrl.WithMCP(server.EnableMCP()),
			)
			if err != nil {
				return goerr.Wrap(err, "failed to build HTTP server")
			}

			httpServer := &http.Server{
				Addr:              server.Addr(),
				Handler:           handler,
				ReadHeaderTimeout: 10 * time.Second,
			}

			ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			// Papers in the trash past the retention are deleted for good.
			go purgeLoop(ctx, uc, time.Hour)

			errCh := make(chan error, 1)
			go func() {
				logger.Info("Listening", "addr", server.Addr(), "base_url", server.BaseURL())
				if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					errCh <- goerr.Wrap(err, "HTTP server failed")
				}
				close(errCh)
			}()

			select {
			case err := <-errCh:
				return err
			case <-ctx.Done():
			}

			logger.Info("Shutting down")
			shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if err := httpServer.Shutdown(shutdownCtx); err != nil {
				return goerr.Wrap(err, "failed to shut down HTTP server")
			}
			return nil
		},
	}
}

func purgeLoop(ctx context.Context, uc *usecase.UseCases, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		n, err := uc.PurgeTrash(ctx)
		if err != nil {
			errutil.Handle(ctx, err, "failed to purge trash")
		} else if n > 0 {
			logging.From(ctx).Info("purged papers from the trash", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
