package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	fs "cloud.google.com/go/firestore"
	"github.com/m-mizutani/gt"
	"github.com/urfave/cli/v3"

	papers "github.com/m-mizutani/papers/pkg/cli"
	"github.com/m-mizutani/papers/pkg/repository/firestore"
)

func runFirestore(t *testing.T, in io.Reader, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	app := &cli.Command{
		Name:     "papers",
		Commands: []*cli.Command{papers.CmdFirestore()},
		Reader:   in,
		Writer:   &out,
	}
	err := app.Run(context.Background(), append([]string{"papers", "firestore"}, args...))
	return out.String(), err
}

// failingReader fails the test when the confirmation is read.
type failingReader struct{ t *testing.T }

func (r failingReader) Read([]byte) (int, error) {
	r.t.Error("the confirmation must not be read")
	return 0, io.EOF
}

// stubReset replaces the database reset and counts calls.
func stubReset(t *testing.T) *int {
	t.Helper()
	calls := 0
	t.Cleanup(papers.SetResetFirestore(func(context.Context, string, string) (*firestore.ResetReport, error) {
		calls++
		return &firestore.ResetReport{Collections: []firestore.ResetCount{{Collection: "users", Documents: 3}}, Total: 3}, nil
	}))
	return &calls
}

func TestFirestoreCommands(t *testing.T) {
	// T15
	out, err := runFirestore(t, nil, "--help")
	gt.NoError(t, err)
	gt.S(t, out).Contains("migrate")
	gt.S(t, out).Contains("reset")
	gt.S(t, out).NotContains("setup")
}

func TestResetAborts(t *testing.T) {
	// T23: nothing but "yes" goes on, and the database is never reached.
	for _, in := range []string{"no\n", "y\n", "YES\n", "\n", ""} {
		calls := stubReset(t)
		out, err := runFirestore(t, strings.NewReader(in), "reset", "--firestore-project-id", "p")
		gt.True(t, errors.Is(err, papers.ErrResetAborted)).Describef("input %q", in)
		gt.Equal(t, *calls, 0)
		gt.S(t, out).Contains("This deletes every document in projects/p/databases/(default).")
		gt.S(t, out).Contains(`Type "yes" to continue: `)
	}
}

func TestResetConfirmed(t *testing.T) {
	// T19
	for _, in := range []string{"yes\n", "  yes  \n", "yes"} {
		calls := stubReset(t)
		out, err := runFirestore(t, strings.NewReader(in), "reset", "--firestore-project-id", "p", "--firestore-database-id", "db")
		gt.NoError(t, err)
		gt.Equal(t, *calls, 1)
		gt.S(t, out).Contains("projects/p/databases/db")
		gt.S(t, out).Contains("deleted  users  3 documents")
		gt.S(t, out).Contains("deleted 3 documents in total")
	}
}

func TestResetForced(t *testing.T) {
	// T22
	calls := stubReset(t)
	out, err := runFirestore(t, failingReader{t}, "reset", "--firestore-project-id", "p", "--force-dangerous-apply")
	gt.NoError(t, err)
	gt.Equal(t, *calls, 1)
	gt.S(t, out).NotContains(`Type "yes"`)
}

func TestResetOnEmulator(t *testing.T) {
	// T19 and T22 against the emulator, on a project of their own.
	projectID := os.Getenv("TEST_FIRESTORE_PROJECT_ID")
	if projectID == "" || os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("TEST_FIRESTORE_PROJECT_ID and FIRESTORE_EMULATOR_HOST are not set")
	}
	projectID += "-reset-cli"
	ctx := context.Background()
	client, err := fs.NewClient(ctx, projectID)
	gt.NoError(t, err).Required()
	t.Cleanup(func() { _ = client.Close() })

	seed := func() {
		_, err := client.Doc("users/u1").Set(ctx, map[string]any{"Name": "a"})
		gt.NoError(t, err).Required()
	}
	left := func() int {
		docs, err := client.Collection("users").Documents(ctx).GetAll()
		gt.NoError(t, err).Required()
		return len(docs)
	}

	seed()
	out, err := runFirestore(t, strings.NewReader("yes\n"), "reset", "--firestore-project-id", projectID)
	gt.NoError(t, err)
	gt.S(t, out).Contains("Firestore emulator: ")
	gt.S(t, out).Contains("deleted 1 documents in total")
	gt.Equal(t, left(), 0)

	seed()
	_, err = runFirestore(t, failingReader{t}, "reset", "--firestore-project-id", projectID, "--force-dangerous-apply")
	gt.NoError(t, err)
	gt.Equal(t, left(), 0)
}
