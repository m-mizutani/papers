package config

import "time"

func NewServerForTest(baseURL string) *Server {
	return &Server{baseURL: baseURL, lockTTL: time.Second, workspaceCreation: "everyone", maxOpsPerCommit: 1, paperTouch: time.Minute}
}

func NewAuthForTest(clientID, secret string, noAuthn bool, domains []string) *Auth {
	return &Auth{googleClientID: clientID, googleClientSecret: secret, noAuthn: noAuthn, allowedDomains: domains, sessionTTL: time.Hour, devUserEmail: "dev@localhost", devUserName: "Dev"}
}

func NewRepositoryForTest(backend, projectID string, buffer int) *Repository {
	return &Repository{backend: backend, projectID: projectID, eventBuffer: buffer}
}
