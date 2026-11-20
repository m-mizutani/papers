package config_test

import (
	"context"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/cli/config"
)

func TestRepositoryConfigure(t *testing.T) {
	repo, bus, err := config.NewRepositoryForTest("memory", 8).Configure(context.Background())
	gt.NoError(t, err)
	gt.NotNil(t, repo)
	gt.NotNil(t, bus)

	_, _, err = config.NewRepositoryForTest("firestore", 8).Configure(context.Background())
	gt.Error(t, err)
	_, _, err = config.NewRepositoryForTest("memory", 0).Configure(context.Background())
	gt.Error(t, err)
}
