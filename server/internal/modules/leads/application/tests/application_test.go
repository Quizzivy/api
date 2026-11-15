package application_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"quizzivy/internal/modules/leads/application"
	"quizzivy/internal/modules/leads/application/command"
	"quizzivy/internal/modules/leads/application/query"
	"quizzivy/internal/modules/leads/domain"
	"quizzivy/internal/shared/actor"
	"quizzivy/internal/shared/paging"
	"quizzivy/internal/shared/validation"
)

var now = time.Date(2026, 11, 15, 9, 30, 0, 0, time.UTC)

const (
	typedName  = "Trần Thị Mai"
	typedPhone = "0903 123 456"
	digits     = "0903123456"
)

type memory struct {
	mu      sync.Mutex
	stored  []domain.Submit
	submits int
	updates []domain.Update
	deletes []domain.Delete
	lists   []domain.ListQuery
}

func (m *memory) Submit(_ context.Context, in domain.Submit) (domain.Drop, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.submits++
	recent := 0
	for _, s := range m.stored {
		if s.Submission.PhoneDigits == in.Submission.PhoneDigits && s.Now.After(in.Now.Add(-domain.RepeatWindow)) {
			recent++
		}
	}
	verdict := in.Submission.Verdict(recent)
	if verdict == domain.Stored {
		m.stored = append(m.stored, in)
	}
	return verdict, nil
}

func (m *memory) List(_ context.Context, q domain.ListQuery) ([]domain.Lead, paging.Page, error) {
	m.lists = append(m.lists, q)
	return nil, paging.Page{Number: 1, Size: domain.DefaultPageSize}, nil
}

func (m *memory) Get(context.Context, string) (domain.Lead, error) {
	return domain.Lead{}, domain.ErrNotFound
}

func (m *memory) Update(_ context.Context, in domain.Update) (domain.Lead, error) {
	m.updates = append(m.updates, in)
	return domain.Lead{ID: in.ID}, nil
}

func (m *memory) Delete(_ context.Context, in domain.Delete) error {
	m.deletes = append(m.deletes, in)
	return nil
}

func form() domain.Form {
	return domain.Form{
		FullName: typedName, Phone: typedPhone, Interests: []string{"ielts"}, CourseFor: "self",
		CallTime: "any", Locale: "vi", PrivacyNoticeVersion: "2026-11",
	}
}

func newApp(repo *memory) (*application.Application, *bytes.Buffer) {
	var logged bytes.Buffer
	untimed := func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return a
	}
	app := application.New(repo, slog.New(slog.NewJSONHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: untimed})))
	app.SetClock(func() time.Time { return now })
	return app, &logged
}

func submit(t *testing.T, app *application.Application, f domain.Form) {
	t.Helper()
	if _, err := app.Commands.Submit.Handle(context.Background(), command.Submit{Form: f}); err != nil {
		t.Fatalf("submit: %v", err)
	}
}

// TestADroppedRequestIsLoggedByItsReasonAndNothingThePersonTyped fails when the
// handler stops noting a dropped request, notes a stored one, or puts the
// name, a word of it, the number as typed or its digits in the log line. The
// domain's verdict test decides which request is dropped; this one is about
// what the operator's log holds afterwards.
func TestADroppedRequestIsLoggedByItsReasonAndNothingThePersonTyped(t *testing.T) {
	repo := &memory{}
	app, logged := newApp(repo)
	submit(t, app, form())
	submit(t, app, form())
	if logged.Len() != 0 {
		t.Fatalf("a stored request was logged: %s", logged)
	}
	submit(t, app, form())
	trapped := form()
	trapped.Phone, trapped.Website = "0911 222 333", "https://spam.example/buy"
	submit(t, app, trapped)

	lines := strings.Split(strings.TrimSpace(logged.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"msg":"lead_dropped"`) || !strings.Contains(lines[0], `"reason":"repeat"`) ||
		!strings.Contains(lines[1], `"msg":"lead_dropped"`) || !strings.Contains(lines[1], `"reason":"honeypot"`) {
		t.Fatalf("log = %q, want lead_dropped with reason repeat, then with reason honeypot", lines)
	}
	for _, typed := range []string{typedName, "Mai", "Trần", typedPhone, digits, "0903", "0911", "spam.example", "ielts"} {
		if strings.Contains(logged.String(), typed) {
			t.Errorf("the log holds %q, which the person typed: %s", typed, logged)
		}
	}
	if len(repo.stored) != 2 || repo.submits != 4 {
		t.Errorf("%d of %d submissions were stored, want 2 of 4, each one judged by the store", len(repo.stored), repo.submits)
	}
}

// TestAFormThatCannotBeStoredIsRefusedBeforeTheStoreIsAsked fails when the
// handler hands the store a form it did not normalise, or swallows the
// refusal and answers as if the request were received.
func TestAFormThatCannotBeStoredIsRefusedBeforeTheStoreIsAsked(t *testing.T) {
	repo := &memory{}
	app, logged := newApp(repo)
	bad := form()
	bad.Phone = "12 34"
	_, err := app.Commands.Submit.Handle(context.Background(), command.Submit{Form: bad})
	var invalid *validation.Error
	if !errors.As(err, &invalid) || len(invalid.Fields) != 1 || invalid.Fields[0].Field != domain.FieldPhone {
		t.Fatalf("error = %v, want phone refused", err)
	}
	if repo.submits != 0 || logged.Len() != 0 {
		t.Errorf("the store was asked %d times and the log holds %q", repo.submits, logged)
	}
}

// TestAStoredRequestCarriesTheNormalisedFormAndTheClocksTime fails when the
// handler stores the name or number as typed, or stamps the request with
// another time than the application's clock, which the 24-hour window and the
// consent time both read.
func TestAStoredRequestCarriesTheNormalisedFormAndTheClocksTime(t *testing.T) {
	repo := &memory{}
	app, _ := newApp(repo)
	f := form()
	f.FullName = "  Trần   Thị Mai "
	submit(t, app, f)
	if len(repo.stored) != 1 {
		t.Fatalf("%d requests stored", len(repo.stored))
	}
	got := repo.stored[0]
	if got.Submission.FullName != typedName || got.Submission.PhoneDigits != digits || !got.Now.Equal(now) {
		t.Errorf("stored %+v at %v, want the tidied name, the digits and %v", got.Submission, got.Now, now)
	}
}

// TestAnUpdateNamesItsActorAndCarriesTheTidiedChange fails when the handler
// drops who is acting or from where (the audit row then names nobody), passes
// the note untrimmed, or lets a note the column cannot hold reach the store.
func TestAnUpdateNamesItsActorAndCarriesTheTidiedChange(t *testing.T) {
	repo := &memory{}
	app, _ := newApp(repo)
	who := actor.Actor{ID: "019535d9-3df7-79fb-b466-fa907fa17f9e", IP: "203.0.113.9", UserAgent: "test-agent"}
	closed := domain.StatusClosed
	_, err := app.Commands.Update.Handle(context.Background(), command.Update{
		Actor: who, ID: "019535d9-3df7-79fb-b466-fa907fa17f01", Change: domain.Change{Status: &closed, NoteSet: true, Note: "  Đã gọi  "},
	})
	if err != nil || len(repo.updates) != 1 {
		t.Fatalf("update: %v (%d reached the store)", err, len(repo.updates))
	}
	got := repo.updates[0]
	if got.ActorID != who.ID || got.IP != who.IP || got.UserAgent != who.UserAgent || !got.Now.Equal(now) ||
		got.ID != "019535d9-3df7-79fb-b466-fa907fa17f01" || got.Change.Note != "Đã gọi" || *got.Change.Status != domain.StatusClosed {
		t.Errorf("the store was given %+v", got)
	}
	_, err = app.Commands.Update.Handle(context.Background(), command.Update{
		Actor: who, ID: got.ID, Change: domain.Change{NoteSet: true, Note: strings.Repeat("ă", 1001)},
	})
	var invalid *validation.Error
	if !errors.As(err, &invalid) || len(repo.updates) != 1 {
		t.Errorf("a note of 1001 characters: %v, with %d updates at the store", err, len(repo.updates))
	}
}

// TestADeleteNamesItsActorAndTheListTrimsItsSearch fails when the delete
// reaches the store without who asked for it, and when a search typed with
// spaces around it is compared as typed.
func TestADeleteNamesItsActorAndTheListTrimsItsSearch(t *testing.T) {
	repo := &memory{}
	app, _ := newApp(repo)
	who := actor.Actor{ID: "019535d9-3df7-79fb-b466-fa907fa17f9e", IP: "203.0.113.9", UserAgent: "test-agent"}
	if _, err := app.Commands.Delete.Handle(context.Background(), command.Delete{Actor: who, ID: "019535d9-3df7-79fb-b466-fa907fa17f01"}); err != nil {
		t.Fatal(err)
	}
	if len(repo.deletes) != 1 || repo.deletes[0].ActorID != who.ID || repo.deletes[0].IP != who.IP || !repo.deletes[0].Now.Equal(now) {
		t.Errorf("the store was given %+v", repo.deletes)
	}
	if _, err := app.Queries.List.Handle(context.Background(), query.List{Status: domain.StatusNew, Q: "  mai 0903 ", Page: 2, Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if len(repo.lists) != 1 || repo.lists[0] != (domain.ListQuery{Status: domain.StatusNew, Q: "mai 0903", Page: 2, Limit: 10}) {
		t.Errorf("the store was asked %+v", repo.lists)
	}
}
