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
	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/repository/firestore"
	"github.com/m-mizutani/papers/pkg/repository/memory"
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

// useMemoryRepository makes reset --demo write through repo and fails the
// test when the repository is opened although it should not be.
func useMemoryRepository(t *testing.T, repo *memory.Repository) *int {
	t.Helper()
	opened := 0
	t.Cleanup(papers.SetOpenRepository(func(context.Context, string, string) (interfaces.Repository, error) {
		opened++
		return repo, nil
	}))
	return &opened
}

// stubResetChecking replaces the database reset; check runs at the time of
// the reset.
func stubResetChecking(t *testing.T, check func()) *int {
	t.Helper()
	calls := 0
	t.Cleanup(papers.SetResetFirestore(func(context.Context, string, string) (*firestore.ResetReport, error) {
		calls++
		check()
		return &firestore.ResetReport{Collections: []firestore.ResetCount{{Collection: "papers", Documents: 2}}, Total: 2}, nil
	}))
	return &calls
}

const exampleConfig = "../../examples/config"

func demoArgs(t *testing.T, extra ...string) []string {
	t.Helper()
	return append([]string{"reset", "--firestore-project-id", "p", "--force-dangerous-apply",
		"--config", exampleConfig, "--attachment-backend", "dir", "--attachment-dir", t.TempDir()}, extra...)
}

func workspaceRecords(t *testing.T, repo *memory.Repository) int {
	t.Helper()
	recs, err := repo.ListWorkspaceRecords(context.Background())
	gt.NoError(t, err).Required()
	return len(recs)
}

func TestResetDemoWritesTheSamplesAfterDeleting(t *testing.T) {
	// T3: the homes are created after the reset, and each workspace reports
	// what was written after the deleted documents.
	repo := memory.New()
	opened := useMemoryRepository(t, repo)
	calls := stubResetChecking(t, func() { gt.Equal(t, workspaceRecords(t, repo), 0) })

	out, err := runFirestore(t, failingReader{t}, demoArgs(t, "--demo")...)
	gt.NoError(t, err)
	gt.Equal(t, *calls, 1)
	gt.Equal(t, *opened, 1)
	gt.Equal(t, workspaceRecords(t, repo), 2)
	gt.S(t, out).Contains("seeded   product  4 sample papers\n")
	gt.S(t, out).Contains("seeded   security  4 sample papers\n")
	gt.True(t, strings.Index(out, "deleted 2 documents in total") < strings.Index(out, "seeded   "))
}

func TestResetDemoFromTheEnvironment(t *testing.T) {
	// T4
	t.Setenv("PAPERS_DEMO", "true")
	repo := memory.New()
	useMemoryRepository(t, repo)
	stubResetChecking(t, func() {})

	out, err := runFirestore(t, failingReader{t}, demoArgs(t)...)
	gt.NoError(t, err)
	gt.S(t, out).Contains("seeded   product  4 sample papers")
}

func TestResetDemoStopsBeforeDeleting(t *testing.T) {
	bad := t.TempDir()
	gt.NoError(t, os.WriteFile(bad+"/broken.toml", []byte("[[[ not toml"), 0o600)).Required()
	for name, args := range map[string][]string{
		// T5
		"no config": {"reset", "--firestore-project-id", "p", "--force-dangerous-apply", "--demo",
			"--attachment-backend", "dir", "--attachment-dir", t.TempDir()},
		// T6
		"memory attachments": {"reset", "--firestore-project-id", "p", "--force-dangerous-apply", "--demo",
			"--config", exampleConfig},
		// T7
		"broken config": {"reset", "--firestore-project-id", "p", "--force-dangerous-apply", "--demo",
			"--config", bad, "--attachment-backend", "dir", "--attachment-dir", t.TempDir()},
	} {
		t.Run(name, func(t *testing.T) {
			opened := useMemoryRepository(t, memory.New())
			calls := stubReset(t)
			out, err := runFirestore(t, failingReader{t}, args...)
			gt.Error(t, err)
			gt.Equal(t, *calls, 0)
			gt.Equal(t, *opened, 0)
			gt.S(t, out).NotContains("This deletes")
		})
	}
}

func TestResetDemoMemoryAttachmentsMessage(t *testing.T) {
	// T6: the message names the backends that work.
	useMemoryRepository(t, memory.New())
	stubReset(t)
	_, err := runFirestore(t, failingReader{t}, "reset", "--firestore-project-id", "p", "--force-dangerous-apply", "--demo", "--config", exampleConfig)
	gt.Error(t, err)
	gt.S(t, err.Error()).Contains("--demo needs an attachment backend that keeps files (gcs or dir)")
}

func TestResetDemoAborted(t *testing.T) {
	// T8: an aborted confirmation deletes nothing and writes nothing.
	repo := memory.New()
	useMemoryRepository(t, repo)
	calls := stubReset(t)
	args := demoArgs(t, "--demo")
	args = append(args[:3], args[4:]...) // without --force-dangerous-apply
	out, err := runFirestore(t, strings.NewReader("no\n"), args...)
	gt.True(t, errors.Is(err, papers.ErrResetAborted))
	gt.Equal(t, *calls, 0)
	gt.Equal(t, workspaceRecords(t, repo), 0)
	gt.S(t, out).NotContains("seeded")
}

func TestResetWithoutDemoNeedsNoConfiguration(t *testing.T) {
	// T9: without --demo nothing is opened or written, and the workspace
	// and attachment flags are not read.
	opened := useMemoryRepository(t, memory.New())
	calls := stubReset(t)
	out, err := runFirestore(t, failingReader{t}, "reset", "--firestore-project-id", "p", "--force-dangerous-apply",
		"--config", t.TempDir()+"/missing", "--attachment-backend", "nope")
	gt.NoError(t, err)
	gt.Equal(t, *calls, 1)
	gt.Equal(t, *opened, 0)
	gt.S(t, out).NotContains("seeded")
	gt.S(t, out).NotContains("skipped")
}

func TestResetDemoSkipsAWorkspaceInUse(t *testing.T) {
	// T13: a workspace that is not empty after the reset is reported as
	// skipped. The stubbed reset keeps the samples of the first run.
	repo := memory.New()
	useMemoryRepository(t, repo)
	stubResetChecking(t, func() {})
	_, err := runFirestore(t, failingReader{t}, demoArgs(t, "--demo")...)
	gt.NoError(t, err).Required()

	out, err := runFirestore(t, failingReader{t}, demoArgs(t, "--demo")...)
	gt.NoError(t, err)
	gt.S(t, out).Contains("skipped  product  not empty\n")
	gt.S(t, out).Contains("skipped  security  not empty\n")
}

func TestResetDemoOnEmulator(t *testing.T) {
	// T10: against the emulator, reset --demo replaces the data with the
	// homes and the sample papers.
	projectID := os.Getenv("TEST_FIRESTORE_PROJECT_ID")
	if projectID == "" || os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("TEST_FIRESTORE_PROJECT_ID and FIRESTORE_EMULATOR_HOST are not set")
	}
	projectID += "-reset-demo-cli"
	ctx := context.Background()
	client, err := fs.NewClient(ctx, projectID)
	gt.NoError(t, err).Required()
	t.Cleanup(func() { _ = client.Close() })
	_, err = client.Doc("users/u1").Set(ctx, map[string]any{"Name": "a"})
	gt.NoError(t, err).Required()

	out, err := runFirestore(t, failingReader{t}, "reset", "--firestore-project-id", projectID, "--force-dangerous-apply",
		"--demo", "--config", exampleConfig, "--attachment-backend", "dir", "--attachment-dir", t.TempDir())
	gt.NoError(t, err).Required()
	gt.S(t, out).Contains("seeded   product  4 sample papers")
	gt.S(t, out).Contains("seeded   security  4 sample papers")

	users, err := client.Collection("users").Documents(ctx).GetAll()
	gt.NoError(t, err).Required()
	gt.Equal(t, len(users), 0)

	repo, err := firestore.New(ctx, projectID, "(default)")
	gt.NoError(t, err).Required()
	t.Cleanup(func() { _ = repo.Close() })
	for _, ws := range []model.WorkspaceID{"product", "security"} {
		rec, err := repo.GetWorkspaceRecord(ctx, ws)
		gt.NoError(t, err).Required()
		blocks, _, err := repo.ListBlocksRange(ctx, ws, rec.HomePaperID, "", 1)
		gt.NoError(t, err).Required()
		gt.A(t, blocks).Length(1)
		papers, _, err := repo.ListPapers(ctx, ws, model.PaperQuery{Sort: model.SortCreated, Limit: 10})
		gt.NoError(t, err).Required()
		gt.A(t, papers).Length(5) // the home and four samples
	}
}
