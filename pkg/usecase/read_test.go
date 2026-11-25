package usecase_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/interfaces"
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

func TestSidebarFavorites(t *testing.T) {
	// T39: favorites are added, ordered by after and removed
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	a := e.child(t, alice, wsA, "", "A")
	b := e.child(t, alice, wsA, "", "B")
	c := e.child(t, alice, wsA, "", "C")
	gt.NoError(t, e.uc.Favorite(ctx, alice, wsA, a.ID, nil))
	gt.NoError(t, e.uc.Favorite(ctx, alice, wsA, b.ID, ptr("")))
	gt.NoError(t, e.uc.Favorite(ctx, alice, wsA, c.ID, ptr(string(b.ID))))
	gt.R1(e.uc.GetPaper(ctx, alice, wsA, a.ID)).NoError(t)
	gt.R1(e.uc.GetPaper(ctx, alice, wsA, b.ID)).NoError(t)

	sb := gt.R1(e.uc.Sidebar(ctx, alice, wsA)).NoError(t)
	gt.Equal(t, sb.Home.ID, e.home(t, wsA))
	var order []model.PaperID
	for _, f := range sb.Favorites {
		order = append(order, f.ID)
	}
	gt.Equal(t, order, []model.PaperID{b.ID, c.ID, a.ID})
	gt.Equal(t, sb.Recent[0].ID, b.ID)
	gt.Equal(t, sb.Recent[1].ID, a.ID)

	gt.NoError(t, e.uc.Unfavorite(ctx, alice, wsA, b.ID))
	sb = gt.R1(e.uc.Sidebar(ctx, alice, wsA)).NoError(t)
	gt.A(t, sb.Favorites).Length(2)
	gt.Equal(t, sb.Favorites[0].ID, c.ID)
}

func TestFavoriteRefusesHomeAndPrivate(t *testing.T) {
	// T40: the home and private papers cannot be favorites
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	err := e.uc.Favorite(ctx, alice, wsA, e.home(t, wsA), nil)
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	memo := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Memo", Visibility: model.VisibilityPrivate})).NoError(t)
	err = e.uc.Favorite(ctx, alice, wsA, memo.ID, nil)
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	// an anchor that is not a favorite is refused too
	p := e.child(t, alice, wsA, "", "P")
	err = e.uc.Favorite(ctx, alice, wsA, p.ID, ptr(string(memo.ID)))
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	sb := gt.R1(e.uc.Sidebar(ctx, alice, wsA)).NoError(t)
	gt.A(t, sb.Favorites).Length(0)
}

// unfiled creates a public paper without a parent.
func (e *env) unfiled(t *testing.T, p *model.Principal, ws model.WorkspaceID, title string) *model.Paper {
	t.Helper()
	return gt.R1(e.uc.CreatePaper(context.Background(), p, ws, usecase.CreateInput{Title: title})).NoError(t)
}

func TestSidebarUnfiledCount(t *testing.T) {
	// T43: three unfiled papers and one private paper of one's own
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	for i := range 3 {
		e.unfiled(t, alice, wsA, "mine "+string(rune('a'+i)))
	}
	gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Memo", Visibility: model.VisibilityPrivate})).NoError(t)
	// not counted: filed, archived, trashed, someone else's
	e.child(t, alice, wsA, "", "Filed")
	archived := e.unfiled(t, alice, wsA, "Archived")
	gt.R1(e.uc.Archive(ctx, alice, wsA, archived.ID, false)).NoError(t)
	trashed := e.unfiled(t, alice, wsA, "Trashed")
	gt.R1(e.uc.Trash(ctx, alice, wsA, trashed.ID)).NoError(t)
	e.unfiled(t, bob, wsA, "Bob's")

	sb := gt.R1(e.uc.Sidebar(ctx, alice, wsA)).NoError(t)
	gt.V(t, sb.UnfiledCount).Equal(4)
	sb = gt.R1(e.uc.Sidebar(ctx, bob, wsA)).NoError(t)
	gt.V(t, sb.UnfiledCount).Equal(1)
}

func TestSidebarUnfiledCountCaps(t *testing.T) {
	// T44 and T52: 150 unfiled papers count as 100, and the sidebar reads a
	// bounded number of times whatever the number of papers
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	a := e.child(t, alice, wsA, "", "A")
	gt.NoError(t, e.uc.Favorite(ctx, alice, wsA, a.ID, nil))
	gt.R1(e.uc.GetPaper(ctx, alice, wsA, a.ID)).NoError(t)

	e.count.reset()
	gt.R1(e.uc.Sidebar(ctx, alice, wsA)).NoError(t)
	few := e.count.reads()

	for i := range 150 {
		e.unfiled(t, alice, wsA, "u"+strconv.Itoa(i))
	}
	e.count.reset()
	sb := gt.R1(e.uc.Sidebar(ctx, alice, wsA)).NoError(t)
	gt.V(t, sb.UnfiledCount).Equal(usecase.MaxUnfiledCount)
	gt.A(t, sb.Favorites).Length(1)
	// workspace record, favorites, recent, one batch of papers, and the
	// count's queries (at most 5)
	gt.V(t, e.count.papers).Equal(1)
	gt.V(t, e.count.callsOf("ListPapers")).Equal(1)
	gt.True(t, e.count.reads()-e.count.callsOf("GetWorkspaceRecord") <= 8)
	gt.V(t, e.count.reads()).Equal(few)

	// many papers that do not count still stop after 5 queries
	carol := e.user(t, "carol")
	for i := range 450 {
		p := e.unfiled(t, carol, wsA, "archived "+strconv.Itoa(i))
		gt.R1(e.uc.Archive(ctx, carol, wsA, p.ID, false)).NoError(t)
	}
	e.count.reset()
	sb = gt.R1(e.uc.Sidebar(ctx, carol, wsA)).NoError(t)
	gt.V(t, sb.UnfiledCount).Equal(0)
	gt.V(t, e.count.callsOf("ListPapers")).Equal(5)
	gt.True(t, e.count.reads()-e.count.callsOf("GetWorkspaceRecord") <= 8)
}

func TestListPapersDefault(t *testing.T) {
	// T27: by default only one's own unfiled and private papers
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	mine := e.unfiled(t, alice, wsA, "Mine")
	e.clock.Advance(time.Second)
	memo := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Memo", Visibility: model.VisibilityPrivate})).NoError(t)
	e.clock.Advance(time.Second)
	filed := e.child(t, alice, wsA, "", "Filed")
	archived := e.unfiled(t, alice, wsA, "Archived")
	gt.R1(e.uc.Archive(ctx, alice, wsA, archived.ID, false)).NoError(t)
	trashed := e.unfiled(t, alice, wsA, "Trashed")
	gt.R1(e.uc.Trash(ctx, alice, wsA, trashed.ID)).NoError(t)
	moving := e.unfiled(t, alice, wsA, "Moving")
	gt.NoError(t, e.repo.RunTx(ctx, wsA, func(tx interfaces.Tx) error {
		q, err := tx.GetPaper(moving.ID)
		if err != nil {
			return err
		}
		q.JobID = model.NewJobID()
		return tx.PutPaper(q)
	}))
	theirs := e.unfiled(t, bob, wsA, "Theirs")

	ids := func(page *usecase.PapersPage) []model.PaperID {
		var out []model.PaperID
		for _, x := range page.Papers {
			out = append(out, x.ID)
		}
		return out
	}
	page := gt.R1(e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{})).NoError(t)
	gt.Equal(t, ids(page), []model.PaperID{memo.ID, mine.ID})
	gt.Equal(t, page.NextCursor, "")
	gt.Nil(t, page.Papers[0].Parent)

	// archived on request, filed with its parent, everything with all
	page = gt.R1(e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{Archived: true})).NoError(t)
	gt.A(t, page.Papers).Length(3)
	page = gt.R1(e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{Place: usecase.PlaceFiled})).NoError(t)
	gt.Equal(t, ids(page), []model.PaperID{filed.ID})
	gt.Equal(t, page.Papers[0].Parent.ID, e.home(t, wsA))
	gt.Equal(t, page.Papers[0].Parent.Title, "Alpha")
	page = gt.R1(e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{Place: usecase.PlaceAll, Visibility: model.VisibilityPublic})).NoError(t)
	gt.Equal(t, ids(page), []model.PaperID{filed.ID, mine.ID})
	page = gt.R1(e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{Visibility: model.VisibilityPrivate})).NoError(t)
	gt.Equal(t, ids(page), []model.PaperID{memo.ID})
	page = gt.R1(e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{Sort: model.SortCreated})).NoError(t)
	gt.Equal(t, ids(page), []model.PaperID{memo.ID, mine.ID})

	// T28: everyone's: others' unfiled papers but never others' private ones
	page = gt.R1(e.uc.ListPapers(ctx, bob, wsA, usecase.ListPapersInput{Creator: usecase.CreatorAll})).NoError(t)
	gt.Equal(t, ids(page), []model.PaperID{theirs.ID, mine.ID})

	// one person's: their public papers, never their private ones
	page = gt.R1(e.uc.ListPapers(ctx, bob, wsA, usecase.ListPapersInput{Place: usecase.PlaceAll, Creator: usecase.PaperCreator(alice.User.ID)})).NoError(t)
	gt.Equal(t, ids(page), []model.PaperID{filed.ID, mine.ID})
	page = gt.R1(e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{Creator: usecase.PaperCreator(bob.User.ID)})).NoError(t)
	gt.Equal(t, ids(page), []model.PaperID{theirs.ID})
	page = gt.R1(e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{Creator: usecase.PaperCreator(model.NewUserID())})).NoError(t)
	gt.A(t, page.Papers).Length(0)

	for _, bad := range []usecase.ListPapersInput{
		{Place: "tree"}, {Creator: "not an id"}, {Creator: "you"}, {Visibility: "secret"}, {Sort: "title"}, {Limit: 101}, {Limit: -1}, {Cursor: "!!"},
	} {
		_, err := e.uc.ListPapers(ctx, alice, wsA, bad)
		gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	}
	e.deny(wsA, bob)
	_, err := e.uc.ListPapers(ctx, bob, wsA, usecase.ListPapersInput{})
	gt.True(t, errors.Is(err, model.ErrNotFound))
}

func TestListPapersReadsOn(t *testing.T) {
	// T29: when most candidates are dropped, reading goes on until the page
	// is full, or stops after 5 queries with a cursor; T52: a page costs the
	// queries plus one batch read of the parents
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	var keep []model.PaperID
	for i := range 3 {
		keep = append(keep, e.unfiled(t, alice, wsA, "keep "+strconv.Itoa(i)).ID)
		e.clock.Advance(time.Second)
	}
	for i := range 30 {
		p := e.unfiled(t, alice, wsA, "drop "+strconv.Itoa(i))
		gt.R1(e.uc.Archive(ctx, alice, wsA, p.ID, false)).NoError(t)
		e.clock.Advance(time.Second)
	}
	// newest first: 30 archived papers, then the 3 kept ones; limit 2 reads
	// 4 at a time, so 5 queries reach 20 of the archived papers
	e.count.reset()
	page := gt.R1(e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{Limit: 2})).NoError(t)
	gt.A(t, page.Papers).Length(0)
	gt.NotEqual(t, page.NextCursor, "")
	gt.V(t, e.count.callsOf("ListPapers")).Equal(5)
	gt.V(t, e.count.papers).Equal(0) // no parents to read

	var got []model.PaperID
	cursor := page.NextCursor
	for cursor != "" {
		page = gt.R1(e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{Limit: 2, Cursor: cursor})).NoError(t)
		for _, x := range page.Papers {
			got = append(got, x.ID)
		}
		cursor = page.NextCursor
	}
	gt.Equal(t, got, []model.PaperID{keep[2], keep[1], keep[0]})

	// filed papers: one batch read of the parents however many there are
	parent := e.child(t, alice, wsA, "", "Parent")
	for i := range 10 {
		e.child(t, alice, wsA, string(parent.ID), "kid "+strconv.Itoa(i))
	}
	e.count.reset()
	page = gt.R1(e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{Place: usecase.PlaceFiled, Limit: 20})).NoError(t)
	gt.A(t, page.Papers).Length(11)
	gt.V(t, e.count.papers).Equal(1)
	gt.V(t, e.count.reads()-e.count.callsOf("GetWorkspaceRecord")).Equal(e.count.callsOf("ListPapers") + 1)
	mine := e.count.reads()

	// one person's list reads the same as one's own
	bob := e.user(t, "bob")
	e.count.reset()
	page = gt.R1(e.uc.ListPapers(ctx, bob, wsA, usecase.ListPapersInput{Place: usecase.PlaceFiled, Creator: usecase.PaperCreator(alice.User.ID), Limit: 20})).NoError(t)
	gt.A(t, page.Papers).Length(11)
	gt.V(t, e.count.reads()).Equal(mine)
}

func TestListWorkspacesLastOpened(t *testing.T) {
	// T54: the last paper opened in each workspace, read in two batches
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	a := e.child(t, alice, wsA, "", "A")
	e.clock.Advance(time.Minute)
	gt.R1(e.uc.GetPaper(ctx, alice, wsA, a.ID)).NoError(t)
	openedAt := e.clock.Now()

	e.count.reset()
	list, _ := gt.R2(e.uc.ListWorkspaces(ctx, alice)).NoError(t)
	gt.A(t, list).Length(2)
	gt.Equal(t, list[0].ID, wsA)
	gt.Equal(t, list[0].LastOpened.PaperID, a.ID)
	gt.Equal(t, list[0].LastOpened.Title, "A")
	gt.True(t, list[0].LastOpened.OpenedAt.Equal(openedAt))
	gt.Nil(t, list[1].LastOpened)
	gt.V(t, e.count.callsOf("GetRecents")).Equal(1)
	gt.V(t, e.count.callsOf("GetPapersIn")).Equal(1)
	gt.V(t, e.count.papers).Equal(0)

	// a paper that cannot be shown any more gives none
	b := e.child(t, bob, wsB, "", "B")
	gt.R1(e.uc.GetPaper(ctx, bob, wsB, b.ID)).NoError(t)
	gt.R1(e.uc.Archive(ctx, alice, wsA, a.ID, false)).NoError(t)
	list, _ = gt.R2(e.uc.ListWorkspaces(ctx, alice)).NoError(t)
	gt.Nil(t, list[0].LastOpened)
	list, _ = gt.R2(e.uc.ListWorkspaces(ctx, bob)).NoError(t)
	gt.Equal(t, list[1].LastOpened.PaperID, b.ID)
	gt.R1(e.uc.Trash(ctx, bob, wsB, b.ID)).NoError(t)
	list, _ = gt.R2(e.uc.ListWorkspaces(ctx, bob)).NoError(t)
	gt.Nil(t, list[1].LastOpened)
	memo := gt.R1(e.uc.CreatePaper(ctx, bob, wsA, usecase.CreateInput{Title: "Memo", Visibility: model.VisibilityPrivate})).NoError(t)
	gt.R1(e.uc.GetPaper(ctx, bob, wsA, memo.ID)).NoError(t)
	list, _ = gt.R2(e.uc.ListWorkspaces(ctx, bob)).NoError(t)
	gt.Equal(t, list[0].LastOpened.PaperID, memo.ID)
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

func TestFieldsAreSearchable(t *testing.T) {
	// T12: a value of an html block's fields is found by search, and an
	// update of it with the base revision is reflected
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	p := e.child(t, alice, wsA, "", "DR")
	res := e.apply(t, alice, wsA, p.ID, usecase.Op{
		Kind: usecase.OpInsert, Type: ptr(model.BlockHTML), Content: ptr(`<p>RTO <span data-field="rto">15m</span></p>`),
		Props: &model.BlockProps{Fields: map[string]string{"rto": "15m"}},
	})
	b := res.Blocks[0]
	gt.Equal(t, b.Props.Fields, map[string]string{"rto": "15m"})

	upd := e.apply(t, alice, wsA, p.ID, usecase.Op{
		Kind: usecase.OpUpdate, BlockID: b.ID, BaseRev: b.Rev,
		Props: &model.BlockProps{Fields: map[string]string{"rto": "10m"}},
	})
	gt.V(t, upd.Blocks[0].Rev).Equal(b.Rev + 1)
	gt.Equal(t, upd.Blocks[0].Content, b.Content)
	hits := gt.R1(e.uc.Search(ctx, alice, wsA, usecase.SearchInput{Query: "10m"})).NoError(t)
	gt.A(t, hits).Length(1)
	gt.Equal(t, hits[0].BlockID, b.ID)

	// fields are validated on write and dropped from other types
	_, err := e.uc.Apply(ctx, alice, wsA, p.ID, usecase.BatchInput{Ops: []usecase.Op{{
		Kind: usecase.OpUpdate, BlockID: b.ID, BaseRev: upd.Blocks[0].Rev,
		Props: &model.BlockProps{Fields: map[string]string{"Bad-Key": "x"}},
	}}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	para := e.apply(t, alice, wsA, p.ID, usecase.Op{
		Kind: usecase.OpInsert, Type: ptr(model.BlockParagraph), Content: ptr("text"),
		Props: &model.BlockProps{Fields: map[string]string{"rto": "1m"}},
	})
	gt.Nil(t, para.Blocks[0].Props.Fields)
}

func TestMentionsAreSearchable(t *testing.T) {
	// T4: a mention is stored as written and found by the name
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	p := e.child(t, alice, wsA, "", "Weekly")
	id := model.NewUserID()
	res := e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpInsert, Type: ptr(model.BlockParagraph), Content: ptr("Ask [Masayoshi Mizutani]{user=" + string(id) + "} to review")})
	gt.Equal(t, res.Blocks[0].Content, "Ask [@Masayoshi Mizutani]{user="+string(id)+"} to review")
	hits := gt.R1(e.uc.Search(ctx, alice, wsA, usecase.SearchInput{Query: "Mizutani"})).NoError(t)
	gt.A(t, hits).Length(1)
	gt.Equal(t, hits[0].BlockID, res.Blocks[0].ID)
	gt.Equal(t, hits[0].Text, "Ask @Masayoshi Mizutani to review")
}
