package config

import "time"

func NewServerForTest(baseURL string) *Server {
	return &Server{baseURL: baseURL, maxOpsPerBatch: 1, paperTouch: time.Minute, trashRetention: time.Hour}
}

func (x *Server) SetMCPBaseURLForTest(v string) { x.mcpBaseURL = v }

func NewAuthForTest(clientID, secret string, noAuthn bool, domains []string) *Auth {
	return &Auth{googleClientID: clientID, googleClientSecret: secret, noAuthn: noAuthn, allowedDomains: domains, sessionTTL: time.Hour, devUserEmail: "dev@localhost", devUserName: "Dev"}
}

func NewAnonymousAuthForTest(clientID string, noAuthn bool) *Auth {
	return &Auth{googleClientID: clientID, googleClientSecret: "secret", noAuthn: noAuthn, anonymous: true, allowedDomains: []string{"example.com"}, sessionTTL: time.Hour, devUserEmail: "dev@localhost", devUserName: "Dev"}
}

func NewRepositoryForTest(backend, projectID string, buffer int) *Repository {
	return &Repository{backend: backend, projectID: projectID, eventBuffer: buffer}
}

func NewWorkspacesForTest(dir, global string) *Workspaces {
	return &Workspaces{dir: dir, global: global}
}

func NewAttachmentsForTest(backend, dir string) *Attachments {
	return &Attachments{backend: backend, dir: dir}
}
