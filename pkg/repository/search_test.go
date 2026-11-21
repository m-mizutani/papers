package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
)

func mustQuery(t *testing.T, raw string) model.SearchQuery {
	t.Helper()
	return gt.R1(model.ParseSearchQuery(raw)).NoError(t)
}

func TestSearch(t *testing.T) {
	runRepositoryTest(t, "titles and block text", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		home, p := homeAndChild(t, repo)
		ws := home.WorkspaceID
		b := blockIn(p, "", "a1", "the **quarterly** [plan]{.red}")
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutBlock(b) }))

		hits := gt.R1(repo.Search(ctx, ws, mustQuery(t, "quarterly plan"), 10)).NoError(t)
		gt.A(t, hits).Length(1)
		gt.Equal(t, hits[0].BlockID, b.ID)
		gt.Equal(t, hits[0].Text, "the quarterly plan") // markup is stripped

		titles := gt.R1(repo.Search(ctx, ws, mustQuery(t, "design"), 10)).NoError(t)
		gt.A(t, titles).Length(1)
		gt.Equal(t, titles[0].PaperID, p.ID)
		gt.Equal(t, titles[0].BlockID, model.BlockID(""))

		// edits and deletes are reflected
		b.Content, b.Rev, b.UpdatedAt = "nothing", 2, time.Now().UTC()
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.PutBlock(b) }))
		gone := gt.R1(repo.Search(ctx, ws, mustQuery(t, "quarterly"), 10)).NoError(t)
		gt.A(t, gone).Length(0)
		gt.NoError(t, repo.RunTx(ctx, ws, func(tx interfaces.Tx) error { return tx.DeleteBlock(p.ID, b.ID) }))
		gone = gt.R1(repo.Search(ctx, ws, mustQuery(t, "nothing"), 10)).NoError(t)
		gt.A(t, gone).Length(0)

		// other workspaces are not searched
		other := gt.R1(repo.Search(ctx, newWorkspaceID(), mustQuery(t, "design"), 10)).NoError(t)
		gt.A(t, other).Length(0)
	})

	runRepositoryTest(t, "limit is validated", func(t *testing.T, repo interfaces.Repository) {
		_, err := repo.Search(context.Background(), newWorkspaceID(), mustQuery(t, "x"), 0)
		gt.Error(t, err)
	})
}
