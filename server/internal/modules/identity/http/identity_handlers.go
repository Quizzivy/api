package http

import (
	"time"

	"quizzivy/internal/modules/identity/application"
	"quizzivy/internal/modules/identity/application/token"
	"quizzivy/internal/shared/access"
)

type Identity struct {
	app          *application.Application
	refreshTTL   time.Duration
	cookieSecure bool
	docs         *token.Issuer
	hosts        access.Hosts
}

// NewIdentity builds the identity transport of a deployment with one web
// host. docs issues API reference sessions; nil leaves openDocsSession
// answering 501.
func NewIdentity(app *application.Application, refreshTTL time.Duration, cookieSecure bool, docs *token.Issuer) Identity {
	return Identity{app: app, refreshTTL: refreshTTL, cookieSecure: cookieSecure, docs: docs}
}

// WithHosts returns h reading a request's Origin against hosts: each of the
// two web hosts has its own refresh cookie and admits only the accounts that
// belong at it.
func (h Identity) WithHosts(hosts access.Hosts) Identity {
	h.hosts = hosts
	return h
}
