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

// Defaults of the use case settings. Commands other than serve, such as
// firestore reset --demo, use them as they are.
const (
	defaultMaxOpsPerBatch = 300
	defaultPaperTouch     = time.Minute
	defaultTrashRetention = 30 * 24 * time.Hour
	defaultJobLease       = time.Minute
	defaultJobStep        = 200
)

// DefaultUseCaseConfig is the use case configuration serve has when no
// flag changes it.
func DefaultUseCaseConfig() usecase.Config {
	return usecase.Config{
		MaxOpsPerBatch:     defaultMaxOpsPerBatch,
		PaperTouchInterval: defaultPaperTouch,
		TrashRetention:     defaultTrashRetention,
		JobLease:           defaultJobLease,
		JobStep:            defaultJobStep,
	}
}

// Server holds the HTTP server and use case settings.
type Server struct {
	addr           string
	baseURL        string
	enableMCP      bool
	wsOrigins      []string
	maxOpsPerBatch int
	paperTouch     time.Duration
	trashRetention time.Duration
	jobLease       time.Duration
	jobStep        int
	jobPoll        time.Duration
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
		&cli.IntFlag{
			Name: "max-ops-per-batch", Category: "editing", Usage: "Upper bound of operations in one request (blocks per batch are capped at 240 regardless)",
			Value: defaultMaxOpsPerBatch, Sources: cli.EnvVars("PAPERS_MAX_OPS_PER_BATCH"), Destination: &x.maxOpsPerBatch,
		},
		&cli.DurationFlag{
			Name: "paper-touch-interval", Category: "editing",
			Usage: "Minimum interval between refreshes of a paper's updated time by writes (keeps the paper document off the write hot path)",
			Value: defaultPaperTouch, Sources: cli.EnvVars("PAPERS_PAPER_TOUCH_INTERVAL"), Destination: &x.paperTouch,
		},
		&cli.DurationFlag{
			Name: "trash-retention", Category: "editing", Usage: "How long papers stay in the trash before they are deleted for good",
			Value: defaultTrashRetention, Sources: cli.EnvVars("PAPERS_TRASH_RETENTION"), Destination: &x.trashRetention,
		},
		&cli.DurationFlag{
			Name: "job-lease", Category: "jobs", Usage: "How long an instance holds a background job between steps before another instance may take it over",
			Value: defaultJobLease, Sources: cli.EnvVars("PAPERS_JOB_LEASE"), Destination: &x.jobLease,
		},
		&cli.IntFlag{
			Name: "job-step", Category: "jobs", Usage: "Papers (or blocks of a copy) one step of a background job handles in one transaction",
			Value: defaultJobStep, Sources: cli.EnvVars("PAPERS_JOB_STEP"), Destination: &x.jobStep,
		},
		&cli.DurationFlag{
			Name: "job-poll", Category: "jobs", Usage: "How often the server looks for background jobs whose instance stopped",
			Value: 15 * time.Second, Sources: cli.EnvVars("PAPERS_JOB_POLL"), Destination: &x.jobPoll,
		},
	}
}

func (x Server) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("addr", x.addr),
		slog.String("base_url", x.baseURL),
		slog.Bool("enable_mcp", x.enableMCP),
		slog.Duration("trash_retention", x.trashRetention),
		slog.Duration("job_lease", x.jobLease),
		slog.Int("job_step", x.jobStep),
	)
}

func (x *Server) Addr() string               { return x.addr }
func (x *Server) BaseURL() string            { return strings.TrimRight(x.baseURL, "/") }
func (x *Server) EnableMCP() bool            { return x.enableMCP }
func (x *Server) SecureCookie() bool         { return strings.HasPrefix(x.baseURL, "https://") }
func (x *Server) WebSocketOrigins() []string { return x.wsOrigins }
func (x *Server) JobPoll() time.Duration     { return x.jobPoll }

func (x *Server) UseCaseConfig() usecase.Config {
	return usecase.Config{
		MaxOpsPerBatch:     x.maxOpsPerBatch,
		PaperTouchInterval: x.paperTouch,
		TrashRetention:     x.trashRetention,
		JobLease:           x.jobLease,
		JobStep:            x.jobStep,
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
