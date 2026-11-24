package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/papers/pkg/cli/config"
	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/repository/firestore"
	"github.com/m-mizutani/papers/pkg/usecase"
	"github.com/m-mizutani/papers/pkg/utils/safe"
)

// resetFirestore is replaced in tests to observe whether reset reached the
// database.
var resetFirestore = firestore.Reset

// openRepository opens the repository reset --demo writes the sample papers
// through; tests replace it with the memory repository.
var openRepository = func(ctx context.Context, projectID, databaseID string) (interfaces.Repository, error) {
	return firestore.New(ctx, projectID, databaseID)
}

// errResetAborted is returned when the reset confirmation is not "yes".
var errResetAborted = errors.New(`reset aborted: confirmation was not "yes"`)

type firestoreTarget struct {
	projectID  string
	databaseID string
}

func (t *firestoreTarget) flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name: "firestore-project-id", Usage: "Google Cloud project of the Firestore database", Required: true,
			Sources: cli.EnvVars("PAPERS_FIRESTORE_PROJECT_ID"), Destination: &t.projectID,
		},
		&cli.StringFlag{
			Name: "firestore-database-id", Usage: "Firestore database ID", Value: "(default)",
			Sources: cli.EnvVars("PAPERS_FIRESTORE_DATABASE_ID"), Destination: &t.databaseID,
		},
	}
}

func (t *firestoreTarget) String() string {
	return fmt.Sprintf("projects/%s/databases/%s", t.projectID, t.databaseID)
}

func cmdFirestore() *cli.Command {
	return &cli.Command{
		Name:     "firestore",
		Usage:    "Manage the Firestore database",
		Commands: []*cli.Command{cmdFirestoreMigrate(), cmdFirestoreReset()},
	}
}

func cmdFirestoreMigrate() *cli.Command {
	var target firestoreTarget
	var opts firestore.MigrateOptions
	var noWait bool
	var waitTimeout time.Duration
	return &cli.Command{
		Name:  "migrate",
		Usage: "Create the indexes and TTL policies papers needs on a Firestore Enterprise database; safe to run repeatedly",
		Flags: append(target.flags(),
			&cli.BoolFlag{Name: "dry-run", Usage: "Only read, and report what would be created or deleted", Sources: cli.EnvVars("PAPERS_DRY_RUN"), Destination: &opts.DryRun},
			&cli.BoolFlag{Name: "no-wait", Usage: "Do not wait for the indexes to become ready", Sources: cli.EnvVars("PAPERS_NO_WAIT"), Destination: &noWait},
			&cli.DurationFlag{Name: "wait-timeout", Usage: "How long to wait for the indexes to become ready", Value: 30 * time.Minute, Sources: cli.EnvVars("PAPERS_WAIT_TIMEOUT"), Destination: &waitTimeout},
			&cli.BoolFlag{Name: "prune", Usage: "Delete indexes of the managed collection groups that papers does not need", Sources: cli.EnvVars("PAPERS_PRUNE"), Destination: &opts.Prune},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			m, err := firestore.NewMigrator(ctx, target.projectID, target.databaseID)
			if err != nil {
				return err
			}
			defer safe.Close(ctx, m)
			w := c.Root().Writer

			report, err := m.Apply(ctx, opts)
			if err != nil {
				return err
			}
			printMigrateReport(w, report)

			if pending := report.Pending(); !opts.DryRun && !noWait && len(pending) > 0 {
				_, _ = fmt.Fprintf(w, "waiting for %d indexes to become ready...\n", len(pending))
				err := m.Wait(ctx, report, waitTimeout)
				for _, res := range pending {
					printIndex(w, res)
				}
				if err != nil {
					return err
				}
			}
			if n := report.Failed(); n > 0 {
				return goerr.New("migration incomplete: indexes or TTL policies failed", goerr.V("failed", n))
			}
			return nil
		},
	}
}

func printMigrateReport(w io.Writer, report *firestore.MigrateReport) {
	for _, res := range report.Indexes {
		printIndex(w, res)
	}
	for _, res := range report.TTL {
		line := fmt.Sprintf("ttl   %-9s %s", res.Status, res)
		if res.Detail != "" {
			line += "  (" + res.Detail + ")"
		}
		_, _ = fmt.Fprintln(w, line)
	}
}

func printIndex(w io.Writer, res *firestore.IndexResult) {
	line := fmt.Sprintf("index %-9s %s", res.Status, res.Description)
	if res.Spec != nil {
		line += "  [" + res.Spec.Query + "]"
	}
	if res.Detail != "" {
		line += "  (" + res.Detail + ")"
	}
	_, _ = fmt.Fprintln(w, line)
}

func cmdFirestoreReset() *cli.Command {
	var (
		target      firestoreTarget
		force       bool
		demo        bool
		workspaces  config.Workspaces
		attachments config.Attachments
	)
	flags := append(target.flags(),
		&cli.BoolFlag{Name: "force-dangerous-apply", Usage: "Delete without asking for confirmation", Sources: cli.EnvVars("PAPERS_FORCE_DANGEROUS_APPLY"), Destination: &force},
		&cli.BoolFlag{Name: "demo", Usage: "After deleting, create each workspace's home and write the sample papers (needs --config and a gcs or dir attachment backend)", Sources: cli.EnvVars("PAPERS_DEMO"), Destination: &demo},
	)
	flags = append(flags, workspaces.Flags()...)
	flags = append(flags, attachments.Flags()...)
	return &cli.Command{
		Name:  "reset",
		Usage: "Delete every document in the Firestore database (indexes and TTL policies are kept); with --demo, write the sample papers afterwards",
		Flags: flags,
		Action: func(ctx context.Context, c *cli.Command) error {
			w := c.Root().Writer

			// Everything --demo needs that can fail is prepared before
			// anything is deleted.
			var uc *usecase.UseCases
			if demo {
				var closeRepo func()
				var err error
				uc, closeRepo, err = prepareSamples(ctx, &target, &workspaces, &attachments)
				if err != nil {
					return err
				}
				defer closeRepo()
			}

			_, _ = fmt.Fprintf(w, "This deletes every document in %s.\n", &target)
			if host := os.Getenv("FIRESTORE_EMULATOR_HOST"); host != "" {
				_, _ = fmt.Fprintf(w, "Firestore emulator: %s\n", host)
			}
			_, _ = fmt.Fprintln(w, "Indexes and TTL policies are kept. Stop the papers server first.")
			if !force {
				if err := confirmReset(c.Root().Reader, w); err != nil {
					return err
				}
			}

			report, err := resetFirestore(ctx, target.projectID, target.databaseID)
			if err != nil {
				return err
			}
			for _, col := range report.Collections {
				_, _ = fmt.Fprintf(w, "deleted  %s  %d documents\n", col.Collection, col.Documents)
			}
			_, _ = fmt.Fprintf(w, "deleted %d documents in total\n", report.Total)

			if uc == nil {
				return nil
			}
			if err := uc.Init(ctx); err != nil {
				return err
			}
			results, err := uc.SeedSamples(ctx)
			printSeedResults(w, results)
			return err
		},
	}
}

// prepareSamples loads the workspace configuration, the attachment store and
// the repository, and builds the use cases that write the sample papers.
func prepareSamples(ctx context.Context, target *firestoreTarget, workspaces *config.Workspaces, attachments *config.Attachments) (*usecase.UseCases, func(), error) {
	registry, err := workspaces.Configure(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !attachments.KeepsFiles() {
		return nil, nil, goerr.New("--demo needs an attachment backend that keeps files (gcs or dir); memory loses the sample images when reset exits")
	}
	store, err := attachments.Configure(ctx)
	if err != nil {
		return nil, nil, err
	}
	repo, err := openRepository(ctx, target.projectID, target.databaseID)
	if err != nil {
		return nil, nil, err
	}
	closeRepo := func() { safe.Close(ctx, repo) }
	uc, err := usecase.New(repo, registry, store, config.DefaultUseCaseConfig())
	if err != nil {
		closeRepo()
		return nil, nil, goerr.Wrap(err, "failed to build use cases")
	}
	return uc, closeRepo, nil
}

func printSeedResults(w io.Writer, results []usecase.SeedResult) {
	for _, r := range results {
		if r.Seeded {
			_, _ = fmt.Fprintf(w, "seeded   %s  %d sample papers\n", r.Workspace, r.Papers)
		} else {
			_, _ = fmt.Fprintf(w, "skipped  %s  not empty\n", r.Workspace)
		}
	}
}

// confirmReset reads one line and accepts exactly "yes" (surrounding
// spaces aside). Anything else, an empty line and the end of input abort.
func confirmReset(r io.Reader, w io.Writer) error {
	_, _ = fmt.Fprint(w, `Type "yes" to continue: `)
	if r == nil {
		r = os.Stdin
	}
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return goerr.Wrap(err, "failed to read confirmation")
	}
	if strings.TrimSpace(line) != "yes" {
		_, _ = fmt.Fprintln(w)
		return errResetAborted
	}
	return nil
}
