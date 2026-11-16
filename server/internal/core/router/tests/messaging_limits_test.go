package router_test

import (
	"net/http"
	"testing"
)

func TestAStaffMembersPostsSpendOneBudgetPerCaller(t *testing.T) {
	issuer := testIssuer(t)
	h := roleRouter(t, issuer, rolePrincipals())
	post := `{"clientId":"01935000-0000-7000-8000-0000000000d1","body":"Chào em"}`
	direct := "/teacher/messages/students/01935000-0000-7000-8000-0000000000d2"
	announcement := "/teacher/messages/classes/01935000-0000-7000-8000-0000000000d3"
	for i := range 30 {
		path := direct
		if i%2 == 1 {
			path = announcement
		}
		if rec := sendFrom(t, h, issuer, http.MethodPost, path, teacherUser, centreAddress, post); rec.Code != http.StatusNotImplemented {
			t.Fatalf("post %d to %s: %d, want it to reach the handler within the budget of 30 a minute", i+1, path, rec.Code)
		}
	}
	for _, path := range []string{direct, announcement} {
		if rec := sendFrom(t, h, issuer, http.MethodPost, path, teacherUser, homeAddress, post); rec.Code != http.StatusTooManyRequests {
			t.Errorf("the 31st post, to %s from another address: %d, want 429: direct messages and announcements spend one budget", path, rec.Code)
		}
	}
	if rec := sendFrom(t, h, issuer, http.MethodPost, announcement, adminUser, centreAddress, post); rec.Code != http.StatusNotImplemented {
		t.Errorf("a second user's first post behind the same address: %d, want it to reach the handler", rec.Code)
	}
	if rec := sendFrom(t, h, issuer, http.MethodGet, "/teacher/messages/threads", teacherUser, centreAddress, ""); rec.Code != http.StatusNotImplemented {
		t.Errorf("the spent poster reading the list: %d, want reads outside the posts' budget", rec.Code)
	}
}
