package repository_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/repository/memory"
)

func runRepositoryTest(t *testing.T, name string, fn func(t *testing.T, repo interfaces.Repository)) {
	t.Helper()
	t.Run(name+"/memory", func(t *testing.T) {
		fn(t, memory.New())
	})
}

func runEventBusTest(t *testing.T, name string, fn func(t *testing.T, bus interfaces.EventBus)) {
	t.Helper()
	t.Run(name+"/memory", func(t *testing.T) {
		fn(t, memory.NewEventBus(16))
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
