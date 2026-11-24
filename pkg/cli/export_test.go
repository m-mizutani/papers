package cli

import (
	"context"

	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/repository/firestore"
)

var ErrResetAborted = errResetAborted

// CmdFirestore is the firestore command for tests.
func CmdFirestore() *cli.Command { return cmdFirestore() }

// SetResetFirestore replaces the reset of the database and returns a
// function restoring it.
func SetResetFirestore(f func(ctx context.Context, projectID, databaseID string) (*firestore.ResetReport, error)) func() {
	prev := resetFirestore
	resetFirestore = f
	return func() { resetFirestore = prev }
}

// SetOpenRepository replaces the repository reset --demo writes through
// and returns a function restoring it.
func SetOpenRepository(f func(ctx context.Context, projectID, databaseID string) (interfaces.Repository, error)) func() {
	prev := openRepository
	openRepository = f
	return func() { openRepository = prev }
}

// NewAppForTest is the whole command tree.
func NewAppForTest() *cli.Command { return newApp("test") }
