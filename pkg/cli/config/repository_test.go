package config_test

import (
	"context"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/cli/config"
)

func TestRepositoryConfigure(t *testing.T) {
	repo, err := config.NewRepositoryForTest("memory", "", 8).Configure(context.Background())
	gt.NoError(t, err)
	gt.NotNil(t, repo)

	_, err = config.NewRepositoryForTest("firestore", "", 8).Configure(context.Background())
	gt.Error(t, err) // project ID required
	_, err = config.NewRepositoryForTest("bigtable", "", 8).Configure(context.Background())
	gt.Error(t, err)
	_, err = config.NewRepositoryForTest("memory", "", 0).Configure(context.Background())
	gt.Error(t, err)
}
