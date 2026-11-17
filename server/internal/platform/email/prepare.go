package email

import (
	"context"
	"time"
)

// SkippedCode is the last_error_code of a row a Preparer dropped: closed as
// failed and never sent, so housekeeping prunes it like any other.
const SkippedCode = "SKIPPED"

// Draft is a row the dispatcher is about to send, as its template's Preparer
// sees it: the recipient's account, the params as stored, when the row was
// written, and the dispatcher's clock.
type Draft struct {
	ToUserID  string
	Params    Params
	CreatedAt time.Time
	Now       time.Time
}

// Preparer is a template's last look at a message before it is rendered
// (T-R7.13). It answers the params to render, which it may refresh from the
// database (a bucket's final count, the current title, the recipient's name),
// and whether to send at all: false drops the row, as when the thread was
// read, the assignment closed or the recipient turned the switch off since.
// An error is a failure the dispatcher tries again later, as a transient send
// failure.
type Preparer interface {
	Prepare(ctx context.Context, d Draft) (Params, bool, error)
}

// PreparerFunc is a function as a Preparer.
type PreparerFunc func(ctx context.Context, d Draft) (Params, bool, error)

// Prepare calls f.
func (f PreparerFunc) Prepare(ctx context.Context, d Draft) (Params, bool, error) { return f(ctx, d) }

// Preparers are the Preparer of each template that has one. A template that
// is Prepared and has none here is not sent by the dispatcher holding them.
type Preparers map[Template]Preparer

// sendable lists the templates a dispatcher with p sends: every template
// that needs no Preparer, and each Prepared one p has a Preparer for.
func (p Preparers) sendable() []string {
	names := make([]string, 0, len(definitions))
	for t, def := range definitions {
		if !def.prepared || p[t] != nil {
			names = append(names, string(t))
		}
	}
	return names
}
