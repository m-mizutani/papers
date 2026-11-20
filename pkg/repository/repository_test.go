package repository_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/repository/firestore"
	"github.com/m-mizutani/papers/pkg/repository/memory"
)

// runRepositoryTest runs fn against every backend. The Firestore backend
// runs when TEST_FIRESTORE_PROJECT_ID is set (use the emulator via
// FIRESTORE_EMULATOR_HOST, see `task test:firestore`).
func runRepositoryTest(t *testing.T, name string, fn func(t *testing.T, repo interfaces.Repository)) {
	t.Helper()
	t.Run(name+"/memory", func(t *testing.T) {
		fn(t, memory.New(memory.WithEventBuffer(64)))
	})
	t.Run(name+"/firestore", func(t *testing.T) {
		projectID := os.Getenv("TEST_FIRESTORE_PROJECT_ID")
		if projectID == "" {
			t.Skip("TEST_FIRESTORE_PROJECT_ID is not set")
		}
		repo, err := firestore.New(context.Background(), projectID, os.Getenv("TEST_FIRESTORE_DATABASE_ID"), firestore.WithEventBuffer(64))
		gt.NoError(t, err).Required()
		t.Cleanup(func() { gt.NoError(t, repo.Close()) })
		fn(t, repo)
	})
}

func uniq(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func humanActor(id model.UserID) model.Actor {
	return model.Actor{Kind: model.ActorHuman, UserID: id, Name: string(id)}
}

// timeEq compares timestamps with a tolerance for backend precision.
func timeEq(a, b time.Time) bool {
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return d < time.Millisecond
}
