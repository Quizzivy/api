package domain

import "github.com/google/uuid"

// Route is the page a notification opens. The client resolves it, so it is
// one of the contract's NotificationTarget routes and never a URL.
type Route string

const (
	RouteAssignment        Route = "assignment"
	RouteAttempt           Route = "attempt"
	RouteGrading           Route = "grading"
	RouteClasses           Route = "classes"
	RouteResult            Route = "result"
	RouteStudentAssignment Route = "studentAssignment"
	RouteSharedWithMe      Route = "sharedWithMe"
	RouteTest              Route = "test"
	RouteMessages          Route = "messages"
	RouteStudentMessages   Route = "studentMessages"
	RouteDashboard         Route = "dashboard"
)

type requirement struct {
	assignment bool
	attempt    bool
	test       bool
	// peer is a staff member's thread: a student's direct thread or a
	// colleague's, exactly one of the two.
	peer bool
	// thread is a student's thread: a teacher's direct thread or a class's
	// channel, exactly one of the two.
	thread bool
}

var routes = map[Route]requirement{
	RouteAssignment:        {assignment: true},
	RouteAttempt:           {attempt: true},
	RouteGrading:           {},
	RouteClasses:           {},
	RouteResult:            {attempt: true},
	RouteStudentAssignment: {assignment: true},
	RouteSharedWithMe:      {},
	RouteTest:              {test: true},
	RouteMessages:          {peer: true},
	RouteStudentMessages:   {thread: true},
	RouteDashboard:         {},
}

// Target is where a click on a notification goes, stored as the contract's
// NotificationTarget. RouteAssignment and RouteStudentAssignment need
// AssignmentID, RouteAttempt and RouteResult need AttemptID, RouteTest needs
// TestID, RouteMessages needs one of StudentID and ColleagueID (a colleague
// thread, F2), RouteStudentMessages needs one of TeacherID and ClassID, and
// any id may accompany any other route.
type Target struct {
	Route        Route  `json:"route"`
	AssignmentID string `json:"assignmentId,omitempty"`
	AttemptID    string `json:"attemptId,omitempty"`
	TestID       string `json:"testId,omitempty"`
	StudentID    string `json:"studentId,omitempty"`
	ColleagueID  string `json:"colleagueId,omitempty"`
	TeacherID    string `json:"teacherId,omitempty"`
	ClassID      string `json:"classId,omitempty"`
}

// Validate answers ErrInvalidTarget for an unknown route, for an id that is
// not a uuid, and for a route without the id it needs.
func (t Target) Validate() error {
	needs, known := routes[t.Route]
	if !known {
		return ErrInvalidTarget
	}
	for _, id := range []string{t.AssignmentID, t.AttemptID, t.TestID, t.StudentID, t.ColleagueID, t.TeacherID, t.ClassID} {
		if !idOrNone(id) {
			return ErrInvalidTarget
		}
	}
	if needs.assignment && t.AssignmentID == "" || needs.attempt && t.AttemptID == "" || needs.test && t.TestID == "" ||
		needs.peer && (t.StudentID == "") == (t.ColleagueID == "") || needs.thread && (t.TeacherID == "") == (t.ClassID == "") {
		return ErrInvalidTarget
	}
	return nil
}

func idOrNone(id string) bool {
	if id == "" {
		return true
	}
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}
