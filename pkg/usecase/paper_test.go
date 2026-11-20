package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
)

func TestCreatePaperWithMarkdown(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.user(t, "owner")
	ws := e.workspace(t, owner)

	view := gt.R1(e.uc.CreatePaper(ctx, owner, ws, usecase.CreatePaperInput{Title: "Plan", Markdown: "# Goal\n\n- a\n- b"})).NoError(t)
	gt.Equal(t, view.Paper.Title, "Plan")
	gt.Equal(t, view.Paper.Revision, int64(1))
	gt.Equal(t, view.Role, model.RoleOwner)
	gt.A(t, view.Blocks).Length(3)

	commits := gt.R1(e.uc.ListCommits(ctx, owner, ws, view.Paper.ID, 0, 0)).NoError(t)
	gt.A(t, commits).Length(1)
	gt.Equal(t, commits[0].Message, "Initial content")

	md := gt.R1(e.uc.ExportMarkdown(ctx, owner, ws, view.Paper.ID)).NoError(t)
	gt.Equal(t, md, "# Plan\n\n## Goal\n\n- a\n- b\n")

	_, err := e.uc.CreatePaper(ctx, owner, ws, usecase.CreatePaperInput{Title: "a\nb"})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestPaperAccessControl(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, editor, viewer, guest := e.user(t, "owner"), e.user(t, "editor"), e.user(t, "viewer"), e.user(t, "guest")
	ws := e.workspace(t, owner, userBinding(editor, model.RoleEditor), userBinding(viewer, model.RoleViewer))

	// viewers cannot create papers
	_, err := e.uc.CreatePaper(ctx, viewer, ws, usecase.CreatePaperInput{Title: "x"})
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))

	open := gt.R1(e.uc.CreatePaper(ctx, editor, ws, usecase.CreatePaperInput{Title: "open"})).NoError(t)
	e.clock.Advance(time.Second)
	secret := gt.R1(e.uc.CreatePaper(ctx, editor, ws, usecase.CreatePaperInput{Title: "secret", Restricted: true})).NoError(t)

	// listings are filtered and ordered by update time
	gt.A(t, gt.R1(e.uc.ListPapers(ctx, viewer, ws)).NoError(t)).Length(1)
	ownerList := gt.R1(e.uc.ListPapers(ctx, owner, ws)).NoError(t)
	gt.A(t, ownerList).Length(2)
	gt.Equal(t, ownerList[0].Paper.ID, secret.Paper.ID)
	gt.Equal(t, gt.R1(e.uc.GetPaper(ctx, editor, ws, secret.Paper.ID)).NoError(t).Role, model.RoleOwner)

	_, err = e.uc.GetPaper(ctx, viewer, ws, secret.Paper.ID)
	gt.True(t, errors.Is(err, model.ErrNotFound))

	// outsiders cannot list the workspace
	_, err = e.uc.ListPapers(ctx, guest, ws)
	gt.True(t, errors.Is(err, model.ErrNotFound))

	// share the restricted paper with a guest outside the workspace
	access := model.PaperAccess{Restricted: true, Bindings: []model.RoleBinding{userBinding(guest, model.RoleViewer)}}
	_, err = e.uc.UpdatePaperAccess(ctx, viewer, ws, open.Paper.ID, access)
	gt.True(t, errors.Is(err, model.ErrPermissionDenied))
	gt.R1(e.uc.UpdatePaperAccess(ctx, editor, ws, secret.Paper.ID, access)).NoError(t)

	guestView := gt.R1(e.uc.GetPaper(ctx, guest, ws, secret.Paper.ID)).NoError(t)
	gt.Equal(t, guestView.Role, model.RoleViewer)
	guestList := gt.R1(e.uc.ListPapers(ctx, guest, ws)).NoError(t)
	gt.A(t, guestList).Length(1)

	// deleting requires ownership
	gt.True(t, errors.Is(e.uc.DeletePaper(ctx, guest, ws, secret.Paper.ID), model.ErrPermissionDenied))
	gt.NoError(t, e.uc.DeletePaper(ctx, owner, ws, secret.Paper.ID))
	_, err = e.uc.GetPaper(ctx, owner, ws, secret.Paper.ID)
	gt.True(t, errors.Is(err, model.ErrNotFound))
}

func TestSearchPapers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.user(t, "owner")
	ws := e.workspace(t, owner)
	long := strings.Repeat("filler ", 30)
	gt.R1(e.uc.CreatePaper(ctx, owner, ws, usecase.CreatePaperInput{Title: "Incident review", Markdown: "nothing"})).NoError(t)
	gt.R1(e.uc.CreatePaper(ctx, owner, ws, usecase.CreatePaperInput{Title: "Notes", Markdown: long + "the KUBERNETES cluster " + long})).NoError(t)

	hits := gt.R1(e.uc.SearchPapers(ctx, owner, ws, "incident", 10)).NoError(t)
	gt.A(t, hits).Length(1)
	gt.Equal(t, hits[0].Paper.Title, "Incident review")

	hits = gt.R1(e.uc.SearchPapers(ctx, owner, ws, "kubernetes", 10)).NoError(t)
	gt.A(t, hits).Length(1)
	gt.NotEqual(t, hits[0].BlockID, model.BlockID(""))
	gt.True(t, strings.Contains(hits[0].Snippet, "KUBERNETES"))
	gt.True(t, strings.HasPrefix(hits[0].Snippet, "…"))

	_, err := e.uc.SearchPapers(ctx, owner, ws, " ", 10)
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}
