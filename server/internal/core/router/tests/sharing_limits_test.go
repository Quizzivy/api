package router_test

import (
	"net/http"
	"testing"
)

func TestTheShareDialogsLimitsKeepOneBudgetPerCaller(t *testing.T) {
	issuer := testIssuer(t)
	share := `{"kind":"test","resourceId":"01935000-0000-7000-8000-0000000000a1","granteeIds":["01935000-0000-7000-8000-0000000000a2"],"access":"use","notify":false}`
	for name, c := range map[string]struct {
		method, path, body string
		budget             int
	}{
		"the directory, 60 a minute": {http.MethodGet, "/teacher/directory/teachers?q=lan", "", 60},
		"sharing, 30 an hour":        {http.MethodPost, "/teacher/shares", share, 30},
	} {
		h := roleRouter(t, issuer, rolePrincipals())
		for i := range c.budget {
			if rec := sendFrom(t, h, issuer, c.method, c.path, teacherUser, centreAddress, c.body); rec.Code != http.StatusNotImplemented {
				t.Fatalf("%s: call %d: %d, want it to reach the handler within the budget", name, i+1, rec.Code)
			}
		}
		if rec := sendFrom(t, h, issuer, c.method, c.path, teacherUser, homeAddress, c.body); rec.Code != http.StatusTooManyRequests {
			t.Errorf("%s: the first teacher's next call, from another address: %d, want 429 from their one budget", name, rec.Code)
		}
		if rec := sendFrom(t, h, issuer, c.method, c.path, adminUser, centreAddress, c.body); rec.Code != http.StatusNotImplemented {
			t.Errorf("%s: a second user's first call behind the same address: %d, want it to reach the handler", name, rec.Code)
		}
	}
}
