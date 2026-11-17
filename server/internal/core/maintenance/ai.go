package maintenance

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"strings"
	"time"

	aiapp "quizzivy/internal/modules/ai/application"
	aicommand "quizzivy/internal/modules/ai/application/command"
	aiquery "quizzivy/internal/modules/ai/application/query"
	"quizzivy/internal/modules/ai/domain"
	airepo "quizzivy/internal/modules/ai/repositories"
	notificationsapp "quizzivy/internal/modules/notifications/application"
	notificationsemail "quizzivy/internal/modules/notifications/application/email"
	notificationsrepo "quizzivy/internal/modules/notifications/repositories"
	"quizzivy/internal/platform/config"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/platform/email"
)

const (
	aiCredits = "ai-credits"
	aiSweep   = "ai-reservations-sweep"
)

const maxOperatorLength = 100

// AIReport is what an ai-credits command did, or with Applied false would have done: the credits commands run through the same application handlers as the Admin operations, and a dry run runs them inside a transaction that is rolled back, so nothing is written and nobody is told.
type AIReport struct {
	Command  string `json:"command"`
	Operator string `json:"operator,omitempty"`
	Applied  bool   `json:"applied"`
	Result   any    `json:"result"`
}

type aiAction func(ctx context.Context, app *aiapp.Application, by domain.Author) (any, error)

// RunAI runs act through the credits application over conn, as System with the operator named in the audit row. With apply false it runs inside a transaction that it rolls back and gives the application no way to tell anyone, so it writes no notice and queues no email. With apply true it commits and tells the teacher of a decided request as the Admin operation does (T-R7.23, the owner's decision of 2026-11-16): in the app, and by an email that Notify's email channel queues in app.email_outbox unless NOTIFY_EMAIL_EVENTS, read from the console's environment as Load reads the API's, turns it off; a value the API would refuse fails the command before it writes. Migration 00173 grants quizzivy_maintenance what that path runs and lets it queue nothing else. The console sends nothing itself: the API's and the worker's dispatchers send the row, and drop it if their own NOTIFY_EMAIL_EVENTS turns it off. The console cannot wake them, so the row waits for a dispatcher's next pass, which each of them runs at least once a minute (wiring.Mail.Run): the teacher has the email within about a minute, where the Admin operation's wakes its own process's dispatcher at once. As in the API, a notice or an email that fails is logged, here on the console's standard error, and never fails the decision, which is already committed.
func RunAI(ctx context.Context, conn db.Conn, name, operator string, apply bool, act aiAction) (AIReport, error) {
	report := AIReport{Command: name, Operator: operator}
	zone, err := config.AppLocation()
	if err != nil {
		return report, err
	}
	by := domain.Author{Operator: operator}
	if apply {
		events, err := config.NotifyEmailEvents()
		if err != nil {
			return report, err
		}
		dbx := db.NewContext(conn)
		notifications := notificationsapp.New(notificationsrepo.NewPostgres(dbx)).WithMail(consoleMail(dbx, events))
		app := aiapp.New(airepo.NewPostgres(dbx), aiapp.Options{Zone: zone, Notifier: notifications.Commands.Notify, Logger: slog.Default()})
		report.Result, err = act(ctx, app, by)
		report.Applied = err == nil
		return report, err
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return report, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	app := aiapp.New(airepo.NewPostgres(db.NewContext(tx)), aiapp.Options{Zone: zone})
	report.Result, err = act(ctx, app, by)
	return report, err
}

// consoleMail is Notify's email channel for the console, as wiring.NotificationMail is the API's, over an outbox with no sealer: the only email the console's notices call for, a credit decision, carries no secret, so it needs no SECRETS_KEY. events is NOTIFY_EMAIL_EVENTS, which turns the email off as it does on the API.
func consoleMail(dbx db.Context, events []string) notificationsemail.Mailer {
	return notificationsemail.Mailer{Outbox: notificationsrepo.NewMail(dbx, email.NewOutbox(nil)), Sends: config.Email{Events: events}.Sends}
}

type aiFlags struct {
	apply    *bool
	operator *string
	user     *string
	request  *string
}

type aiSpec struct {
	flags func(fs *flag.FlagSet, f *aiFlags)
	build func(fs *flag.FlagSet, f aiFlags) (aiAction, error)
	read  bool
}

var aiSubcommands = map[string]aiSpec{
	"status": {read: true, build: func(*flag.FlagSet, aiFlags) (aiAction, error) {
		return func(ctx context.Context, app *aiapp.Application, _ domain.Author) (any, error) {
			view, err := app.Queries.AdminCredits.Handle(ctx, aiquery.AdminCredits{})
			return statusOf(view), err
		}, nil
	}},
	"set-allowance": {
		flags: func(fs *flag.FlagSet, f *aiFlags) {
			f.user = fs.String("user", "", "teacher UUID")
			fs.Int64("credits", 0, "the teacher's own monthly allowance, in credits")
			fs.Bool("default", false, "return the teacher to the centre's default allowance")
		},
		build: func(fs *flag.FlagSet, f aiFlags) (aiAction, error) {
			credits, byDefault := fs.Lookup("credits"), fs.Lookup("default")
			custom := credits.Value.String() != "0"
			if custom == (byDefault.Value.String() == "true") {
				return nil, errors.New("name exactly one of -credits N and -default")
			}
			n, _ := credits.Value.(flag.Getter).Get().(int64)
			return allowanceAction(*f.user, &custom, n, nil), nil
		},
	},
	"pause":  {flags: userFlag, build: pauseBuild(true)},
	"resume": {flags: userFlag, build: pauseBuild(false)},
	"add-credits": {
		flags: func(fs *flag.FlagSet, _ *aiFlags) { fs.Int64("credits", 0, "credits to add to this month's pool") },
		build: func(fs *flag.FlagSet, _ aiFlags) (aiAction, error) {
			n, _ := fs.Lookup("credits").Value.(flag.Getter).Get().(int64)
			return func(ctx context.Context, app *aiapp.Application, by domain.Author) (any, error) {
				pool, err := app.Commands.AddCredits.Handle(ctx, aicommand.AddCredits{By: by, Credits: n})
				return poolOf(pool), err
			}, nil
		},
	},
	"approve": {flags: requestFlag, build: decideBuild(true)},
	"decline": {flags: requestFlag, build: decideBuild(false)},
	"set-rules": {
		flags: func(fs *flag.FlagSet, _ *aiFlags) {
			fs.Int64("default-allowance", 0, "the default monthly allowance, in credits")
			fs.Int("warn-percent", 0, "the share of the allowance at which a teacher is warned")
			fs.String("on-limit", "", "pause or request: what happens to a teacher who runs out")
		},
		build: rulesBuild,
	},
}

func userFlag(fs *flag.FlagSet, f *aiFlags)    { f.user = fs.String("user", "", "teacher UUID") }
func requestFlag(fs *flag.FlagSet, f *aiFlags) { f.request = fs.String("request", "", "request UUID") }

func pauseBuild(paused bool) func(*flag.FlagSet, aiFlags) (aiAction, error) {
	return func(_ *flag.FlagSet, f aiFlags) (aiAction, error) {
		return allowanceAction(*f.user, nil, 0, &paused), nil
	}
}

func allowanceAction(user string, custom *bool, credits int64, paused *bool) aiAction {
	return func(ctx context.Context, app *aiapp.Application, by domain.Author) (any, error) {
		month, err := app.Commands.SetAllowance.Handle(ctx, aicommand.SetAllowance{
			By: by, UserID: user, Custom: custom, Credits: credits, Paused: paused,
		})
		return teacherOf(month), err
	}
}

func decideBuild(approve bool) func(*flag.FlagSet, aiFlags) (aiAction, error) {
	return func(_ *flag.FlagSet, f aiFlags) (aiAction, error) {
		return func(ctx context.Context, app *aiapp.Application, by domain.Author) (any, error) {
			decided, err := app.Commands.DecideRequest.Handle(ctx, aicommand.DecideRequest{By: by, ID: *f.request, Approve: approve})
			return decidedOf(decided), err
		}, nil
	}
}

func rulesBuild(fs *flag.FlagSet, _ aiFlags) (aiAction, error) {
	in := aicommand.SetRules{}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "default-allowance":
			in.DefaultAllowance = new(f.Value.(flag.Getter).Get().(int64))
		case "warn-percent":
			in.WarnPercent = new(f.Value.(flag.Getter).Get().(int))
		case "on-limit":
			in.OnTeacherLimit = new(domain.Limit(f.Value.String()))
		}
	})
	return func(ctx context.Context, app *aiapp.Application, by domain.Author) (any, error) {
		in.By = by
		change, err := app.Commands.SetRules.Handle(ctx, in)
		return rulesChangeOf(change), err
	}, nil
}

func parseAICredits(args []string) (Command, error) {
	if len(args) == 0 {
		return Command{}, fmt.Errorf("%s: name a subcommand: %s\n%s", aiCredits, aiSubcommandNames, Usage)
	}
	sub, ok := aiSubcommands[args[0]]
	if !ok {
		return Command{}, fmt.Errorf("%s: unknown subcommand %q: %s\n%s", aiCredits, args[0], aiSubcommandNames, Usage)
	}
	name := aiCredits + " " + args[0]
	fs := newFlagSet(name)
	timeout := fs.Duration("timeout", time.Minute, "how long the command may run")
	flags := aiFlags{}
	if !sub.read {
		flags.apply = fs.Bool("apply", false, "write; omitted means dry run")
		flags.operator = fs.String("operator", "", "who is running this, recorded in the audit row")
	}
	if sub.flags != nil {
		sub.flags(fs, &flags)
	}
	if err := fs.Parse(args[1:]); err != nil {
		return Command{}, fmt.Errorf("%s: %w\n%s", name, err, Usage)
	}
	if fs.NArg() > 0 {
		return Command{}, fmt.Errorf("%s: unexpected argument %q\n%s", name, fs.Arg(0), Usage)
	}
	return command(name, *timeout, func() (runner, error) {
		operator, err := operatorOf(sub, flags)
		if err != nil {
			return nil, err
		}
		act, err := sub.build(fs, flags)
		if err != nil {
			return nil, err
		}
		apply := flags.apply != nil && *flags.apply
		return func(ctx context.Context, conn db.Conn) (any, error) {
			return RunAI(ctx, conn, name, operator, apply, act)
		}, nil
	})
}

const aiSubcommandNames = "status, set-allowance, add-credits, approve, decline, pause, resume, set-rules"

func operatorOf(sub aiSpec, f aiFlags) (string, error) {
	if sub.read {
		return "", nil
	}
	operator := strings.TrimSpace(*f.operator)
	if operator == "" || len(operator) > maxOperatorLength {
		return "", errors.New("-operator is required: name who is running this, up to 100 characters")
	}
	return operator, nil
}

func sweepSpec(fs *flag.FlagSet) func() (runner, error) {
	apply := fs.Bool("apply", false, "release; omitted means dry run")
	return func() (runner, error) {
		return func(ctx context.Context, conn db.Conn) (any, error) {
			return SweepAIReservations(ctx, conn, *apply)
		}, nil
	}
}

// AISweepReport is what ai-reservations-sweep released, or with Applied false would have released.
type AISweepReport struct {
	Reservations int64 `json:"reservations"`
	Credits      int64 `json:"credits"`
	Applied      bool  `json:"applied"`
}

// SweepAIReservations returns the reservations held longer than domain.StaleAfter, which the API's hourly job does too. A dry run counts them inside a transaction it rolls back.
func SweepAIReservations(ctx context.Context, conn db.Conn, apply bool) (AISweepReport, error) {
	run := conn
	if !apply {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return AISweepReport{}, err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		run = tx
	}
	app := aiapp.New(airepo.NewPostgres(db.NewContext(run)), aiapp.Options{})
	released, err := app.Commands.ReleaseStale.Handle(ctx, aicommand.ReleaseStale{})
	if err != nil {
		return AISweepReport{}, err
	}
	return AISweepReport{Reservations: released.Reservations, Credits: released.Credits, Applied: apply}, nil
}
