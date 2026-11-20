package model

// Principal is the authenticated caller of a request.
type Principal struct {
	User *User
	// Groups are the IDs of groups the user belongs to, resolved once per
	// request.
	Groups []GroupID
	// SystemAdmin users are owners of everything. Configured by email at
	// deployment time, never stored.
	SystemAdmin bool
	// Token is set when the request was authenticated by an API token; it
	// caps the user's roles by its scope and workspace list.
	Token *APIToken
	// HolderKey identifies the caller's live connection for soft locks.
	HolderKey string
	// AgentName marks requests made by an AI client (e.g. through MCP) even
	// when they are not authenticated by an agent token.
	AgentName string
}

// Actor returns the actor recorded for writes made by the principal.
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

func (p *Principal) inGroup(id GroupID) bool {
	for _, g := range p.Groups {
		if g == id {
			return true
		}
	}
	return false
}

func (p *Principal) matches(s Subject) bool {
	switch s.Kind {
	case SubjectUser:
		return UserID(s.ID) == p.User.ID
	case SubjectGroup:
		return p.inGroup(GroupID(s.ID))
	case SubjectDomain:
		return s.ID != "" && p.User.EmailDomain() == s.ID
	case SubjectAuthenticated:
		return true
	default:
		return false
	}
}

func (p *Principal) roleFromBindings(bs []RoleBinding) Role {
	r := RoleNone
	for _, b := range bs {
		if p.matches(b.Subject) {
			r = r.Max(b.Role)
		}
	}
	return r
}

// tokenCap returns the highest role the principal's token allows in ws.
func (p *Principal) tokenCap(ws WorkspaceID) Role {
	if p.Token == nil {
		return RoleOwner
	}
	if !p.Token.AllowsWorkspace(ws) {
		return RoleNone
	}
	switch p.Token.Scope {
	case ScopeWrite:
		// Tokens never administer: access control stays a human decision.
		return RoleEditor
	case ScopeRead:
		return RoleViewer
	default:
		return RoleNone
	}
}

// WorkspaceRole is the principal's effective role in ws.
func (p *Principal) WorkspaceRole(ws *Workspace) Role {
	r := RoleNone
	if p.SystemAdmin {
		r = RoleOwner
	} else {
		r = p.roleFromBindings(ws.Bindings)
	}
	return r.Min(p.tokenCap(ws.ID))
}

// PaperRole is the principal's effective role on paper in ws.
//
//   - Workspace owners (and system admins) own every paper.
//   - A human creator of a paper owns it.
//   - Unrestricted papers: max(workspace role, paper bindings).
//   - Restricted papers: paper bindings only.
//
// The result is capped by the token, if any.
func (p *Principal) PaperRole(ws *Workspace, paper *Paper) Role {
	var r Role
	wsRole := RoleNone
	if p.SystemAdmin {
		wsRole = RoleOwner
	} else {
		wsRole = p.roleFromBindings(ws.Bindings)
	}

	switch {
	case wsRole == RoleOwner:
		r = RoleOwner
	case paper.CreatedBy.UserID == p.User.ID && wsRole.AtLeast(RoleEditor):
		// Creators keep ownership only while they can still edit the
		// workspace; removing someone from a workspace removes their papers'
		// ownership too.
		r = RoleOwner
	case paper.Access.Restricted:
		r = p.roleFromBindings(paper.Access.Bindings)
	default:
		r = wsRole.Max(p.roleFromBindings(paper.Access.Bindings))
	}
	return r.Min(p.tokenCap(ws.ID))
}
