package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
	"github.com/m-mizutani/papers/pkg/service/attachment"
	"github.com/m-mizutani/papers/pkg/usecase"
)

const docV1 = `<!doctype html><html><head><title>Report</title><style>.x{color:red}</style></head><body><h1>Weekly incidents</h1><p>fourteen this week</p><script>var secret = "hidden token";</script></body></html>`
const docV2 = `<!doctype html><html><body><h1>Weekly incidents</h1><p>nine this week</p></body></html>`

// htmlPaper creates an unfiled HTML paper with doc.
func (e *env) htmlPaper(t *testing.T, p *model.Principal, ws model.WorkspaceID, title, doc string) *model.Paper {
	t.Helper()
	return gt.R1(e.uc.CreatePaper(context.Background(), p, ws, usecase.CreateInput{Title: title, Kind: model.PaperKindHTML, HTML: doc})).NoError(t)
}

// page reads the html_page block of an HTML paper, checking it is the only block.
func (e *env) page(t *testing.T, ws model.WorkspaceID, id model.PaperID) *model.Block {
	t.Helper()
	blocks := gt.R1(e.repo.ListBlocks(context.Background(), ws, id)).NoError(t)
	gt.A(t, blocks).Length(1).Required()
	gt.Equal(t, blocks[0].Type, model.BlockHTMLPage)
	return blocks[0]
}

func setHTML(doc string, base int64) usecase.Op {
	return usecase.Op{Kind: usecase.OpSetHTML, HTML: ptr(doc), BaseRev: base}
}

func (e *env) storedHTML(t *testing.T, ws model.WorkspaceID, paper model.PaperID, att model.AttachmentID) string {
	t.Helper()
	c := gt.R1(e.uc.OpenHTML(context.Background(), &model.Principal{User: &model.User{ID: e.paper(t, ws, paper).CreatedBy.UserID}}, ws, paper, att)).NoError(t)
	body := gt.R1(c.Open(context.Background())).NoError(t)
	defer func() { _ = body.Close() }()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}

func htmlAttachments(t *testing.T, e *env) int {
	t.Helper()
	n := 0
	for _, k := range e.store.Keys() {
		if strings.Contains(k, "/attachments/") {
			n++
		}
	}
	return n
}

func TestCreateHTMLPaper(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")

	t.Run("unfiled, in one transaction", func(t *testing.T) { // T2
		e.count.reset()
		p := e.htmlPaper(t, alice, wsA, "Report", docV1)
		gt.Equal(t, p.Kind, model.PaperKindHTML)
		gt.Equal(t, p.ParentID, model.PaperID(""))
		page := e.page(t, wsA, p.ID)
		gt.Equal(t, page.Rev, int64(1))
		gt.True(t, model.ValidID(string(page.Props.AttachmentID)))
		gt.S(t, page.Content).Contains("fourteen this week")
		gt.Equal(t, e.storedHTML(t, wsA, p.ID, page.Props.AttachmentID), docV1)
		a := gt.R1(e.repo.GetAttachment(ctx, wsA, p.ID, page.Props.AttachmentID)).NoError(t)
		gt.Equal(t, a.ContentType, model.HTMLContentType)
		gt.Equal(t, a.Size, int64(len(docV1)))
	})

	t.Run("under a parent", func(t *testing.T) { // T3
		parent := e.child(t, alice, wsA, "", "Runbooks")
		p := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Weekly", Parent: string(parent.ID), Kind: model.PaperKindHTML, HTML: docV1})).NoError(t)
		gt.Equal(t, p.Kind, model.PaperKindHTML)
		gt.Equal(t, p.ParentID, parent.ID)
		kids := paperBlocks(t, e, alice, wsA, parent.ID)
		gt.A(t, kids).Length(1)
		gt.Equal(t, kids[0], p.ID)
		page := e.page(t, wsA, p.ID)
		gt.Equal(t, e.storedHTML(t, wsA, p.ID, page.Props.AttachmentID), docV1)
	})

	t.Run("through an insert of the parent's batch", func(t *testing.T) { // T3 (ops)
		parent := e.child(t, alice, wsA, "", "Reports")
		res := e.apply(t, alice, wsA, parent.ID, usecase.Op{Kind: usecase.OpInsert, Type: ptr(model.BlockPaper), Title: ptr("Q3"), PaperKind: ptr(model.PaperKindHTML), HTML: ptr(docV2)})
		child := e.paper(t, wsA, res.Blocks[0].Props.PaperID)
		gt.Equal(t, child.Kind, model.PaperKindHTML)
		gt.Equal(t, e.storedHTML(t, wsA, child.ID, e.page(t, wsA, child.ID).Props.AttachmentID), docV2)
	})

	t.Run("private", func(t *testing.T) { // T4
		p := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Mine", Visibility: model.VisibilityPrivate, Kind: model.PaperKindHTML, HTML: docV2})).NoError(t)
		gt.True(t, p.IsPrivate())
		gt.Equal(t, p.ParentID, model.PaperID(""))
		page := e.page(t, wsA, p.ID)
		gt.Equal(t, page.Rev, int64(1))
		gt.True(t, model.ValidID(string(page.Props.AttachmentID)))
	})

	t.Run("blocks papers say so", func(t *testing.T) {
		p := e.child(t, alice, wsA, "", "Plain")
		gt.Equal(t, p.Kind, model.PaperKindBlocks)
	})
}

func TestCreateHTMLPaperRejects(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	parent := e.child(t, alice, wsA, "", "Runbooks")
	before := len(gt.R1(e.repo.ListChildren(ctx, wsA, parent.ID)).NoError(t))
	objects := htmlAttachments(t, e)

	cases := map[string]usecase.CreateInput{
		"unknown kind":     {Title: "x", Kind: "pdf", HTML: docV1},                                                  // T1
		"no html":          {Title: "x", Kind: model.PaperKindHTML},                                                 // T62
		"html and md":      {Title: "x", Kind: model.PaperKindHTML, HTML: docV1, Markdown: "# x"},                   // T8 (use case)
		"html for blocks":  {Title: "x", HTML: docV1},                                                               // T8 (use case)
		"too large":        {Title: "x", Kind: model.PaperKindHTML, HTML: strings.Repeat("a", model.MaxHTMLSize+1)}, // T58
		"not utf-8":        {Title: "x", Kind: model.PaperKindHTML, HTML: "<p>\xff\xfe</p>"},                        // T58
		"under the parent": {Title: "x", Parent: string(parent.ID), Kind: model.PaperKindHTML, HTML: ""},            // T62
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := e.uc.CreatePaper(ctx, alice, wsA, in)
			gt.True(t, errors.Is(err, model.ErrInvalidArgument))
		})
	}
	gt.Equal(t, len(gt.R1(e.repo.ListChildren(ctx, wsA, parent.ID)).NoError(t)), before)
	gt.Equal(t, htmlAttachments(t, e), objects) // nothing stored

	t.Run("an insert needs html, and html needs paper_kind html", func(t *testing.T) { // T62
		_, err := e.uc.Apply(ctx, alice, wsA, parent.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpInsert, Type: ptr(model.BlockPaper), PaperKind: ptr(model.PaperKindHTML)}}})
		gt.True(t, errors.Is(err, model.ErrInvalidArgument))
		_, err = e.uc.Apply(ctx, alice, wsA, parent.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpInsert, Type: ptr(model.BlockPaper), HTML: ptr(docV1)}}})
		gt.True(t, errors.Is(err, model.ErrInvalidArgument))
		_, err = e.uc.Apply(ctx, alice, wsA, parent.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpInsert, Type: ptr(model.BlockPaper), PaperKind: ptr(model.PaperKindHTML), HTML: ptr("")}}})
		gt.True(t, errors.Is(err, model.ErrInvalidArgument))
		_, err = e.uc.Apply(ctx, alice, wsA, parent.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpInsert, Type: ptr(model.BlockHTMLPage)}}})
		gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	})

	t.Run("a failed transaction leaves nothing", func(t *testing.T) { // T59
		papers := len(gt.R1(e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{Place: "all", Creator: "me"})).NoError(t).Papers)
		e.count.failTx = 1
		_, err := e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "lost", Kind: model.PaperKindHTML, HTML: docV1})
		gt.Error(t, err)
		e.count.failTx = 1
		_, err = e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "lost too", Parent: string(parent.ID), Kind: model.PaperKindHTML, HTML: docV2})
		gt.Error(t, err)
		gt.Equal(t, len(gt.R1(e.uc.ListPapers(ctx, alice, wsA, usecase.ListPapersInput{Place: "all", Creator: "me"})).NoError(t).Papers), papers)
		gt.Equal(t, len(gt.R1(e.repo.ListChildren(ctx, wsA, parent.ID)).NoError(t)), before)
		gt.A(t, paperBlocks(t, e, alice, wsA, parent.ID)).Length(0)
	})
}

func TestSetHTML(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	p := e.htmlPaper(t, alice, wsA, "Report", docV1)
	first := e.page(t, wsA, p.ID)

	// T9 + T18 (use case): title and HTML in one batch
	res := e.apply(t, bob, wsA, p.ID, usecase.Op{Kind: usecase.OpSetTitle, Title: ptr("Weekly report")}, setHTML(docV2, 1))
	gt.A(t, res.Blocks).Length(1)
	got := e.page(t, wsA, p.ID)
	gt.Equal(t, got.ID, first.ID) // T43: the block stays
	gt.Equal(t, got.Rev, int64(2))
	gt.NotEqual(t, got.Props.AttachmentID, first.Props.AttachmentID)
	gt.S(t, got.Content).Contains("nine this week")
	gt.Equal(t, e.paper(t, wsA, p.ID).Title, "Weekly report")
	gt.Equal(t, e.storedHTML(t, wsA, p.ID, got.Props.AttachmentID), docV2)
	// the earlier document stays
	gt.Equal(t, e.storedHTML(t, wsA, p.ID, first.Props.AttachmentID), docV1)

	// T10: the same document again changes nothing and stores nothing
	objects := htmlAttachments(t, e)
	res = e.apply(t, alice, wsA, p.ID, setHTML(docV2, 2))
	gt.A(t, res.Blocks).Length(0)
	gt.Equal(t, e.page(t, wsA, p.ID).Rev, int64(2))
	gt.Equal(t, htmlAttachments(t, e), objects)

	// T11 + T19: a stale base conflicts and nothing of the batch is written
	_, err := e.uc.Apply(ctx, alice, wsA, p.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpSetTitle, Title: ptr("lost")}, setHTML(docV1+"<p>x</p>", 1)}})
	var ce *usecase.ConflictError
	gt.True(t, errors.As(err, &ce))
	gt.A(t, ce.Conflicts).Length(1)
	gt.Equal(t, ce.Conflicts[0].Current.Rev, int64(2))
	gt.Equal(t, ce.Conflicts[0].Current.Props.AttachmentID, got.Props.AttachmentID)
	gt.Equal(t, e.paper(t, wsA, p.ID).Title, "Weekly report")
	gt.Equal(t, e.page(t, wsA, p.ID).Rev, int64(2))
	// the stored bytes have no metadata: they are not an attachment
	for _, k := range e.store.Keys() {
		id := model.AttachmentID(k[strings.LastIndex(k, "/")+1:])
		if strings.Contains(k, string(p.ID)) && id != first.Props.AttachmentID && id != got.Props.AttachmentID {
			_, err := e.repo.GetAttachment(ctx, wsA, p.ID, id)
			gt.True(t, errors.Is(err, model.ErrNotFound))
		}
	}

	// T12: force writes over it
	res = gt.R1(e.uc.Apply(ctx, alice, wsA, p.ID, usecase.BatchInput{Ops: []usecase.Op{setHTML(docV1+"<p>forced</p>", 1)}, Force: true})).NoError(t)
	gt.Equal(t, res.Blocks[0].Rev, int64(3))

	// T13: exactly the maximum size is accepted, and the content is capped
	big := "<p>" + strings.Repeat("あ", (model.MaxHTMLSize-7)/3) + "</p>"
	big += strings.Repeat(" ", model.MaxHTMLSize-len(big))
	gt.Equal(t, len(big), model.MaxHTMLSize)
	res = e.apply(t, alice, wsA, p.ID, setHTML(big, 3))
	gt.True(t, len(res.Blocks[0].Content) <= model.MaxSearchTextLength)
	gt.True(t, strings.HasPrefix(res.Blocks[0].Content, "ああ"))

	rejects := map[string][]usecase.Op{
		"too large": {setHTML(big+"x", 4)},                   // T14
		"not utf-8": {setHTML("\xff", 4)},                    // T15
		"empty":     {setHTML("", 4)},                        // T62
		"no html":   {{Kind: usecase.OpSetHTML, BaseRev: 4}}, // T62
		"twice":     {setHTML(docV1, 4), setHTML(docV2, 4)},  // T60
		"no base":   {setHTML(docV1, 0)},                     // like an update
	}
	for name, ops := range rejects {
		t.Run(name, func(t *testing.T) {
			objects := htmlAttachments(t, e)
			_, err := e.uc.Apply(ctx, alice, wsA, p.ID, usecase.BatchInput{Ops: ops})
			gt.True(t, errors.Is(err, model.ErrInvalidArgument))
			gt.Equal(t, e.page(t, wsA, p.ID).Rev, int64(4))
			if name != "no base" {
				gt.Equal(t, htmlAttachments(t, e), objects) // T14: not stored
			}
		})
	}

	t.Run("blocks papers have no set_html", func(t *testing.T) { // T16
		plain := e.child(t, alice, wsA, "", "Plain")
		_, err := e.uc.Apply(ctx, alice, wsA, plain.ID, usecase.BatchInput{Ops: []usecase.Op{setHTML(docV1, 1)}})
		gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	})

	t.Run("access", func(t *testing.T) { // T17
		readOnly := &model.Principal{User: alice.User, Token: &model.APIToken{ID: model.NewTokenID(), UserID: alice.User.ID, Scope: model.ScopeRead}}
		_, err := e.uc.Apply(ctx, readOnly, wsA, p.ID, usecase.BatchInput{Ops: []usecase.Op{setHTML(docV2, 4)}})
		gt.True(t, errors.Is(err, model.ErrPermissionDenied))
		mine := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Mine", Visibility: model.VisibilityPrivate, Kind: model.PaperKindHTML, HTML: docV1})).NoError(t)
		_, err = e.uc.Apply(ctx, bob, wsA, mine.ID, usecase.BatchInput{Ops: []usecase.Op{setHTML(docV2, 1)}})
		gt.True(t, errors.Is(err, model.ErrNotFound))
	})
}

func TestSetHTMLOnBusyPaper(t *testing.T) { // T17
	e := newEnvWith(t, nil, usecase.WithoutBackgroundJobs())
	ctx := context.Background()
	alice := e.user(t, "alice")
	p := e.htmlPaper(t, alice, wsA, "Report", docV1)
	e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpMovePaper, Workspace: wsB, Parent: string(e.home(t, wsB))})
	jobs := gt.R1(e.repo.ListRunnableJobs(ctx, wsA, e.clock.Now().Add(time.Hour), 10)).NoError(t)
	gt.A(t, jobs).Length(1).Required()
	gt.NoError(t, usecase.StepJob(ctx, e.uc, wsA, jobs[0].ID)) // collect: the paper is marked
	_, err := e.uc.Apply(ctx, alice, wsA, p.ID, usecase.BatchInput{Ops: []usecase.Op{setHTML(docV2, 1)}})
	gt.True(t, errors.Is(err, model.ErrBusy))
}

func TestHTMLPaperRefusesBlockOps(t *testing.T) { // T38, T39
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	parent := e.child(t, alice, wsA, "", "Runbooks")
	p := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Report", Parent: string(parent.ID), Kind: model.PaperKindHTML, HTML: docV1})).NoError(t)
	page := e.page(t, wsA, p.ID)

	ops := map[string]usecase.Op{
		"insert":          para("x"),
		"insert paper":    {Kind: usecase.OpInsert, Type: ptr(model.BlockPaper), Title: ptr("kid")},
		"update":          {Kind: usecase.OpUpdate, BlockID: page.ID, BaseRev: 1, Content: ptr("x")},
		"delete":          {Kind: usecase.OpDelete, BlockID: page.ID, BaseRev: 1},
		"move":            {Kind: usecase.OpMove, BlockID: page.ID, After: ptr("")},
		"insert_markdown": {Kind: usecase.OpInsertMarkdown, Markdown: ptr("# x")},
		"insert_row":      {Kind: usecase.OpInsertRow, BlockID: page.ID},
		"delete_col":      {Kind: usecase.OpDeleteCol, BlockID: page.ID},
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			_, err := e.uc.Apply(ctx, alice, wsA, p.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpSetTitle, Title: ptr("renamed")}, op}})
			gt.True(t, errors.Is(err, model.ErrInvalidArgument))
			gt.Equal(t, e.paper(t, wsA, p.ID).Title, "Report")
			gt.Equal(t, e.page(t, wsA, p.ID).Rev, int64(1))
		})
	}
	// set_title alone is fine
	e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpSetTitle, Title: ptr("renamed")})
	gt.Equal(t, e.paper(t, wsA, p.ID).Title, "renamed")

	// it never becomes a parent
	_, err := e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "kid", Parent: string(p.ID)})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	other := e.child(t, alice, wsA, "", "Other")
	_, err = e.uc.MovePaper(ctx, alice, wsA, other.ID, usecase.MoveInput{Parent: string(p.ID)})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	priv := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "p", Visibility: model.VisibilityPrivate})).NoError(t)
	_, err = e.uc.SetVisibility(ctx, alice, wsA, priv.ID, usecase.VisibilityInput{Visibility: model.VisibilityPublic, Parent: string(p.ID)})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	// a blocks paper cannot gain an html_page block
	_, err = e.uc.Apply(ctx, alice, wsA, other.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpInsert, Type: ptr(model.BlockHTMLPage)}}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	b := e.apply(t, alice, wsA, other.ID, para("x")).Blocks[0]
	_, err = e.uc.Apply(ctx, alice, wsA, other.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpUpdate, BlockID: b.ID, BaseRev: 1, Type: ptr(model.BlockHTMLPage)}}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestReadHTML(t *testing.T) { // T21, T22
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	p := e.htmlPaper(t, alice, wsA, "Report", docV1)
	gt.R1(e.uc.SaveVersion(ctx, alice, wsA, p.ID, "v1")).NoError(t)
	e.apply(t, alice, wsA, p.ID, setHTML(docV2, 1))

	cur := gt.R1(e.uc.ReadHTML(ctx, bob, wsA, p.ID, "")).NoError(t)
	gt.Equal(t, cur.HTML, docV2)
	gt.Equal(t, cur.Rev, int64(2))
	gt.Equal(t, cur.BlockID, e.page(t, wsA, p.ID).ID)
	gt.Equal(t, cur.Size, int64(len(docV2)))
	v1 := gt.R1(e.uc.ReadHTML(ctx, bob, wsA, p.ID, "1")).NoError(t)
	gt.Equal(t, v1.HTML, docV1)
	gt.Equal(t, v1.Rev, int64(1))
	latest := gt.R1(e.uc.ReadHTML(ctx, bob, wsA, p.ID, "latest")).NoError(t)
	gt.Equal(t, latest.HTML, docV1)

	_, err := e.uc.ReadHTML(ctx, bob, wsA, p.ID, "7")
	gt.True(t, errors.Is(err, model.ErrNotFound))
	plain := e.child(t, alice, wsA, "", "Plain")
	_, err = e.uc.ReadHTML(ctx, bob, wsA, plain.ID, "")
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
	mine := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Mine", Visibility: model.VisibilityPrivate, Kind: model.PaperKindHTML, HTML: docV1})).NoError(t)
	_, err = e.uc.ReadHTML(ctx, bob, wsA, mine.ID, "")
	gt.True(t, errors.Is(err, model.ErrNotFound))
}

func TestOpenHTML(t *testing.T) { // T47 (use case)
	e := newEnv(t)
	ctx := context.Background()
	alice, bob := e.user(t, "alice"), e.user(t, "bob")
	p := e.htmlPaper(t, alice, wsA, "Report", docV1)
	att := e.page(t, wsA, p.ID).Props.AttachmentID
	c := gt.R1(e.uc.OpenHTML(ctx, bob, wsA, p.ID, att)).NoError(t)
	gt.Equal(t, c.Attachment.ID, att)
	gt.False(t, c.ExternalResources) // the test workspaces are static, with no sandbox setting

	img := e.child(t, alice, wsA, "", "Pictures")
	png := gt.R1(e.uc.Upload(ctx, alice, wsA, img.ID, usecase.UploadInput{Name: "x.png", ContentType: "image/png", Body: strings.NewReader("png")})).NoError(t)
	_, err := e.uc.OpenHTML(ctx, alice, wsA, img.ID, png.ID)
	gt.True(t, errors.Is(err, model.ErrNotFound))
	_, err = e.uc.OpenHTML(ctx, alice, wsA, img.ID, att) // another paper's
	gt.True(t, errors.Is(err, model.ErrNotFound))
	mine := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Mine", Visibility: model.VisibilityPrivate, Kind: model.PaperKindHTML, HTML: docV2})).NoError(t)
	_, err = e.uc.OpenHTML(ctx, bob, wsA, mine.ID, e.page(t, wsA, mine.ID).Props.AttachmentID)
	gt.True(t, errors.Is(err, model.ErrNotFound))
}

func TestHTMLPaperVersions(t *testing.T) { // T30, T43
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	p := e.htmlPaper(t, alice, wsA, "Report", docV1)
	first := e.page(t, wsA, p.ID)
	gt.R1(e.uc.SaveVersion(ctx, alice, wsA, p.ID, "")).NoError(t)
	e.apply(t, alice, wsA, p.ID, setHTML(docV2, 1))
	gt.R1(e.uc.SaveVersion(ctx, alice, wsA, p.ID, "")).NoError(t)

	diff := gt.R1(e.uc.Diff(ctx, alice, wsA, p.ID, 1, 2)).NoError(t)
	gt.A(t, diff.Entries).Length(1)
	gt.Equal(t, diff.Entries[0].Kind, usecase.DiffChanged)

	gt.R1(e.uc.RestoreVersion(ctx, alice, wsA, p.ID, 1)).NoError(t)
	back := e.page(t, wsA, p.ID)
	gt.Equal(t, back.ID, first.ID)
	gt.Equal(t, back.Props.AttachmentID, first.Props.AttachmentID)
	gt.Equal(t, gt.R1(e.uc.ReadHTML(ctx, alice, wsA, p.ID, "")).NoError(t).HTML, docV1)
}

func TestHTMLPaperSearchAndFeed(t *testing.T) { // T31, T32
	e := newEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	alice := e.user(t, "alice")
	p := e.htmlPaper(t, alice, wsA, "Report", docV1)

	hits := gt.R1(e.uc.Search(ctx, alice, wsA, usecase.SearchInput{Query: "fourteen"})).NoError(t)
	gt.A(t, hits).Length(1)
	gt.Equal(t, hits[0].Paper.ID, p.ID)
	hidden := gt.R1(e.uc.Search(ctx, alice, wsA, usecase.SearchInput{Query: "hidden token"})).NoError(t)
	gt.A(t, hidden).Length(0)

	ch := gt.R1(e.uc.Watch(ctx, alice, wsA, p.ID)).NoError(t)
	res := e.apply(t, alice, wsA, p.ID, setHTML(docV2, 1))
	ev := <-ch
	gt.Equal(t, ev.Type, model.EventBlockPut)
	gt.Equal(t, ev.Block.Props.AttachmentID, res.Blocks[0].Props.AttachmentID)
}

func TestHTMLPaperMovesAcrossWorkspaces(t *testing.T) { // T33
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	p := e.htmlPaper(t, alice, wsA, "Report", docV1)
	gt.R1(e.uc.SaveVersion(ctx, alice, wsA, p.ID, "")).NoError(t)
	e.apply(t, alice, wsA, p.ID, setHTML(docV2, 1))

	e.apply(t, alice, wsA, p.ID, usecase.Op{Kind: usecase.OpMovePaper, Workspace: wsB, Parent: string(e.home(t, wsB))})
	fwd := gt.R1(e.repo.GetForward(ctx, wsA, p.ID)).NoError(t)
	cp := e.paper(t, wsB, fwd.ToPaperID)
	gt.Equal(t, cp.Kind, model.PaperKindHTML)
	page := e.page(t, wsB, cp.ID)
	gt.NotEqual(t, page.Props.AttachmentID, model.AttachmentID(""))
	cur := gt.R1(e.uc.ReadHTML(ctx, alice, wsB, cp.ID, "")).NoError(t)
	gt.Equal(t, cur.HTML, docV2)
	v1 := gt.R1(e.uc.ReadHTML(ctx, alice, wsB, cp.ID, "1")).NoError(t)
	gt.Equal(t, v1.HTML, docV1)
}

func TestHTMLPaperPurge(t *testing.T) { // T34
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	p := e.htmlPaper(t, alice, wsA, "Report", docV1)
	e.apply(t, alice, wsA, p.ID, setHTML(docV2, 1))
	gt.R1(e.uc.Trash(ctx, alice, wsA, p.ID)).NoError(t)
	e.clock.Advance(31 * 24 * time.Hour)
	gt.V(t, gt.R1(e.uc.PurgeTrash(ctx)).NoError(t)).Equal(1)
	for _, k := range e.store.Keys() {
		gt.False(t, strings.Contains(k, string(p.ID)))
	}
}

func TestHTMLPaperExportAndAttachments(t *testing.T) { // T35 (use case), T44, T45
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	p := e.htmlPaper(t, alice, wsA, "Report", docV1)
	_, err := e.uc.Export(ctx, alice, wsA, p.ID)
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	// HTML cannot be uploaded
	plain := e.child(t, alice, wsA, "", "Plain")
	_, err = e.uc.Upload(ctx, alice, wsA, plain.ID, usecase.UploadInput{Name: "x.html", ContentType: "text/html", Body: strings.NewReader("<p>x</p>")})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	// an image block does not show an HTML attachment
	att := e.page(t, wsA, p.ID).Props.AttachmentID
	gt.NoError(t, e.repo.PutAttachment(ctx, &model.Attachment{
		ID: att, WorkspaceID: wsA, PaperID: plain.ID, ContentType: model.HTMLContentType, Size: 3, SHA256: "s",
		CreatedBy: alice.Actor(), CreatedAt: e.clock.Now(),
	}))
	_, err = e.uc.Apply(ctx, alice, wsA, plain.ID, usecase.BatchInput{Ops: []usecase.Op{{Kind: usecase.OpInsert, Type: ptr(model.BlockImage), Props: &model.BlockProps{AttachmentID: att}}}})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))
}

func TestSetHTMLWritesFewRecords(t *testing.T) { // T50
	e := newEnv(t)
	alice := e.user(t, "alice")
	p := e.htmlPaper(t, alice, wsA, "Report", docV1)
	updated := e.paper(t, wsA, p.ID).UpdatedAt
	e.clock.Advance(10 * time.Second)
	res := e.apply(t, alice, wsA, p.ID, setHTML(docV2, 1))
	gt.True(t, len(res.Blocks[0].Content) <= model.MaxSearchTextLength)
	gt.False(t, strings.Contains(res.Blocks[0].Content, "<h1>"))
	// within the touch interval the paper record is not written
	gt.True(t, e.paper(t, wsA, p.ID).UpdatedAt.Equal(updated))
}

// signingStore is a store that could hand out signed URLs.
type signingStore struct{ *attachment.Memory }

func (signingStore) SignedURL(context.Context, string, time.Duration) (string, error) {
	return "https://storage.example/signed", nil
}

func TestOpenHTMLNeverSigns(t *testing.T) { // T46 (use case)
	e := newEnv(t)
	ctx := context.Background()
	uc := gt.R1(usecase.New(e.count, e.rules, signingStore{e.store}, e.cfg, usecase.WithClock(e.clock.Now), usecase.WithSyncJobs())).NoError(t)
	alice := e.user(t, "alice")
	p := gt.R1(uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Report", Kind: model.PaperKindHTML, HTML: docV1})).NoError(t)
	c := gt.R1(uc.OpenHTML(ctx, alice, wsA, p.ID, e.page(t, wsA, p.ID).Props.AttachmentID)).NoError(t)
	body := gt.R1(c.Open(ctx)).NoError(t)
	_ = body.Close()
}

func TestHTMLRejectedBeforeStoring(t *testing.T) { // review: refuse what the transaction would refuse, before storing
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	p := e.htmlPaper(t, alice, wsA, "Report", docV1)
	priv := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Mine", Visibility: model.VisibilityPrivate})).NoError(t)
	loose := gt.R1(e.uc.CreatePaper(ctx, alice, wsA, usecase.CreateInput{Title: "Loose"})).NoError(t)
	objects := htmlAttachments(t, e)

	cases := map[string]struct {
		paper model.PaperID
		ops   []usecase.Op
	}{
		"set_html with another block op": {p.ID, []usecase.Op{setHTML(docV2, 1), para("x")}},
		"paper_kind on an update":        {loose.ID, []usecase.Op{{Kind: usecase.OpUpdate, PaperKind: ptr(model.PaperKindHTML)}}},
		"html child of a paragraph":      {loose.ID, []usecase.Op{{Kind: usecase.OpInsert, Type: ptr(model.BlockParagraph), PaperKind: ptr(model.PaperKindHTML), HTML: ptr(docV1)}}},
		"html child of a private paper":  {priv.ID, []usecase.Op{{Kind: usecase.OpInsert, Type: ptr(model.BlockPaper), PaperKind: ptr(model.PaperKindHTML), HTML: ptr(docV1)}}},
		"html child of an unfiled paper": {loose.ID, []usecase.Op{{Kind: usecase.OpInsert, Type: ptr(model.BlockPaper), PaperKind: ptr(model.PaperKindHTML), HTML: ptr(docV1)}}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := e.uc.Apply(ctx, alice, wsA, c.paper, usecase.BatchInput{Ops: c.ops})
			gt.True(t, errors.Is(err, model.ErrInvalidArgument))
			gt.Equal(t, htmlAttachments(t, e), objects)
		})
	}
}

func TestHTMLPapersHaveNoImages(t *testing.T) { // review
	e := newEnv(t)
	ctx := context.Background()
	alice := e.user(t, "alice")
	p := e.htmlPaper(t, alice, wsA, "Report", docV1)
	_, err := e.uc.Upload(ctx, alice, wsA, p.ID, usecase.UploadInput{Name: "x.png", ContentType: "image/png", Body: strings.NewReader("png")})
	gt.True(t, errors.Is(err, model.ErrInvalidArgument))

	// the image route does not serve an HTML document
	att := e.page(t, wsA, p.ID).Props.AttachmentID
	_, err = e.uc.OpenAttachment(ctx, alice, wsA, p.ID, att)
	gt.True(t, errors.Is(err, model.ErrNotFound))

	// an upload whose bytes equal an HTML document of the paper is a new image
	plain := e.child(t, alice, wsA, "", "Plain")
	gt.NoError(t, e.repo.PutAttachment(ctx, &model.Attachment{
		ID: model.NewAttachmentID(), WorkspaceID: wsA, PaperID: plain.ID, ContentType: model.HTMLContentType, Size: 3,
		SHA256: "8f8cbb7dcf46e0bc7d53265749a6c17d116093a6ba95e442764060c76fd4a86c", CreatedBy: alice.Actor(), CreatedAt: e.clock.Now(),
	}))
	img := gt.R1(e.uc.Upload(ctx, alice, wsA, plain.ID, usecase.UploadInput{Name: "x.png", ContentType: "image/png", Body: strings.NewReader("png")})).NoError(t)
	gt.True(t, img.IsImage())
}
