package cli

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/papers/pkg/repository/firestore"
)

func cmdFirestore() *cli.Command {
	var projectID, databaseID string
	var dryRun bool
	return &cli.Command{
		Name:  "firestore",
		Usage: "Manage the Firestore database",
		Commands: []*cli.Command{{
			Name:  "setup",
			Usage: "Check that the database is a Firestore Enterprise database and create missing indexes and TTL policies",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name: "firestore-project-id", Usage: "Google Cloud project of the Firestore database", Required: true,
					Sources: cli.EnvVars("PAPERS_FIRESTORE_PROJECT_ID"), Destination: &projectID,
				},
				&cli.StringFlag{
					Name: "firestore-database-id", Usage: "Firestore database ID", Value: "(default)",
					Sources: cli.EnvVars("PAPERS_FIRESTORE_DATABASE_ID"), Destination: &databaseID,
				},
				&cli.BoolFlag{Name: "dry-run", Usage: "Report what is missing without creating it", Destination: &dryRun},
			},
			Action: func(ctx context.Context, c *cli.Command) error {
				report, err := firestore.Setup(ctx, projectID, databaseID, dryRun)
				if err != nil {
					return err
				}
				verb := "created"
				if dryRun {
					verb = "missing"
				}
				w := c.Root().Writer
				for _, s := range report.Existing {
					_, _ = fmt.Fprintf(w, "index ok       %s\n", s)
				}
				for _, s := range report.Created {
					_, _ = fmt.Fprintf(w, "index %-8s %s  [%s]\n", verb, s, s.Query)
				}
				for _, f := range report.TTL {
					_, _ = fmt.Fprintf(w, "ttl   %-8s %s\n", verb, f)
				}
				if !dryRun && len(report.Created) > 0 {
					_, _ = fmt.Fprintln(w, "Index builds run on the server; check their state in the Cloud Console before serving traffic.")
				}
				return nil
			},
		}},
	}
}
