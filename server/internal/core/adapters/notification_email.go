package adapters

import (
	"context"
	"time"

	notificationsdomain "quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/platform/email"
)

// NotificationFacts are the reads a notification email's Preparer makes when
// R5's dispatcher is about to send it: the notifications module's email store
// (repositories.Mail). Recipient answers the recipient's name and whether the
// email column of the switch sw (nil for none), or its default, is on for
// them and they are enabled. Each other read takes a row's params, which hold
// ids, and answers them filled as the database stands at now, and false when
// the email is no longer true: the paper was voided or unflagged, the
// assignment closed or was handed in, the result is no longer shown, the
// grant was revoked, the class changed hands again, no paper came in, the
// thread was read, or the recipient no longer reaches what it names.
type NotificationFacts interface {
	Recipient(ctx context.Context, userID string, sw *notificationsdomain.Event) (string, bool, error)
	Flagged(ctx context.Context, userID string, p email.AttemptFlaggedParams, now time.Time) (email.AttemptFlaggedParams, bool, error)
	Extended(ctx context.Context, userID string, p email.AssignmentExtendedParams, now time.Time) (email.AssignmentExtendedParams, bool, error)
	DueSoon(ctx context.Context, userID string, p email.AssignmentDueSoonParams, now time.Time) (email.AssignmentDueSoonParams, bool, error)
	Closing(ctx context.Context, userID string, p email.AssignmentClosingParams, now time.Time) (email.AssignmentClosingParams, bool, error)
	Ready(ctx context.Context, userID string, p email.ResultReadyParams, now time.Time) (email.ResultReadyParams, bool, error)
	Shared(ctx context.Context, userID string, p email.ContentSharedParams, now time.Time) (email.ContentSharedParams, bool, error)
	TeacherChanged(ctx context.Context, userID string, p email.ClassTeacherChangedParams, now time.Time) (email.ClassTeacherChangedParams, bool, error)
	Digest(ctx context.Context, userID string, p email.SubmissionsDigestParams, now time.Time) (email.SubmissionsDigestParams, bool, error)
	Message(ctx context.Context, userID string, p email.MessageReceivedParams, now time.Time) (email.MessageReceivedParams, bool, error)
}

type notificationRead[P email.Params] func(ctx context.Context, userID string, p P, now time.Time) (P, bool, error)

// NotificationPreparers are the Preparer of every notification email (T-R7.13)
// for R5's dispatcher. Each drops the row when NOTIFY_EMAIL_EVENTS (sends)
// turned its email off, when the recipient turned its switch off or was
// disabled since it was written, or when facts finds it no longer true, and
// otherwise fills it with the recipient's name and what it says. The weekly
// summary carries its figures, so its Preparer checks the switches alone.
func NotificationPreparers(facts NotificationFacts, sends func(event string) bool) email.Preparers {
	sw := func(e notificationsdomain.Event) *notificationsdomain.Event { return &e }
	return email.Preparers{
		email.AttemptFlagged: notificationPreparer(facts, sends, sw(notificationsdomain.EventAttemptFlagged), facts.Flagged,
			func(p email.AttemptFlaggedParams, name string) email.AttemptFlaggedParams {
				p.FullName = name
				return p
			}),
		email.AssignmentExtended: notificationPreparer(facts, sends, sw(notificationsdomain.EventAssignmentDueSoon), facts.Extended,
			func(p email.AssignmentExtendedParams, name string) email.AssignmentExtendedParams {
				p.FullName = name
				return p
			}),
		email.AssignmentDueSoon: notificationPreparer(facts, sends, sw(notificationsdomain.EventAssignmentDueSoon), facts.DueSoon,
			func(p email.AssignmentDueSoonParams, name string) email.AssignmentDueSoonParams {
				p.FullName = name
				return p
			}),
		email.AssignmentClosing: notificationPreparer(facts, sends, sw(notificationsdomain.EventAssignmentClosing), facts.Closing,
			func(p email.AssignmentClosingParams, name string) email.AssignmentClosingParams {
				p.FullName = name
				return p
			}),
		email.ResultReady: notificationPreparer(facts, sends, sw(notificationsdomain.EventResultReady), facts.Ready,
			func(p email.ResultReadyParams, name string) email.ResultReadyParams { p.FullName = name; return p }),
		email.ContentShared: notificationPreparer(facts, sends, nil, facts.Shared,
			func(p email.ContentSharedParams, name string) email.ContentSharedParams { p.FullName = name; return p }),
		email.ClassTeacherChanged: notificationPreparer(facts, sends, nil, facts.TeacherChanged,
			func(p email.ClassTeacherChangedParams, name string) email.ClassTeacherChangedParams {
				p.FullName = name
				return p
			}),
		email.SubmissionsDigest: notificationPreparer(facts, sends, sw(notificationsdomain.EventAttemptSubmitted), facts.Digest,
			func(p email.SubmissionsDigestParams, name string) email.SubmissionsDigestParams {
				p.FullName = name
				return p
			}),
		email.MessageReceived: notificationPreparer(facts, sends, sw(notificationsdomain.EventMessageReceived), facts.Message,
			func(p email.MessageReceivedParams, name string) email.MessageReceivedParams {
				p.FullName = name
				return p
			}),
		email.WeeklySummary: notificationPreparer(facts, sends, sw(notificationsdomain.EventSummaryWeekly), nil,
			func(p email.WeeklySummaryParams, name string) email.WeeklySummaryParams { p.FullName = name; return p }),
	}
}

func notificationPreparer[P email.Params](facts NotificationFacts, sends func(string) bool, sw *notificationsdomain.Event, read notificationRead[P], greet func(P, string) P) email.Preparer {
	return email.PreparerFunc(func(ctx context.Context, d email.Draft) (email.Params, bool, error) {
		p, ok := d.Params.(P)
		if !ok || d.ToUserID == "" || (sends != nil && !sends(email.Event(p.Template()))) {
			return nil, false, nil
		}
		name, on, err := facts.Recipient(ctx, d.ToUserID, sw)
		if err != nil || !on {
			return nil, false, err
		}
		if read != nil {
			if p, ok, err = read(ctx, d.ToUserID, p, d.Now); err != nil || !ok {
				return nil, false, err
			}
		}
		return greet(p, name), true, nil
	})
}
