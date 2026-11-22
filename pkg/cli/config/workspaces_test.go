package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/cli/config"
)

func TestWorkspacesConfigure(t *testing.T) {
	ctx := context.Background()
	_, err := config.NewWorkspacesForTest("", "").Configure(ctx)
	gt.Error(t, err)

	dir := t.TempDir()
	gt.NoError(t, os.WriteFile(filepath.Join(dir, "a.toml"), []byte("[workspace]\nid = \"a\"\n"), 0o600))
	r := gt.R1(config.NewWorkspacesForTest(dir, "").Configure(ctx)).NoError(t)
	gt.A(t, r.List()).Length(1)
}

func TestAttachmentsConfigure(t *testing.T) {
	ctx := context.Background()
	gt.R1(config.NewAttachmentsForTest("memory", "").Configure(ctx)).NoError(t)
	gt.R1(config.NewAttachmentsForTest("dir", t.TempDir()).Configure(ctx)).NoError(t)
	_, err := config.NewAttachmentsForTest("dir", "").Configure(ctx)
	gt.Error(t, err)
	_, err = config.NewAttachmentsForTest("s3", "").Configure(ctx)
	gt.Error(t, err)
}
