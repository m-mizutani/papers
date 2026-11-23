package firestore_test

import (
	"context"
	"errors"
	"os"
	"testing"

	fs "cloud.google.com/go/firestore"
	"github.com/m-mizutani/gt"
	"google.golang.org/api/iterator"

	"github.com/m-mizutani/papers/pkg/repository/firestore"
)

// resetProject is the emulator project the reset tests wipe. It differs from
// TEST_FIRESTORE_PROJECT_ID so that the repository contract tests, which run
// in parallel packages, keep their data. The tests run only against the
// emulator, never a real database.
func resetProject(t *testing.T, suffix string) string {
	t.Helper()
	projectID := os.Getenv("TEST_FIRESTORE_PROJECT_ID")
	if projectID == "" {
		t.Skip("TEST_FIRESTORE_PROJECT_ID is not set")
	}
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST is not set: reset tests run only on the emulator")
	}
	return projectID + "-reset" + suffix
}

func rootCollections(t *testing.T, client *fs.Client) []string {
	t.Helper()
	var ids []string
	it := client.Collections(context.Background())
	for {
		col, err := it.Next()
		if errors.Is(err, iterator.Done) {
			return ids
		}
		gt.NoError(t, err).Required()
		ids = append(ids, col.ID)
	}
}

func TestReset(t *testing.T) {
	ctx := context.Background()
	projectID := resetProject(t, "")
	client, err := fs.NewClient(ctx, projectID)
	gt.NoError(t, err).Required()
	t.Cleanup(func() { _ = client.Close() })

	t.Run("T17 empty database", func(t *testing.T) {
		report, err := firestore.Reset(ctx, projectID, "")
		gt.NoError(t, err).Required()
		gt.Equal(t, report.Total, 0)
	})

	t.Run("T16 deletes the whole tree", func(t *testing.T) {
		docs := map[string]map[string]any{
			"users/u1":                                    {"Name": "a"},
			"users/u1/pins/w1_p1":                         {"At": 1},
			"workspaces/w1":                               {"HomePaperID": "p1"},
			"workspaces/w1/papers/p1":                     {"Title": "t"},
			"workspaces/w1/papers/p1/blocks/b1":           {"Order": "a"},
			"workspaces/w1/papers/p1/blocks/b2":           {"Order": "b"},
			"workspaces/w1/papers/p1/versions/0000000001": {"Number": 1},
			// identities/google exists only as the parent of subjects.
			"identities/google/subjects/1234": {"UserID": "u1"},
		}
		for path, data := range docs {
			_, err := client.Doc(path).Set(ctx, data)
			gt.NoError(t, err).Required()
		}
		gt.A(t, rootCollections(t, client)).Length(3)

		report, err := firestore.Reset(ctx, projectID, "")
		gt.NoError(t, err).Required()
		gt.Equal(t, report.Total, len(docs))
		counts := map[string]int{}
		for _, c := range report.Collections {
			counts[c.Collection] = c.Documents
		}
		gt.Equal(t, counts["users"], 2)
		gt.Equal(t, counts["workspaces"], 5)
		gt.Equal(t, counts["identities"], 1)
		gt.A(t, rootCollections(t, client)).Length(0)
	})

	t.Run("T18 running again deletes nothing", func(t *testing.T) {
		report, err := firestore.Reset(ctx, projectID, "")
		gt.NoError(t, err).Required()
		gt.Equal(t, report.Total, 0)
	})
}

func TestResetFlushesInBatches(t *testing.T) {
	ctx := context.Background()
	projectID := resetProject(t, "-batch")
	client, err := fs.NewClient(ctx, projectID)
	gt.NoError(t, err).Required()
	t.Cleanup(func() { _ = client.Close() })

	// More documents than one flush holds.
	bw := client.BulkWriter(ctx)
	const n = 1200
	for i := range n {
		_, err := bw.Set(client.Collection("tokens").NewDoc(), map[string]any{"I": i})
		gt.NoError(t, err).Required()
	}
	bw.End()

	report, err := firestore.Reset(ctx, projectID, "")
	gt.NoError(t, err).Required()
	gt.Equal(t, report.Total, n)
	gt.A(t, rootCollections(t, client)).Length(0)
}
