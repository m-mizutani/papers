package config

import (
	"context"
	"log/slog"

	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/repository/memory"
	"github.com/m-mizutani/papers/pkg/utils/logging"
)

// Repository selects the persistence backend. Only the in-memory backend
// exists today; the production backend (Firestore or AlloyDB/PostgreSQL) is
// added behind the same interfaces, see docs/design.md.
type Repository struct {
	backend     string
	eventBuffer int
}

func (x *Repository) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name: "repository-backend", Category: "repository", Usage: "Repository backend [memory]",
			Value: "memory", Sources: cli.EnvVars("PAPERS_REPOSITORY_BACKEND"), Destination: &x.backend,
		},
		&cli.IntFlag{
			Name: "event-buffer", Category: "repository", Usage: "Per-subscriber realtime event buffer",
			Value: 256, Sources: cli.EnvVars("PAPERS_EVENT_BUFFER"), Destination: &x.eventBuffer,
		},
	}
}

func (x Repository) LogValue() slog.Value {
	return slog.GroupValue(slog.String("backend", x.backend))
}

func (x *Repository) Configure(_ context.Context) (interfaces.Repository, interfaces.EventBus, error) {
	if x.eventBuffer <= 0 {
		return nil, nil, goerr.New("event-buffer must be positive")
	}
	switch x.backend {
	case "memory":
		logging.Default().Warn("Using the in-memory repository: data is lost on restart and the server must run as a single instance")
		return memory.New(), memory.NewEventBus(x.eventBuffer), nil
	default:
		return nil, nil, goerr.New("unsupported repository backend", goerr.V("backend", x.backend))
	}
}
