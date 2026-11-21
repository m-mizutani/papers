package usecase_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
)

func TestSearchScopeAndVisibility(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	ops := e.child(t, alice, wsA, "", "Ops")
	deep := e.child(t, alice, wsA, string(ops.ID), "Deep")
	deeper := e.child(t, alice, wsA, string(deep.ID), "Deeper")
	elsewhere := e.child(t, alice, wsA, "", "Elsewhere")
	e.apply(t, alice, wsA, deeper.ID, para("failover steps"))
	e.apply(t, alice, wsA, elsewhere.ID, para("failover notes"))
	memo := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Memo", Visibility: model.VisibilityPrivate})).NoError(t)
	e.apply(t, alice, wsA, memo.ID, para("failover secret"))

	all := gt.R1(e.uc.Search(ctx, alice, wsA, usecase.SearchInput{Query: "failover"})).NoError(t)
	gt.A(t, all).Length(3)
	forBob := gt.R1(e.uc.Search(ctx, bob, wsA, usecase.SearchInput{Query: "failover"})).NoError(t)
	gt.A(t, forBob).Length(2)
	under := gt.R1(e.uc.Search(ctx, alice, wsA, usecase.SearchInput{Query: "failover", Under: ops.ID})).NoError(t)
	gt.A(t, under).Length(1)
	gt.Equal(t, under[0].Paper.ID, deeper.ID)
	_, err := e.uc.Search(ctx, alice, wsA, usecase.SearchInput{Query: "  "})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestSidebar(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	a := e.child(t, alice, wsA, "", "A")
	b := e.child(t, alice, wsA, "", "B")
	gt.NoError(t, e.uc.Pin(ctx, alice, wsA, a.ID, nil))
	gt.NoError(t, e.uc.Pin(ctx, alice, wsA, b.ID, ptr("")))
	gt.R1(e.uc.GetPaper(ctx, alice, wsA, a.ID)).NoError(t)
	gt.R1(e.uc.GetPaper(ctx, alice, wsA, b.ID)).NoError(t)

	sb := gt.R1(e.uc.Sidebar(ctx, alice, wsA)).NoError(t)
	gt.Equal(t, sb.Home.ID, e.home(t, wsA))
	gt.A(t, sb.Pins).Length(2)
	gt.Equal(t, sb.Pins[0].ID, b.ID)
	gt.Equal(t, sb.Recent[0].ID, b.ID)
	gt.Equal(t, sb.Recent[1].ID, a.ID)

	gt.NoError(t, e.uc.Unpin(ctx, alice, wsA, b.ID))
	sb = gt.R1(e.uc.Sidebar(ctx, alice, wsA)).NoError(t)
	gt.A(t, sb.Pins).Length(1)
}

func TestAttachments(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	p := e.child(t, alice, wsA, "", "Doc")
	other := e.child(t, alice, wsA, "", "Other")
	a := gt.R1(e.uc.Upload(ctx, alice, wsA, p.ID, usecase.UploadInput{Name: "../a.png", ContentType: "image/png", Body: strings.NewReader("png")})).NoError(t)
	gt.Equal(t, a.Name, "a.png")
	again := gt.R1(e.uc.Upload(ctx, alice, wsA, p.ID, usecase.UploadInput{Name: "b.png", ContentType: "image/png", Body: strings.NewReader("png")})).NoError(t)
	gt.Equal(t, again.ID, a.ID)

	_, err := e.uc.Upload(ctx, alice, wsA, p.ID, usecase.UploadInput{ContentType: "text/html", Body: strings.NewReader("<p>")})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	_, err = e.uc.Upload(ctx, alice, wsA, p.ID, usecase.UploadInput{ContentType: "image/png", Body: bytes.NewReader(make([]byte, model.MaxAttachmentSize+1))})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	content := gt.R1(e.uc.OpenAttachment(ctx, bob, wsA, p.ID, a.ID)).NoError(t)
	data := gt.R1(io.ReadAll(content.Body)).NoError(t)
	gt.Equal(t, string(data), "png")

	// an image block may only use attachments of its own paper
	_, err = e.uc.Apply(ctx, alice, wsA, other.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpInsert, Type: ptr(model.BlockImage), Props: &model.BlockProps{AttachmentID: a.ID}}}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	_, err = e.uc.OpenAttachment(ctx, alice, wsA, other.ID, a.ID)
	gt.True(t, errors.Is(err, model.ErrNotFound))
}

func TestBlockCapPerPaper(t *testing.T) {
	e := newEnv(t)
	alice := e.user(t, "alice")
	p := e.child(t, alice, wsA, "", "Doc")
	ops := make([]usecase.Op, 200)
	for i := range ops {
		ops[i] = para("x")
	}
	for range model.MaxBlocksPerPaper / len(ops) {
		e.apply(t, alice, wsA, p.ID, ops...)
	}
	_, err := e.uc.Apply(context.Background(), alice, wsA, p.ID, usecase.BatchInput{Ops: ops[:1]})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	// range reads walk the paper with a cursor
	ctx := context.Background()
	total, after := 0, ""
	for {
		page := gt.R1(e.uc.ListBlocks(ctx, alice, wsA, p.ID, after, 500)).NoError(t)
		total += len(page.Blocks)
		if page.Next == "" {
			break
		}
		after = page.Next
	}
	gt.V(t, total).Equal(model.MaxBlocksPerPaper)
}
