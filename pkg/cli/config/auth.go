package config

import (
	"context"
	"log/slog"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/service/google"
	"github.com/m-mizutani/papers/pkg/usecase"
	"github.com/m-mizutani/papers/pkg/utils/logging"
)

// Auth holds authentication settings.
type Auth struct {
	googleClientID     string
	googleClientSecret string
	googleHostedDomain string
	allowedDomains     []string
	allowedEmails      []string
	adminEmails        []string
	sessionTTL         time.Duration
	noAuthn            bool
	devUserEmail       string
	devUserName        string
}

func (x *Auth) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "google-client-id", Category: "auth", Usage: "Google OAuth client ID", Sources: cli.EnvVars("PAPERS_GOOGLE_CLIENT_ID"), Destination: &x.googleClientID},
		&cli.StringFlag{Name: "google-client-secret", Category: "auth", Usage: "Google OAuth client secret", Sources: cli.EnvVars("PAPERS_GOOGLE_CLIENT_SECRET"), Destination: &x.googleClientSecret},
		&cli.StringFlag{Name: "google-hosted-domain", Category: "auth", Usage: "Hint Google to show only accounts of this Workspace domain", Sources: cli.EnvVars("PAPERS_GOOGLE_HOSTED_DOMAIN"), Destination: &x.googleHostedDomain},
		&cli.StringSliceFlag{Name: "allowed-domain", Category: "auth", Usage: "Email domain allowed to sign in (repeatable)", Sources: cli.EnvVars("PAPERS_ALLOWED_DOMAINS"), Destination: &x.allowedDomains},
		&cli.StringSliceFlag{Name: "allowed-email", Category: "auth", Usage: "Email address allowed to sign in (repeatable)", Sources: cli.EnvVars("PAPERS_ALLOWED_EMAILS"), Destination: &x.allowedEmails},
		&cli.StringSliceFlag{Name: "admin-email", Category: "auth", Usage: "System administrator email (repeatable)", Sources: cli.EnvVars("PAPERS_ADMIN_EMAILS"), Destination: &x.adminEmails},
		&cli.DurationFlag{Name: "session-ttl", Category: "auth", Usage: "Browser session lifetime", Value: 30 * 24 * time.Hour, Sources: cli.EnvVars("PAPERS_SESSION_TTL"), Destination: &x.sessionTTL},
		&cli.BoolFlag{Name: "no-authn", Category: "auth", Usage: "DEVELOPMENT ONLY: authenticate every request as the dev user", Sources: cli.EnvVars("PAPERS_NO_AUTHN"), Destination: &x.noAuthn},
		&cli.StringFlag{Name: "dev-user-email", Category: "auth", Usage: "Email of the no-authn dev user", Value: "dev@localhost", Sources: cli.EnvVars("PAPERS_DEV_USER_EMAIL"), Destination: &x.devUserEmail},
		&cli.StringFlag{Name: "dev-user-name", Category: "auth", Usage: "Name of the no-authn dev user", Value: "Developer", Sources: cli.EnvVars("PAPERS_DEV_USER_NAME"), Destination: &x.devUserName},
	}
}

func (x Auth) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Bool("google", x.googleClientID != ""),
		slog.Any("allowed_domains", x.allowedDomains),
		slog.Int("allowed_emails", len(x.allowedEmails)),
		slog.Any("admin_emails", x.adminEmails),
		slog.Duration("session_ttl", x.sessionTTL),
		slog.Bool("no_authn", x.noAuthn),
	)
}

// Configure builds the authenticator. Exactly one mode must be chosen:
// Google OIDC or no-authn.
func (x *Auth) Configure(ctx context.Context, repo interfaces.Repository, baseURL string) (*usecase.Auth, error) {
	cfg := usecase.AuthConfig{
		AllowedDomains: x.allowedDomains, AllowedEmails: x.allowedEmails,
		AdminEmails: x.adminEmails, SessionTTL: x.sessionTTL,
	}

	switch {
	case x.noAuthn && x.googleClientID != "":
		return nil, goerr.New("--no-authn cannot be combined with Google sign-in")
	case x.noAuthn:
		// The dev user is a system administrator so a fresh instance is
		// usable immediately.
		cfg.AdminEmails = append(cfg.AdminEmails, x.devUserEmail)
		a, err := usecase.NewAuth(repo, nil, cfg)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to configure auth")
		}
		if err := a.EnableDevUser(ctx, x.devUserEmail, x.devUserName); err != nil {
			return nil, err
		}
		logging.Default().Warn("Authentication is DISABLED (--no-authn). Never use this in production.", "dev_user", x.devUserEmail)
		return a, nil
	case x.googleClientID != "":
		provider, err := google.New(google.Config{
			ClientID: x.googleClientID, ClientSecret: x.googleClientSecret,
			RedirectURL: baseURL + "/api/auth/callback", HostedDomain: x.googleHostedDomain,
		})
		if err != nil {
			return nil, goerr.Wrap(err, "failed to configure Google sign-in")
		}
		a, err := usecase.NewAuth(repo, provider, cfg)
		if err != nil {
			return nil, goerr.Wrap(err, "failed to configure auth")
		}
		return a, nil
	default:
		return nil, goerr.New("no authentication configured: set --google-client-id (or --no-authn for local development)")
	}
}
