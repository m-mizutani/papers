package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/usecase"
)

func names(users []*usecase.UserSummary) []string {
	out := []string{}
	for _, u := range users {
		out = append(out, u.Name)
	}
	return out
}

func TestFindUsers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "Alice")
	mika := e.user(t, "Mika Ishida")
	masa := e.user(t, "Masayoshi Mizutani")
	outsider := e.user(t, "Mio Outside")
	e.deny(wsA, outsider)

	// T19: the workspace's rule leaves out who may not use it; q matches a
	// part of the name or the email, ignoring case
	got := gt.R1(e.uc.FindUsers(ctx, alice, wsA, usecase.FindUsersInput{Query: "MI"})).NoError(t)
	gt.Equal(t, names(got), []string{"Masayoshi Mizutani", "Mika Ishida"})
	gt.Equal(t, got[0].ID, masa.User.ID)
	got = gt.R1(e.uc.FindUsers(ctx, alice, wsA, usecase.FindUsersInput{Query: "alice@example"})).NoError(t)
	gt.Equal(t, names(got), []string{"Alice"})
	// the email is never answered
	for _, u := range gt.R1(e.uc.FindUsers(ctx, alice, wsA, usecase.FindUsersInput{})).NoError(t) {
		gt.Equal(t, *u, usecase.UserSummary{ID: u.ID, Name: u.Name})
	}
	// in the other workspace the outsider is a candidate
	got = gt.R1(e.uc.FindUsers(ctx, alice, wsB, usecase.FindUsersInput{Query: "mio"})).NoError(t)
	gt.Equal(t, names(got), []string{"Mio Outside"})

	// T24: by ID, in the order given, missing ones left out, without the rule
	got = gt.R1(e.uc.FindUsers(ctx, alice, wsA, usecase.FindUsersInput{IDs: []model.UserID{outsider.User.ID, model.NewUserID(), mika.User.ID, mika.User.ID}})).NoError(t)
	gt.Equal(t, names(got), []string{"Mio Outside", "Mika Ishida"})
	gt.Equal(t, got[1].ID, mika.User.ID)
}

func TestFindUsersLimitAndErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	for i := range 25 {
		e.user(t, fmt.Sprintf("member %02d", 24-i))
	}
	// T20: at most 20, by name; an empty q lists everyone
	got := gt.R1(e.uc.FindUsers(ctx, alice, wsA, usecase.FindUsersInput{Query: "member"})).NoError(t)
	gt.Equal(t, len(got), usecase.MaxUserResults)
	gt.Equal(t, got[0].Name, "member 00")
	gt.Equal(t, got[19].Name, "member 19")
	got = gt.R1(e.uc.FindUsers(ctx, alice, wsA, usecase.FindUsersInput{})).NoError(t)
	gt.Equal(t, got[0].Name, "alice")
	gt.Equal(t, len(got), usecase.MaxUserResults)

	// T21: a workspace the caller may not use, and invalid ids
	e.deny(wsB, alice)
	_, err := e.uc.FindUsers(ctx, alice, wsB, usecase.FindUsersInput{Query: "member"})
	gt.True(t, errors.Is(err, model.ErrNotFound))
	ids := make([]model.UserID, 21)
	for i := range ids {
		ids[i] = model.NewUserID()
	}
	for _, in := range []usecase.FindUsersInput{
		{IDs: ids},
		{IDs: []model.UserID{"short"}},
		{IDs: []model.UserID{alice.User.ID}, Query: "a"},
	} {
		_, err := e.uc.FindUsers(ctx, alice, wsA, in)
		isInvalid(t, err)
	}
}

func TestListPapersByCreator(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	bob := e.user(t, "bob")
	mine := gt.R1(e.uc.CreatePaper(ctx, bob, wsA, usecase.CreateInput{Title: "bob's public"})).NoError(t)
	gt.R1(e.uc.CreatePaper(ctx, bob, wsA, usecase.CreateInput{Title: "bob's private", Visibility: model.VisibilityPrivate})).NoError(t)
	gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "alice's"})).NoError(t)

	// T10, T11: another user's ID lists that user's papers the caller sees
	page := gt.R1(e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{Place: usecase.PlaceAll, Creator: usecase.PaperCreator(bob.User.ID)})).NoError(t)
	gt.Equal(t, len(page.Papers), 1)
	gt.Equal(t, page.Papers[0].ID, mine.ID)
	// for bob himself, his private paper too
	page = gt.R1(e.uc.ListPapers(ctx, bob, wsA, usecase.ListPapersInput{Place: usecase.PlaceAll, Creator: usecase.PaperCreator(bob.User.ID)})).NoError(t)
	gt.Equal(t, len(page.Papers), 2)
	_, err := e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{Creator: "bob"})
	isInvalid(t, err)
}
