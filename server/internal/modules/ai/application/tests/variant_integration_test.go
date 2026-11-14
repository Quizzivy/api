//go:build integration

package application_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"quizzivy/internal/modules/ai/application"
	"quizzivy/internal/modules/ai/application/ports"
	"quizzivy/internal/modules/ai/application/query"
	"quizzivy/internal/modules/ai/domain"
	"quizzivy/internal/modules/ai/domain/prompts"
	"quizzivy/internal/modules/ai/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/actor"
)

// varying is a teacher with a draft of the given length in a month of its
// own, over the real repository, so what a variant pass does to the pool, the
// teacher's month and the ledger is read from the tables.
func (w *world) varying(completer ports.Completer, questions int, pool int64, logs *strings.Builder) cleaning {
	w.t.Helper()
	month := w.newMonth()
	w.period(month, pool)
	teacher := w.teacher()
	r := &reads{paper: longPaper(questions), prefs: domain.Preferences{Language: domain.English, ReadStudentAnswers: true}}
	app := application.New(repositories.NewPostgres(db.NewContext(w.pool)), application.Options{
		Prices: testPrices, Zone: saigon, Logger: slog.New(slog.NewTextHandler(logs, nil)), Completer: completer, Tests: r, People: r,
	})
	app.SetClock(func() time.Time { return *w.clock.Load() })
	return cleaning{world: w, month: month, teacher: teacher, app: app}
}

func (c cleaning) vary(ctx context.Context) (query.Varied, error) {
	return c.app.Queries.Variant.Handle(ctx, query.Variant{
		Actor:  actor.Actor{ID: c.teacher, Scope: access.Scope{UserID: c.teacher}},
		TestID: "t-1", Changes: []string{prompts.VariantReword}, Difficulty: prompts.DifficultySame,
	})
}

// The overshoot of VR-10, against the tables: three requests the provider
// meters far above the variant's ceiling move exactly the ceiling from held to
// used, on the pool and on the teacher, in one ledger row that keeps the real
// token counts; nothing stays held, and the overrun is logged.
func TestAVariantMeteredAboveItsCeilingMovesOnlyWhatWasHeld(t *testing.T) {
	w := newWorld(t)
	completer := &scripted{respond: func(_ context.Context, req ports.Request) (ports.Response, error) { return rewordEach(req) }}
	logs := &strings.Builder{}
	c := w.varying(completer, 24, 100_000, logs)
	ceiling := domain.FeatureVariant.Ceiling()

	got, err := c.vary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Changes) != 24 || got.Usage.Credits != ceiling {
		t.Errorf("%d changes for %d credits, want all 24 for the %d that were held", len(got.Changes), got.Usage.Credits, ceiling)
	}
	rows := c.ledger()
	if len(rows) != 1 {
		t.Fatalf("%d ledger rows, want one for the whole pass", len(rows))
	}
	row := rows[0]
	if row.feature != "variant" || row.tier != "capable" || row.input != 3*perCall.InputTokens || row.output != 3*perCall.OutputTokens || row.credits != ceiling {
		t.Errorf("ledger %+v, want the three requests' tokens on the capable tier and %d credits", row, ceiling)
	}
	if tr, p := c.teacherRow(c.month, c.teacher), c.periodRow(c.month); tr.used != ceiling || p.used != ceiling {
		t.Errorf("teacher %+v, pool %+v; want %d used on both, whatever was metered", tr, p, ceiling)
	}
	c.nothingHeld()
	if text := logs.String(); !strings.Contains(text, "ai call cost more than its ceiling") || !strings.Contains(text, "feature=variant") {
		t.Errorf("the overrun was not logged:\n%s", text)
	}
}
