package model_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/m-mizutani/gt"

	"github.com/m-mizutani/papers/pkg/domain/model"
)

func validPaper() *model.Paper {
	actor := model.Actor{Kind: model.ActorHuman, UserID: "u1"}
	return &model.Paper{ID: model.NewPaperID(), WorkspaceID: "ws", Title: "T", Visibility: model.VisibilityPublic, CreatedBy: actor, UpdatedBy: actor}
}

func TestPaperValidate(t *testing.T) {
	gt.NoError(t, validPaper().Validate())

	p := validPaper()
	p.Title = strings.Repeat("x", model.MaxTitleLength+1)
	gt.Error(t, p.Validate())

	p = validPaper()
	p.Title = "a\nb"
	gt.Error(t, p.Validate())

	p = validPaper()
	p.CreatedBy = model.Actor{}
	gt.Error(t, p.Validate())

	p = validPaper()
	p.Visibility = "secret"
	gt.Error(t, p.Validate())

	t.Run("private papers stay out of the tree", func(t *testing.T) {
		p := validPaper()
		p.Visibility = model.VisibilityPrivate
		p.ParentID = model.NewPaperID()
		gt.Error(t, p.Validate())
	})
	t.Run("a public paper without a parent is unfiled", func(t *testing.T) {
		// T33
		p := validPaper()
		p.ParentID = ""
		gt.NoError(t, p.Validate())
		p.ParentID = model.NewPaperID()
		gt.NoError(t, p.Validate())
	})
	t.Run("deleted_at and deleted_with go together", func(t *testing.T) {
		p := validPaper()
		now := time.Now()
		p.DeletedAt = &now
		gt.Error(t, p.Validate())
		p.DeletedWith = p.ID
		gt.NoError(t, p.Validate())
	})
	t.Run("ids must be generated ids", func(t *testing.T) {
		p := validPaper()
		p.ID = "p"
		gt.Error(t, p.Validate())
	})
}

func TestPaperStatus(t *testing.T) {
	p := validPaper()
	gt.Equal(t, p.Status(), model.StatusDraft)
	p.VersionNumber = 2
	gt.Equal(t, p.Status(), model.StatusCanonical)
	now := time.Now()
	p.ArchivedAt = &now
	gt.Equal(t, p.Status(), model.StatusArchived)
	p.DeletedAt, p.DeletedWith = &now, p.ID
	gt.Equal(t, p.Status(), model.StatusTrashed)
}

func TestPrincipalSeesPaper(t *testing.T) {
	alice := &model.User{ID: "alice", Email: "alice@example.com"}
	bob := &model.User{ID: "bob", Email: "bob@example.com"}
	paper := validPaper()
	paper.CreatedBy = model.Actor{Kind: model.ActorAgent, UserID: "alice", AgentName: "Claude"}

	pa := &model.Principal{User: alice}
	pb := &model.Principal{User: bob}
	gt.True(t, pa.SeesPaper(paper))
	gt.True(t, pb.SeesPaper(paper))
	gt.True(t, pa.Owns(paper)) // an agent's paper belongs to the user it acted for
	gt.False(t, pb.Owns(paper))

	paper.Visibility = model.VisibilityPrivate
	gt.True(t, pa.SeesPaper(paper))
	gt.False(t, pb.SeesPaper(paper))

	paper.Visibility = model.VisibilityPublic
	now := time.Now()
	paper.DeletedAt, paper.DeletedWith = &now, paper.ID
	gt.True(t, pa.SeesPaper(paper))
	gt.False(t, pb.SeesPaper(paper))
}

func TestPrincipalActorAndToken(t *testing.T) {
	alice := &model.User{ID: "alice", Email: "alice@example.com", Name: "Alice"}
	p := &model.Principal{User: alice}
	gt.Equal(t, p.Actor(), model.Actor{Kind: model.ActorHuman, UserID: "alice", Name: "Alice"})
	gt.True(t, p.CanWrite())
	gt.True(t, p.TokenAllows("any"))

	p.Token = &model.APIToken{ID: "t", Kind: model.TokenAgent, AgentName: "Claude", Scope: model.ScopeRead, Workspaces: []model.WorkspaceID{"sec"}}
	a := p.Actor()
	gt.Equal(t, a.Kind, model.ActorAgent)
	gt.Equal(t, a.AgentName, "Claude")
	gt.Equal(t, a.TokenID, "t")
	gt.False(t, p.CanWrite())
	gt.True(t, p.TokenAllows("sec"))
	gt.False(t, p.TokenAllows("other"))

	// a personal token stays human; the token id marks "via API"
	p.Token = &model.APIToken{ID: "t2", Kind: model.TokenPersonal, Scope: model.ScopeWrite}
	gt.Equal(t, p.Actor().Kind, model.ActorHuman)
	gt.Equal(t, p.Actor().TokenID, "t2")
}

func TestIDs(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := string(model.NewPaperID())
		gt.Equal(t, len(id), model.IDLength)
		gt.True(t, model.ValidID(id))
		gt.False(t, seen[id])
		seen[id] = true
		gt.False(t, strings.ContainsAny(id, "-_"))
	}
	gt.False(t, model.ValidID("short"))
	gt.False(t, model.ValidID("0000000000000000000000")) // not a v4 UUID
	gt.False(t, model.ValidID("zzzzzzzzzzzzzzzzzzzzzz")) // more than 128 bits
	gt.False(t, model.ValidID("01890a5d-ac96-774b-bcce-b302099a8057"))

	gt.True(t, model.ValidWorkspaceID("security"))
	gt.True(t, model.ValidWorkspaceID("sec-ops-2"))
	gt.False(t, model.ValidWorkspaceID("Sec"))
	gt.False(t, model.ValidWorkspaceID("a--b"))
	gt.False(t, model.ValidWorkspaceID("-a"))
	gt.False(t, model.ValidWorkspaceID(model.WorkspaceID(strings.Repeat("a", 64))))
}

func TestWorkspaceValidate(t *testing.T) {
	ws := &model.Workspace{ID: "security", Name: "Security"}
	gt.NoError(t, ws.Validate())
	ws.Emoji, ws.Color = "🛡️", "blue"
	gt.Error(t, ws.Validate())
	gt.Error(t, (&model.Workspace{ID: "Bad", Name: "x"}).Validate())
	gt.Error(t, (&model.Workspace{ID: "ok"}).Validate())
}

func TestRecentTouch(t *testing.T) {
	r := &model.Recent{}
	ids := make([]model.PaperID, 0, 12)
	for i := 0; i < 12; i++ {
		id := model.NewPaperID()
		ids = append(ids, id)
		r.Touch(id, time.Now())
	}
	gt.Equal(t, len(r.PaperIDs), model.MaxRecent)
	gt.Equal(t, r.PaperIDs[0], ids[11])
	r.Touch(ids[5], time.Now())
	gt.Equal(t, r.PaperIDs[0], ids[5])
	gt.Equal(t, len(r.PaperIDs), model.MaxRecent)
}

func TestPaperQueryAndCursor(t *testing.T) {
	q := model.PaperQuery{Sort: model.SortUpdated, Limit: 10}
	gt.NoError(t, q.Validate())
	for _, bad := range []model.PaperQuery{
		{Sort: "title", Limit: 10},
		{Sort: model.SortCreated, Limit: 0},
		{Sort: model.SortCreated, Limit: model.MaxPaperListLimit + 1},
		{Sort: model.SortCreated, Limit: 1, Cursor: "not a cursor"},
	} {
		gt.True(t, errors.Is(bad.Validate(), model.ErrInvalidArgument))
	}

	p := validPaper()
	p.CreatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p.UpdatedAt = time.Date(2026, 2, 1, 0, 0, 0, 123456000, time.UTC)
	c := gt.R1(model.ParsePaperCursor(model.CursorOf(model.SortUpdated, p))).NoError(t)
	gt.True(t, c.At.Equal(p.UpdatedAt))
	gt.Equal(t, c.ID, p.ID)
	c = gt.R1(model.ParsePaperCursor(model.CursorOf(model.SortCreated, p))).NoError(t)
	gt.True(t, c.At.Equal(p.CreatedAt))

	older := validPaper()
	older.CreatedAt = p.CreatedAt.Add(-time.Second)
	gt.True(t, c.After(model.SortCreated, older))
	gt.False(t, c.After(model.SortCreated, p))
	same := validPaper()
	same.CreatedAt = p.CreatedAt
	gt.Equal(t, c.After(model.SortCreated, same), same.ID < p.ID)
}
