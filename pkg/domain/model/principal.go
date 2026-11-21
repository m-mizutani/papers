package model

// Principal is whoever is making the current request: a user, and the token
// they used if any. It is built per request and never stored.
type Principal struct {
	User  *User
	Token *APIToken
	// AgentName marks MCP calls made with a browser session (development).
	AgentName string
}

// Actor is the record a write leaves behind.
func (p *Principal) Actor() Actor {
	a := Actor{Kind: ActorHuman, UserID: p.User.ID, Name: p.User.Name}
	if a.Name == "" {
		a.Name = p.User.Email
	}
	if p.Token != nil {
		a.TokenID = p.Token.ID
		if p.Token.Kind == TokenAgent {
			a.Kind = ActorAgent
			a.AgentName = p.Token.AgentName
		}
	}
	if a.Kind == ActorHuman && p.AgentName != "" {
		a.Kind = ActorAgent
		a.AgentName = p.AgentName
	}
	return a
}

// TokenAllows reports whether the token (if any) lets the principal use the
// workspace at all.
func (p *Principal) TokenAllows(ws WorkspaceID) bool {
	return p.Token == nil || p.Token.AllowsWorkspace(ws)
}

// CanWrite reports whether the token (if any) allows writing.
func (p *Principal) CanWrite() bool {
	return p.Token == nil || p.Token.Scope == ScopeWrite
}

// SeesPaper reports whether the principal may see the paper, given that it
// can use the paper's workspace. Private papers are seen only by their
// owner; trashed papers too, so that the owner can restore them.
func (p *Principal) SeesPaper(paper *Paper) bool {
	if paper.IsPrivate() || paper.IsTrashed() {
		return paper.Owner() == p.User.ID
	}
	return true
}

// Owns reports whether the principal is the paper's creator (or acts for
// them). Only the owner deletes, restores and purges.
func (p *Principal) Owns(paper *Paper) bool {
	return paper.Owner() == p.User.ID
}
