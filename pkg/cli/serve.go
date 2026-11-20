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
		server config.Server
		auth   config.Auth
		repo   config.Repository
		sentry config.Sentry
	)
	flags := append([]cli.Flag{}, server.Flags()...)
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
			logger.Info("Configuration", "server", server, "auth", auth, "repository", repo)

			repository, err := repo.Configure(ctx)
			if err != nil {
				return err
			}
			defer func() {
				if err := repository.Close(); err != nil {
					errutil.Handle(ctx, goerr.Wrap(err, "failed to close repository"), "failed to close repository")
				}
			}()

			uc, err := usecase.New(repository, server.UseCaseConfig())
			if err != nil {
				return goerr.Wrap(err, "failed to build use cases")
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
