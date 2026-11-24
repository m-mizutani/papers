package config

import (
	"context"
	"log/slog"

	"github.com/m-mizutani/goerr/v2"
	"github.com/urfave/cli/v3"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/service/attachment"
	"github.com/m-mizutani/papers/pkg/utils/logging"
)

// Attachments selects where attached images are stored (D8).
type Attachments struct {
	backend string
	dir     string
	bucket  string
	prefix  string
}

func (x *Attachments) Flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name: "attachment-backend", Category: "attachments", Usage: "Attachment store [gcs|dir|memory]",
			Value: "memory", Sources: cli.EnvVars("PAPERS_ATTACHMENT_BACKEND"), Destination: &x.backend,
		},
		&cli.StringFlag{
			Name: "attachment-dir", Category: "attachments", Usage: "Directory for the dir backend",
			Sources: cli.EnvVars("PAPERS_ATTACHMENT_DIR"), Destination: &x.dir,
		},
		&cli.StringFlag{
			Name: "attachment-bucket", Category: "attachments", Usage: "Cloud Storage bucket for the gcs backend",
			Sources: cli.EnvVars("PAPERS_ATTACHMENT_BUCKET"), Destination: &x.bucket,
		},
		&cli.StringFlag{
			Name: "attachment-prefix", Category: "attachments", Usage: "Object name prefix in the bucket",
			Sources: cli.EnvVars("PAPERS_ATTACHMENT_PREFIX"), Destination: &x.prefix,
		},
	}
}

func (x Attachments) LogValue() slog.Value {
	return slog.GroupValue(slog.String("backend", x.backend), slog.String("dir", x.dir), slog.String("bucket", x.bucket))
}

// KeepsFiles reports whether the selected store keeps files after the
// process exits (every backend but memory).
func (x *Attachments) KeepsFiles() bool { return x.backend != "memory" }

func (x *Attachments) Configure(ctx context.Context) (interfaces.AttachmentStore, error) {
	switch x.backend {
	case "memory":
		logging.Default().Warn("Using the in-memory attachment store: images are lost on restart")
		return attachment.NewMemory(), nil
	case "dir":
		return attachment.NewDir(x.dir)
	case "gcs":
		return attachment.NewGCS(ctx, x.bucket, x.prefix)
	default:
		return nil, goerr.New("unsupported attachment backend", goerr.V("backend", x.backend))
	}
}
