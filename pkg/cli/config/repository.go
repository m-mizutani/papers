package config

import (
	"context"
	"log/slog"

	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/repository/firestore"
	"github.com/m-mizutani/papers/pkg/repository/memory"
	"github.com/m-mizutani/papers/pkg/utils/logging"
)

// Repository selects the persistence backend.
type Repository struct {
	backend     string
	projectID   string
	databaseID  string
	eventBuffer int
	searchLang  string
}

func (x *Repository) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name: "repository-backend", Category: "repository", Usage: "Repository backend [firestore|memory]",
			Value: "memory", Sources: cli.EnvVars("PAPERS_REPOSITORY_BACKEND"), Destination: &x.backend,
		},
		&cli.StringFlag{
			Name: "firestore-project-id", Category: "repository", Usage: "Google Cloud project of the Firestore database",
			Sources: cli.EnvVars("PAPERS_FIRESTORE_PROJECT_ID"), Destination: &x.projectID,
		},
		&cli.StringFlag{
			Name: "firestore-database-id", Category: "repository", Usage: "Firestore database ID",
			Value: "(default)", Sources: cli.EnvVars("PAPERS_FIRESTORE_DATABASE_ID"), Destination: &x.databaseID,
		},
		&cli.StringFlag{
			Name: "firestore-search-language", Category: "repository", Usage: "BCP-47 language code of search queries (e.g. ja); empty lets Firestore detect it",
			Sources: cli.EnvVars("PAPERS_FIRESTORE_SEARCH_LANGUAGE"), Destination: &x.searchLang,
		},
		&cli.IntFlag{
			Name: "event-buffer", Category: "repository", Usage: "Realtime events a watcher may lag behind before it is told to reload",
			Value: 256, Sources: cli.EnvVars("PAPERS_EVENT_BUFFER"), Destination: &x.eventBuffer,
		},
	}
}

func (x Repository) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("backend", x.backend),
		slog.String("firestore_project_id", x.projectID),
		slog.String("firestore_database_id", x.databaseID),
		slog.String("firestore_search_language", x.searchLang),
	)
}

func (x *Repository) Configure(ctx context.Context) (interfaces.Repository, error) {
	if x.eventBuffer <= 0 {
		return nil, goerr.New("event-buffer must be positive")
	}
	switch x.backend {
	case "memory":
		logging.Default().Warn("Using the in-memory repository: data is lost on restart and the server must run as a single instance")
		return memory.New(memory.WithEventBuffer(x.eventBuffer)), nil
	case "firestore":
		if x.projectID == "" {
			return nil, goerr.New("--firestore-project-id is required for the firestore backend")
		}
		repo, err := firestore.New(ctx, x.projectID, x.databaseID,
			firestore.WithEventBuffer(x.eventBuffer), firestore.WithSearchLanguage(x.searchLang))
		if err != nil {
			return nil, err
		}
		return repo, nil
	default:
		return nil, goerr.New("unsupported repository backend", goerr.V("backend", x.backend))
	}
}
