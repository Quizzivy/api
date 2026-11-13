package access_test

import (
	"testing"

	"quizzivy/internal/shared/access"
)

// TestEachHostAdmitsTheAccountsThatBelongAtIt fails when a host admits an
// account of the other, when an account with both sides is turned away from
// either, and when an account is admitted nowhere.
func TestEachHostAdmitsTheAccountsThatBelongAtIt(t *testing.T) {
	for name, tc := range map[string]struct {
		keys                 access.Set
		student, staff, home bool
	}{
		"a student":                   {access.NewSet(access.LearningTakeTests), true, false, false},
		"a teacher":                   {access.NewSet(access.TeachingGrading), false, true, true},
		"an admin without the app":    {access.NewSet(access.SystemAuditRead), false, true, true},
		"staff who also take tests":   {access.NewSet(access.TeachingGrading, access.LearningTakeTests), true, true, true},
		"an admin who also takes one": {access.NewSet(access.ScopeAll, access.LearningTakeTests), true, true, true},
		"a role with no key":          {access.NewSet(), true, false, false},
		"a role that only uses AI":    {access.NewSet(access.AIUse), true, false, false},
	} {
		if got := access.AudienceStudent.Admits(tc.keys); got != tc.student {
			t.Errorf("%s: the student host admits = %t, want %t", name, got, tc.student)
		}
		if got := access.AudienceStaff.Admits(tc.keys); got != tc.staff {
			t.Errorf("%s: the staff host admits = %t, want %t", name, got, tc.staff)
		}
		if !access.AudienceAny.Admits(tc.keys) {
			t.Errorf("%s: an origin that is neither host turns the account away", name)
		}
		home := access.HomeAudience(tc.keys)
		if (home == access.AudienceStaff) != tc.home {
			t.Errorf("%s: a link sends the account to %q", name, home)
		}
		if !home.Admits(tc.keys) {
			t.Errorf("%s: the host a link names, %q, does not admit the account", name, home)
		}
	}
}

// TestAnOriginIsOneOfTheTwoHostsOnlyWhenItEqualsOne fails when an origin is
// matched by prefix or by host alone, and when a deployment whose two hosts
// are one origin splits its sessions.
func TestAnOriginIsOneOfTheTwoHostsOnlyWhenItEqualsOne(t *testing.T) {
	hosts := access.Hosts{Student: "https://my.quizzivy.com", Staff: "https://portal.quizzivy.com"}
	for origin, want := range map[string]access.Audience{
		"https://my.quizzivy.com":               access.AudienceStudent,
		"https://portal.quizzivy.com":           access.AudienceStaff,
		"":                                      access.AudienceAny,
		"null":                                  access.AudienceAny,
		"http://my.quizzivy.com":                access.AudienceAny,
		"https://my.quizzivy.com:8443":          access.AudienceAny,
		"https://my.quizzivy.com.attacker.test": access.AudienceAny,
		"https://app.quizzivy.com":              access.AudienceAny,
		"https://quizzivy.com":                  access.AudienceAny,
	} {
		if got := hosts.AudienceOf(origin); got != want {
			t.Errorf("the audience of %q is %q, want %q", origin, got, want)
		}
	}
	one := access.Hosts{Student: "http://localhost:5173", Staff: "http://localhost:5173"}
	if got := one.AudienceOf("http://localhost:5173"); got != access.AudienceAny {
		t.Errorf("one host for both: the audience is %q, want any", got)
	}
	if got := (access.Hosts{}).AudienceOf("https://my.quizzivy.com"); got != access.AudienceAny {
		t.Errorf("no hosts configured: the audience is %q, want any", got)
	}
	if hosts.URL(access.AudienceStaff) != hosts.Staff || hosts.URL(access.AudienceStudent) != hosts.Student || hosts.URL(access.AudienceAny) != hosts.Student {
		t.Errorf("the hosts' origins by audience: %q %q %q", hosts.URL(access.AudienceStaff), hosts.URL(access.AudienceStudent), hosts.URL(access.AudienceAny))
	}
	if access.AudienceStudent.Other() != access.AudienceStaff || access.AudienceStaff.Other() != access.AudienceStudent {
		t.Error("the other host of one is not the other")
	}
}
