package config

import (
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/papers/pkg/usecase"
)

// Server holds the HTTP server and use case settings.
type Server struct {
	addr              string
	baseURL           string
	enableMCP         bool
	wsOrigins         []string
	workspaceCreation string
	maxOpsPerCommit   int
	paperTouch        time.Duration
}

func (x *Server) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name: "addr", Category: "server", Usage: "Listen address",
			Value: ":8080", Sources: cli.EnvVars("PAPERS_ADDR"), Destination: &x.addr,
		},
		&cli.StringFlag{
			Name: "base-url", Category: "server",
			Usage: "Public URL of the service (e.g. https://papers.example.com). Used for the OAuth redirect and Secure cookies",
			Value: "http://localhost:8080", Sources: cli.EnvVars("PAPERS_BASE_URL"), Destination: &x.baseURL,
		},
		&cli.BoolFlag{
			Name: "enable-mcp", Category: "server", Usage: "Serve the MCP endpoint at /mcp",
			Value: true, Sources: cli.EnvVars("PAPERS_ENABLE_MCP"), Destination: &x.enableMCP,
		},
		&cli.StringSliceFlag{
			Name: "ws-origin", Category: "server",
			Usage:   "Extra Origin host patterns accepted for WebSocket upgrades (e.g. localhost:5173 for the Vite dev server)",
			Sources: cli.EnvVars("PAPERS_WS_ORIGINS"), Destination: &x.wsOrigins,
		},
		&cli.StringFlag{
			Name: "workspace-creation", Category: "editing", Usage: "Who may create workspaces [everyone|admin]",
			Value: string(usecase.WorkspaceCreationEveryone), Sources: cli.EnvVars("PAPERS_WORKSPACE_CREATION"), Destination: &x.workspaceCreation,
		},
		&cli.IntFlag{
			Name: "max-ops-per-commit", Category: "editing", Usage: "Upper bound of operations in one commit",
			Value: 200, Sources: cli.EnvVars("PAPERS_MAX_OPS_PER_COMMIT"), Destination: &x.maxOpsPerCommit,
		},
		&cli.DurationFlag{
			Name: "paper-touch-interval", Category: "editing",
			Usage: "Minimum interval between refreshes of a paper's updated time by commits (keeps the paper record off the write hot path)",
			Value: time.Minute, Sources: cli.EnvVars("PAPERS_PAPER_TOUCH_INTERVAL"), Destination: &x.paperTouch,
		},
	}
}

func (x Server) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("addr", x.addr),
		slog.String("base_url", x.baseURL),
		slog.Bool("enable_mcp", x.enableMCP),
		slog.String("workspace_creation", x.workspaceCreation),
	)
}

func (x *Server) Addr() string               { return x.addr }
func (x *Server) BaseURL() string            { return strings.TrimRight(x.baseURL, "/") }
func (x *Server) EnableMCP() bool            { return x.enableMCP }
func (x *Server) SecureCookie() bool         { return strings.HasPrefix(x.baseURL, "https://") }
func (x *Server) WebSocketOrigins() []string { return x.wsOrigins }

func (x *Server) UseCaseConfig() usecase.Config {
	return usecase.Config{
		WorkspaceCreation:  usecase.WorkspaceCreation(x.workspaceCreation),
		MaxOpsPerCommit:    x.maxOpsPerCommit,
		PaperTouchInterval: x.paperTouch,
	}
}

// Validate checks values the flag parser cannot.
func (x *Server) Validate() error {
	u, err := url.Parse(x.baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return goerr.New("base-url must be an absolute http(s) URL", goerr.V("base_url", x.baseURL))
	}
	return nil
}
