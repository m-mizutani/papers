package repository_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/repository/firestore"
)

// runSearchTest is runRepositoryTest for search: the Firestore emulator has
// no Search stage, so the Firestore backend needs a real Enterprise
// database (TEST_FIRESTORE_PROJECT_ID / TEST_FIRESTORE_DATABASE_ID without
// FIRESTORE_EMULATOR_HOST) whose indexes were created by `papers firestore
// setup`.
func runSearchTest(t *testing.T, name string, fn func(t *testing.T, repo interfaces.Repository)) {
	t.Helper()
	runRepositoryTest(t, name, func(t *testing.T, repo interfaces.Repository) {
		if _, ok := repo.(*firestore.Repository); ok && os.Getenv("FIRESTORE_EMULATOR_HOST") != "" {
			t.Skip("the Firestore emulator does not support search")
		}
		fn(t, repo)
	})
}

func query(t *testing.T, raw string) model.SearchQuery {
	t.Helper()
	return gt.R1(model.ParseSearchQuery(raw)).NoError(t)
}

// searchEventually retries the search until check passes: a search engine
// may apply index updates shortly after the write commits.
func searchEventually(t *testing.T, repo interfaces.Repository, ws model.WorkspaceID, raw string, check func([]*model.SearchEntry) bool) []*model.SearchEntry {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		got := gt.R1(repo.Search(context.Background(), ws, query(t, raw), 20)).NoError(t)
		if check(got) || time.Now().After(deadline) {
			return got
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func hasBlock(id model.BlockID) func([]*model.SearchEntry) bool {
	return func(es []*model.SearchEntry) bool {
		return slices.ContainsFunc(es, func(e *model.SearchEntry) bool { return e.BlockID == id })
	}
}

func lacksBlock(id model.BlockID) func([]*model.SearchEntry) bool {
	return func(es []*model.SearchEntry) bool { return !hasBlock(id)(es) }
}

func hasTitle(p model.PaperID) func([]*model.SearchEntry) bool {
	return func(es []*model.SearchEntry) bool {
		return slices.ContainsFunc(es, func(e *model.SearchEntry) bool { return e.PaperID == p && e.BlockID == "" })
	}
}

func putBlocks(t *testing.T, repo interfaces.Repository, p *model.Paper, blocks ...*model.Block) {
	t.Helper()
	gt.NoError(t, repo.RunPaperTx(context.Background(), p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error {
		for _, b := range blocks {
			if err := tx.PutBlock(b); err != nil {
				return err
			}
		}
		return nil
	}))
}

func TestSearchRepository(t *testing.T) {
	runSearchTest(t, "blocks follow writes and deletes", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		p := newPaper(t, repo)
		b := newBlock(p, "V", "the rollback procedure for zephyrine clusters")
		other := newBlock(p, "W", "unrelated text")
		putBlocks(t, repo, p, b, other)

		got := searchEventually(t, repo, p.WorkspaceID, "zephyrine", hasBlock(b.ID))
		gt.True(t, hasBlock(b.ID)(got))
		gt.True(t, lacksBlock(other.ID)(got))
		e := got[slices.IndexFunc(got, func(e *model.SearchEntry) bool { return e.BlockID == b.ID })]
		gt.Equal(t, e.PaperID, p.ID)
		gt.Equal(t, e.WorkspaceID, p.WorkspaceID)
		gt.Equal(t, e.BlockType, model.BlockParagraph)
		gt.Equal(t, e.Text, b.Content)

		// Every term is required.
		gt.True(t, lacksBlock(b.ID)(searchEventually(t, repo, p.WorkspaceID, "zephyrine absentword", lacksBlock(b.ID))))

		// An update replaces the indexed text.
		b2 := b.Clone()
		b2.Content, b2.Version = "now about quillotine instead", 2
		putBlocks(t, repo, p, b2)
		gt.True(t, hasBlock(b.ID)(searchEventually(t, repo, p.WorkspaceID, "quillotine", hasBlock(b.ID))))
		gt.True(t, lacksBlock(b.ID)(searchEventually(t, repo, p.WorkspaceID, "zephyrine", lacksBlock(b.ID))))

		// A delete removes the entry.
		gt.NoError(t, repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error { return tx.DeleteBlock(b.ID) }))
		gt.True(t, lacksBlock(b.ID)(searchEventually(t, repo, p.WorkspaceID, "quillotine", lacksBlock(b.ID))))
	})

	runSearchTest(t, "titles", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		p := newPaper(t, repo)
		p.Title = "Marmalite runbook"
		gt.NoError(t, repo.RunPaperTx(ctx, p.WorkspaceID, p.ID, func(tx interfaces.PaperTx) error { return tx.PutPaper(p) }))
		gt.True(t, hasTitle(p.ID)(searchEventually(t, repo, p.WorkspaceID, "marmalite", hasTitle(p.ID))))

		// The title of a new paper is searchable right away.
		fresh := newPaper(t, repo)
		got := searchEventually(t, repo, fresh.WorkspaceID, "design", hasTitle(fresh.ID))
		gt.True(t, hasTitle(fresh.ID)(got))
	})

	runSearchTest(t, "scoped by workspace", func(t *testing.T, repo interfaces.Repository) {
		a, b := newPaper(t, repo), newPaper(t, repo)
		ba := newBlock(a, "V", "vexillary signal")
		bb := newBlock(b, "V", "vexillary signal")
		putBlocks(t, repo, a, ba)
		putBlocks(t, repo, b, bb)
		got := searchEventually(t, repo, a.WorkspaceID, "vexillary", hasBlock(ba.ID))
		gt.True(t, hasBlock(ba.ID)(got))
		gt.True(t, lacksBlock(bb.ID)(got))
	})

	runSearchTest(t, "rich blocks index text only", func(t *testing.T, repo interfaces.Repository) {
		p := newPaper(t, repo)
		h := newBlock(p, "V", `<div class="gloamwort"><h2>Pelorian summary</h2><script>const hidden = "scriptonly";</script></div>`)
		h.Type = model.BlockHTML
		putBlocks(t, repo, p, h)
		gt.True(t, hasBlock(h.ID)(searchEventually(t, repo, p.WorkspaceID, "pelorian", hasBlock(h.ID))))
		gt.True(t, lacksBlock(h.ID)(searchEventually(t, repo, p.WorkspaceID, "scriptonly", lacksBlock(h.ID))))
		gt.True(t, lacksBlock(h.ID)(searchEventually(t, repo, p.WorkspaceID, "gloamwort", lacksBlock(h.ID))))
	})

	runSearchTest(t, "deleting a paper removes its entries", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		p := newPaper(t, repo)
		b := newBlock(p, "V", "obsidianite archive")
		putBlocks(t, repo, p, b)
		gt.True(t, hasBlock(b.ID)(searchEventually(t, repo, p.WorkspaceID, "obsidianite", hasBlock(b.ID))))
		gt.NoError(t, repo.DeletePaper(ctx, p.WorkspaceID, p.ID))
		got := searchEventually(t, repo, p.WorkspaceID, "obsidianite", func(es []*model.SearchEntry) bool { return len(es) == 0 })
		gt.A(t, got).Length(0)
	})

	runSearchTest(t, "limit", func(t *testing.T, repo interfaces.Repository) {
		ctx := context.Background()
		p := newPaper(t, repo)
		var blocks []*model.Block
		for _, o := range []string{"V", "W", "X", "Y"} {
			blocks = append(blocks, newBlock(p, o, "tessellane note "+o))
		}
		putBlocks(t, repo, p, blocks...)
		searchEventually(t, repo, p.WorkspaceID, "tessellane", func(es []*model.SearchEntry) bool { return len(es) == 4 })
		got := gt.R1(repo.Search(ctx, p.WorkspaceID, query(t, "tessellane"), 2)).NoError(t)
		gt.A(t, got).Length(2)

		_, err := repo.Search(ctx, p.WorkspaceID, query(t, "tessellane"), 0)
		gt.True(t, errors.Is(err, model.ErrInvalidArgument))
		_, err = repo.Search(ctx, p.WorkspaceID, query(t, "tessellane"), model.MaxSearchLimit+1)
		gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	})

	// Japanese has no spaces between words, so a search engine has to
	// segment it. These cases are what an Enterprise text index must pass
	// before papers relies on it (docs/design.html, search).
	runSearchTest(t, "japanese", func(t *testing.T, repo interfaces.Repository) {
		p := newPaper(t, repo)
		b := newBlock(p, "V", "本番環境で障害対応を行う際は、まず認可設定の変更履歴を確認する。")
		putBlocks(t, repo, p, b)
		for _, q := range []string{"障害対応", "認可設定", "変更履歴 確認", "本番環境"} {
			got := searchEventually(t, repo, p.WorkspaceID, q, hasBlock(b.ID))
			gt.True(t, hasBlock(b.ID)(got)).Describef("query %q", q)
		}
	})
}
