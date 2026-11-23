package model_test

import (
	"errors"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func validAttachment(contentType string, size int64) *model.Attachment {
	return &model.Attachment{ID: model.NewAttachmentID(), WorkspaceID: "ws", PaperID: model.NewPaperID(), ContentType: contentType, Size: size, SHA256: "s"}
}

func TestAttachmentValidate(t *testing.T) {
	gt.NoError(t, validAttachment("image/png", model.MaxAttachmentSize).Validate())
	gt.Error(t, validAttachment("image/png", model.MaxAttachmentSize+1).Validate())

	// HTML documents are stored up to MaxHTMLSize
	gt.NoError(t, validAttachment(model.HTMLContentType, model.MaxHTMLSize).Validate())
	gt.True(t, errors.Is(validAttachment(model.HTMLContentType, model.MaxHTMLSize+1).Validate(), model.ErrInvalidArgument))
	gt.True(t, errors.Is(validAttachment("application/pdf", 10).Validate(), model.ErrInvalidArgument))
	gt.True(t, errors.Is(validAttachment(model.HTMLContentType, 0).Validate(), model.ErrInvalidArgument))

	gt.True(t, validAttachment("image/webp", 1).IsImage())
	gt.False(t, validAttachment(model.HTMLContentType, 1).IsImage())
	gt.True(t, validAttachment(model.HTMLContentType, 1).IsHTML())
	// only images can be uploaded
	gt.False(t, model.AllowedAttachmentTypes[model.HTMLContentType])
}
