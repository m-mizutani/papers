package config_test

import (
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/cli/config"
)

func TestServer(t *testing.T) {
	s := config.NewServerForTest("https://papers.example.com/")
	gt.NoError(t, s.Validate())
	gt.True(t, s.SecureCookie())
	gt.Equal(t, s.BaseURL(), "https://papers.example.com")
	gt.Equal(t, s.UseCaseConfig().MaxOpsPerBatch, 1)

	gt.False(t, config.NewServerForTest("http://localhost:8080").SecureCookie())
	gt.Error(t, config.NewServerForTest("papers.example.com").Validate())
	gt.Error(t, config.NewServerForTest("ftp://x").Validate())
}
