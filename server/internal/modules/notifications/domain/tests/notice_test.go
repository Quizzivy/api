package domain_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"quizzivy/internal/modules/notifications/domain"
)

const (
	someUser       = "01935000-0000-7000-8000-0000000000a1"
	someAssignment = "01935000-0000-7000-8000-00000000a551"
	someAttempt    = "01935000-0000-7000-8000-00000000a77e"
)

func aNotice(change func(*domain.Notice)) domain.Notice {
	n := domain.Notice{
		UserID:    someUser,
		Kind:      domain.AttemptSubmitted,
		Params:    domain.Submitted{Title: "Đề giữa kỳ", Count: 1, ToGrade: 1},
		Target:    &domain.Target{Route: domain.RouteAssignment, AssignmentID: someAssignment},
		DedupeKey: "submitted:" + someAssignment + ":1958400",
		Merge:     domain.Add,
	}
	if change != nil {
		change(&n)
	}
	return n
}

func TestANoticeTheStoreCanWriteIsValid(t *testing.T) {
	for label, change := range map[string]func(*domain.Notice){
		"as built":        nil,
		"replacing":       func(n *domain.Notice) { n.Merge = domain.Replace },
		"leading nowhere": func(n *domain.Notice) { n.Target = nil },
		"the longest key": func(n *domain.Notice) { n.DedupeKey = strings.Repeat("ế", domain.MaxDedupeKey) },
		"a kind with no switch": func(n *domain.Notice) {
			n.Kind, n.Params = domain.ClassJoined, domain.Joined{StudentName: "Bảo", ClassName: "Lớp A"}
		},
		"a student's kind": func(n *domain.Notice) {
			n.Kind, n.Params = domain.ResultReady, domain.Ready{Title: "Đề giữa kỳ"}
		},
		"an attempt with both ids": func(n *domain.Notice) {
			n.Target = &domain.Target{Route: domain.RouteAttempt, AssignmentID: someAssignment, AttemptID: someAttempt}
		},
		"an attempt by its id only": func(n *domain.Notice) { n.Target = &domain.Target{Route: domain.RouteAttempt, AttemptID: someAttempt} },
		"a result":                  func(n *domain.Notice) { n.Target = &domain.Target{Route: domain.RouteResult, AttemptID: someAttempt} },
		"a student's assignment": func(n *domain.Notice) {
			n.Target = &domain.Target{Route: domain.RouteStudentAssignment, AssignmentID: someAssignment}
		},
		"the grading queue": func(n *domain.Notice) { n.Target = &domain.Target{Route: domain.RouteGrading} },
		"grading one assignment": func(n *domain.Notice) {
			n.Target = &domain.Target{Route: domain.RouteGrading, AssignmentID: someAssignment}
		},
		"the classes page": func(n *domain.Notice) { n.Target = &domain.Target{Route: domain.RouteClasses} },
	} {
		if err := aNotice(change).Validate(); err != nil {
			t.Errorf("%s: refused with %v", label, err)
		}
	}
}

func TestANoticeIsRefusedForItsFirstFault(t *testing.T) {
	for label, c := range map[string]struct {
		change func(*domain.Notice)
		want   error
	}{
		"no recipient":            {func(n *domain.Notice) { n.UserID = "" }, domain.ErrNoRecipient},
		"an unknown kind":         {func(n *domain.Notice) { n.Kind = "content.unknown" }, domain.ErrUnknownKind},
		"no kind":                 {func(n *domain.Notice) { n.Kind = "" }, domain.ErrUnknownKind},
		"no params":               {func(n *domain.Notice) { n.Params = nil }, domain.ErrParamsMismatch},
		"another kind's params":   {func(n *domain.Notice) { n.Params = domain.Ready{Title: "Đề giữa kỳ"} }, domain.ErrParamsMismatch},
		"a kind for other params": {func(n *domain.Notice) { n.Kind = domain.AttemptFlagged }, domain.ErrParamsMismatch},
		"params out of bounds":    {func(n *domain.Notice) { n.Params = domain.Submitted{Title: "Đề", Count: -1} }, domain.ErrInvalidParams},
		"an unknown route":        {func(n *domain.Notice) { n.Target = &domain.Target{Route: "messages"} }, domain.ErrInvalidTarget},
		"no route":                {func(n *domain.Notice) { n.Target = &domain.Target{AssignmentID: someAssignment} }, domain.ErrInvalidTarget},
		"an assignment without its id": {func(n *domain.Notice) {
			n.Target = &domain.Target{Route: domain.RouteAssignment, AttemptID: someAttempt}
		}, domain.ErrInvalidTarget},
		"a student's assignment without its id": {func(n *domain.Notice) { n.Target = &domain.Target{Route: domain.RouteStudentAssignment} }, domain.ErrInvalidTarget},
		"an attempt without its id": {func(n *domain.Notice) {
			n.Target = &domain.Target{Route: domain.RouteAttempt, AssignmentID: someAssignment}
		}, domain.ErrInvalidTarget},
		"a result without its id":             {func(n *domain.Notice) { n.Target = &domain.Target{Route: domain.RouteResult} }, domain.ErrInvalidTarget},
		"an assignment id that is not a uuid": {func(n *domain.Notice) { n.Target.AssignmentID = "a1" }, domain.ErrInvalidTarget},
		"an attempt id that is not a uuid":    {func(n *domain.Notice) { n.Target.AttemptID = "../teacher/classes" }, domain.ErrInvalidTarget},
		"an id not in its canonical form":     {func(n *domain.Notice) { n.Target.AssignmentID = strings.ToUpper(someAssignment) }, domain.ErrInvalidTarget},
		"no dedupe key":                       {func(n *domain.Notice) { n.DedupeKey = "" }, domain.ErrInvalidDedupeKey},
		"a dedupe key too long":               {func(n *domain.Notice) { n.DedupeKey = strings.Repeat("k", domain.MaxDedupeKey+1) }, domain.ErrInvalidDedupeKey},
		"no merge mode":                       {func(n *domain.Notice) { n.Merge = 0 }, domain.ErrUnknownMerge},
		"a merge mode that does not exist":    {func(n *domain.Notice) { n.Merge = domain.Add + 1 }, domain.ErrUnknownMerge},
	} {
		if err := aNotice(c.change).Validate(); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", label, err, c.want)
		}
	}
}

func TestEachKindIsGovernedByTheSwitchTheSettingsDraw(t *testing.T) {
	for kind, want := range map[domain.Kind]domain.Event{
		domain.AttemptSubmitted:   domain.EventAttemptSubmitted,
		domain.AttemptFlagged:     domain.EventAttemptFlagged,
		domain.AssignmentClosing:  domain.EventAssignmentClosing,
		domain.AssignmentOpened:   domain.EventAssignmentDueSoon,
		domain.AssignmentDueSoon:  domain.EventAssignmentDueSoon,
		domain.AssignmentExtended: domain.EventAssignmentDueSoon,
		domain.ResultReady:        domain.EventResultReady,
		domain.MessageReceived:    domain.EventMessageReceived,
		domain.MessageFromTeacher: domain.EventMessageReceived,
	} {
		if got, governed := kind.Event(); !governed || got != want {
			t.Errorf("%s is governed by %q (%v), want %s", kind, got, governed, want)
		}
	}
	for _, kind := range []domain.Kind{domain.ClassJoined, domain.JoinCodesRotated, domain.ContentShared, domain.TestPublishedByCollaborator, "content.unknown"} {
		if got, governed := kind.Event(); governed {
			t.Errorf("%s has the switch %s, want none", kind, got)
		}
	}
}

func TestTheContractsRoutesAreTheDomains(t *testing.T) {
	routes := []string{
		string(domain.RouteAssignment), string(domain.RouteAttempt), string(domain.RouteGrading),
		string(domain.RouteClasses), string(domain.RouteResult), string(domain.RouteStudentAssignment),
		string(domain.RouteSharedWithMe), string(domain.RouteTest), string(domain.RouteMessages),
		string(domain.RouteStudentMessages),
	}
	slices.Sort(routes)
	target := schema(t, "NotificationTarget")
	if declared := enum(t, target.Properties["route"].Value); !slices.Equal(declared, routes) {
		t.Errorf("the contract's routes are %v, the domain's %v", declared, routes)
	}
	for _, route := range routes {
		full := domain.Target{Route: domain.Route(route), AssignmentID: someAssignment, AttemptID: someAttempt, TestID: someAssignment,
			StudentID: someAttempt, TeacherID: someAssignment}
		if err := full.Validate(); err != nil {
			t.Errorf("%s with every id but the class's: %v", route, err)
		}
	}
}

func TestAMessageTargetNamesItsThread(t *testing.T) {
	for label, c := range map[string]struct {
		target domain.Target
		valid  bool
	}{
		"a teacher's thread with a student":  {domain.Target{Route: domain.RouteMessages, StudentID: someAttempt}, true},
		"a teacher's thread with no student": {domain.Target{Route: domain.RouteMessages}, false},
		"a student's thread with a teacher":  {domain.Target{Route: domain.RouteStudentMessages, TeacherID: someAssignment}, true},
		"a student's class channel":          {domain.Target{Route: domain.RouteStudentMessages, ClassID: someAssignment}, true},
		"a student's thread with neither":    {domain.Target{Route: domain.RouteStudentMessages}, false},
		"a student's thread with both":       {domain.Target{Route: domain.RouteStudentMessages, TeacherID: someAssignment, ClassID: someAttempt}, false},
		"a class id that is not a uuid":      {domain.Target{Route: domain.RouteStudentMessages, ClassID: "lop-1"}, false},
		"a student id that is not a uuid":    {domain.Target{Route: domain.RouteMessages, StudentID: "hv-1"}, false},
	} {
		if err := c.target.Validate(); (err == nil) != c.valid {
			t.Errorf("%s: err = %v, want valid %v", label, err, c.valid)
		}
	}
}
