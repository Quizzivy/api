//go:build integration

package application_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"quizzivy/internal/modules/identity/application"
	"quizzivy/internal/modules/identity/application/command"
	"quizzivy/internal/modules/identity/application/model"
	"quizzivy/internal/modules/identity/application/query"
	"quizzivy/internal/modules/identity/domain"
	"quizzivy/internal/shared/access"
)

var (
	hostStudentKeys = access.NewSet(access.LearningTakeTests)
	hostTeacherKeys = access.NewSet(access.TeachingGrading, access.ContentTestsWrite)
	hostBothKeys    = access.NewSet(access.LearningTakeTests, access.TeachingGrading)
)

// heldKeys answers the permissions each user holds, and can be changed while
// a session lives, as an Admin changes a role.
type heldKeys struct {
	keys map[string]access.Set
}

func (h *heldKeys) Forget(string) {}

func (h *heldKeys) Resolve(_ context.Context, userID string) (access.Principal, error) {
	return access.Principal{UserID: userID, Permissions: h.keys[userID]}, nil
}

func hostService(t *testing.T, pool *pgxpool.Pool) (*application.Application, *heldKeys) {
	t.Helper()
	svc := newService(t, pool)
	held := &heldKeys{keys: map[string]access.Set{}}
	svc.SetPrincipals(held)
	return svc, held
}

func signInAt(svc *application.Application, email string, audience access.Audience) (model.Session, error) {
	return svc.Commands.Login.Handle(context.Background(), command.Login{
		Email: email, Password: testPassword, Audience: audience, IP: "203.0.113.7", UserAgent: "go-test",
	})
}

func refreshAt(svc *application.Application, token string, audience access.Audience) (model.RefreshResult, error) {
	return svc.Commands.Refresh.Handle(context.Background(), command.Refresh{Token: token, Audience: audience, IP: "203.0.113.7", UserAgent: "go-test"})
}

func sessionsStored(t *testing.T, pool *pgxpool.Pool, userID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT audience FROM app.refresh_tokens WHERE user_id = $1::uuid AND revoked_at IS NULL ORDER BY audience`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var audience string
		if err := rows.Scan(&audience); err != nil {
			t.Fatal(err)
		}
		out = append(out, audience)
	}
	return out
}

func hostOf(t *testing.T, raw string) access.Audience {
	t.Helper()
	claims, err := mustIssuer(t).Verify(raw)
	if err != nil {
		t.Fatalf("the access token does not verify: %v", err)
	}
	return claims.Host()
}

// TestAnAccountSignsInOnlyAtTheHostItBelongsAt fails when IssueSession stops
// asking whether the host admits the account, and when a refused sign-in
// leaves a session or a sign-in stamp behind.
func TestAnAccountSignsInOnlyAtTheHostItBelongsAt(t *testing.T) {
	pool := newPool(t)
	svc, held := hostService(t, pool)
	for name, tc := range map[string]struct {
		keys     access.Set
		admitted []access.Audience
		refused  []access.Audience
	}{
		"a student":                 {hostStudentKeys, []access.Audience{access.AudienceStudent, access.AudienceAny}, []access.Audience{access.AudienceStaff}},
		"a teacher":                 {hostTeacherKeys, []access.Audience{access.AudienceStaff, access.AudienceAny}, []access.Audience{access.AudienceStudent}},
		"staff who also take tests": {hostBothKeys, []access.Audience{access.AudienceStudent, access.AudienceStaff, access.AudienceAny}, nil},
	} {
		id, email := makeUser(t, pool)
		held.keys[id] = tc.keys
		for _, audience := range tc.refused {
			if _, err := signInAt(svc, email, audience); !errors.Is(err, domain.ErrWrongHost) {
				t.Errorf("%s at the %s host: %v, want the wrong-host refusal", name, audience, err)
			}
			if _, err := svc.Commands.Login.Handle(context.Background(), command.Login{Email: email, Password: "sai-mật-khẩu-1", Audience: audience}); !errors.Is(err, domain.ErrInvalidCredentials) {
				t.Errorf("%s at the %s host with a wrong password: %v, want the answer every wrong password gets", name, audience, err)
			}
		}
		if stored := sessionsStored(t, pool, id); len(stored) != 0 {
			t.Errorf("%s: a refused sign-in left the sessions %v", name, stored)
		}
		var stamped bool
		if err := pool.QueryRow(context.Background(), `SELECT last_sign_in_at IS NOT NULL FROM app.users WHERE id = $1::uuid`, id).Scan(&stamped); err != nil || stamped {
			t.Errorf("%s: a refused sign-in was stamped as a sign-in (%v)", name, err)
		}
		var want []string
		for _, audience := range tc.admitted {
			session, err := signInAt(svc, email, audience)
			if err != nil {
				t.Fatalf("%s at the %s host: %v", name, audience, err)
			}
			if got := hostOf(t, session.AccessToken); got != audience {
				t.Errorf("%s at the %s host: the access token is of %q", name, audience, got)
			}
			want = append(want, string(audience))
		}
		slices.Sort(want)
		if stored := sessionsStored(t, pool, id); !slices.Equal(stored, want) {
			t.Errorf("%s: the stored sessions are of %v, want %v", name, stored, want)
		}
	}
}

// TestASessionRefreshesOnlyUnderItsOwnHostsCookie fails when Rotate stops
// comparing the family's audience with the request's: a token carried to
// another host's cookie must be refused and left exactly as it was, so the
// session it belongs to carries on.
func TestASessionRefreshesOnlyUnderItsOwnHostsCookie(t *testing.T) {
	pool := newPool(t)
	svc, held := hostService(t, pool)
	id, email := makeUser(t, pool)
	held.keys[id] = hostBothKeys
	student, err := signInAt(svc, email, access.AudienceStudent)
	if err != nil {
		t.Fatal(err)
	}
	for _, other := range []access.Audience{access.AudienceStaff, access.AudienceAny} {
		if _, err := refreshAt(svc, student.RefreshToken, other); !errors.Is(err, domain.ErrRefreshRejected) {
			t.Errorf("the student session's token as a session of %q: %v, want it rejected and not treated as reuse", other, err)
		}
	}
	if row := loadToken(t, pool, student.RefreshToken); row.revoked || row.replacedBy != nil {
		t.Fatalf("the refusals touched the token: %+v", row)
	}
	next, err := refreshAt(svc, student.RefreshToken, access.AudienceStudent)
	if err != nil {
		t.Fatalf("the session no longer refreshes at its own host: %v", err)
	}
	if got := hostOf(t, next.AccessToken); got != access.AudienceStudent {
		t.Errorf("the refreshed access token is of %q", got)
	}
	if stored := sessionsStored(t, pool, id); !slices.Equal(stored, []string{"student"}) {
		t.Errorf("the successor is of %v, want its family's audience", stored)
	}
}

// TestASessionEndsAtItsNextRefreshOnceTheAccountLeavesItsHost fails when
// Refresh stops asking whether the host still admits the account: a role
// change would then leave a staff session alive at the staff host for the
// thirty days its cookie lasts.
func TestASessionEndsAtItsNextRefreshOnceTheAccountLeavesItsHost(t *testing.T) {
	pool := newPool(t)
	svc, held := hostService(t, pool)
	id, email := makeUser(t, pool)
	held.keys[id] = hostBothKeys
	staff, err := signInAt(svc, email, access.AudienceStaff)
	if err != nil {
		t.Fatal(err)
	}
	student, err := signInAt(svc, email, access.AudienceStudent)
	if err != nil {
		t.Fatal(err)
	}

	held.keys[id] = hostStudentKeys
	if _, err := refreshAt(svc, staff.RefreshToken, access.AudienceStaff); !errors.Is(err, domain.ErrRefreshRejected) {
		t.Fatalf("a staff session of an account that is now a student refreshed: %v", err)
	}
	if live := liveTokensInFamily(t, pool, loadToken(t, pool, staff.RefreshToken).familyID); live != 0 {
		t.Errorf("the staff session keeps %d live tokens, want it ended", live)
	}
	if _, err := refreshAt(svc, student.RefreshToken, access.AudienceStudent); err != nil {
		t.Errorf("the account's student session ended with its staff session: %v", err)
	}
}

// TestTheSessionListAndItsSignOutsStayInsideOneHost fails when the session
// queries stop filtering by audience: the list at one host would show, and
// "sign out the others" would end, the session the same account holds at the
// other host.
func TestTheSessionListAndItsSignOutsStayInsideOneHost(t *testing.T) {
	pool := newPool(t)
	svc, held := hostService(t, pool)
	id, email := makeUser(t, pool)
	held.keys[id] = hostBothKeys
	open := func(audience access.Audience) (string, string) {
		session, err := signInAt(svc, email, audience)
		if err != nil {
			t.Fatal(err)
		}
		return session.RefreshToken, loadToken(t, pool, session.RefreshToken).familyID
	}
	phone, phoneFamily := open(access.AudienceStudent)
	_, laptopFamily := open(access.AudienceStudent)
	staff, staffFamily := open(access.AudienceStaff)

	list := func(audience access.Audience, cookie string) []string {
		sessions, err := svc.Queries.ListSessions.Handle(context.Background(), query.ListSessions{UserID: id, Audience: audience, RefreshToken: cookie})
		if err != nil {
			t.Fatal(err)
		}
		families := familiesOf(sessions)
		slices.Sort(families)
		return families
	}
	wantStudent := []string{phoneFamily, laptopFamily}
	slices.Sort(wantStudent)
	if got := list(access.AudienceStudent, phone); !slices.Equal(got, wantStudent) {
		t.Errorf("the student host lists %v, want its two sessions %v", got, wantStudent)
	}
	if got := list(access.AudienceStaff, staff); !slices.Equal(got, []string{staffFamily}) {
		t.Errorf("the staff host lists %v, want its one session", got)
	}

	_, err := svc.Commands.RevokeSession.Handle(context.Background(), command.RevokeSession{
		UserID: id, FamilyID: staffFamily, Audience: access.AudienceStudent, RefreshToken: phone,
	})
	if !errors.Is(err, domain.ErrSessionNotFound) {
		t.Errorf("ending the staff session from the student host: %v, want it not found", err)
	}
	revoked, err := svc.Commands.RevokeOtherSessions.Handle(context.Background(), command.RevokeOtherSessions{
		UserID: id, Audience: access.AudienceStudent, RefreshToken: phone,
	})
	if err != nil || revoked != 1 {
		t.Fatalf("signing out the others at the student host ended %d sessions (%v), want the laptop alone", revoked, err)
	}
	if liveTokensInFamily(t, pool, laptopFamily) != 0 || liveTokensInFamily(t, pool, phoneFamily) != 1 {
		t.Error("the student host's sign-out of the others did not end the laptop and keep the phone")
	}
	if _, err := refreshAt(svc, staff, access.AudienceStaff); err != nil {
		t.Errorf("the staff session was ended from the student host: %v", err)
	}
}

// TestAPasswordChangeEndsTheSessionsAtBothHosts fails when an account-wide
// revocation starts to filter by audience: a password changed at one host must
// end the session the other host holds.
func TestAPasswordChangeEndsTheSessionsAtBothHosts(t *testing.T) {
	pool := newPool(t)
	svc, held := hostService(t, pool)
	id, email := makeUser(t, pool)
	held.keys[id] = hostBothKeys
	student, err := signInAt(svc, email, access.AudienceStudent)
	if err != nil {
		t.Fatal(err)
	}
	staff, err := signInAt(svc, email, access.AudienceStaff)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Commands.ChangePassword.Handle(context.Background(), command.ChangePassword{
		UserID: id, CurrentPassword: testPassword, NewPassword: "mật-khẩu-mới-9", KeepRefreshToken: student.RefreshToken,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := refreshAt(svc, staff.RefreshToken, access.AudienceStaff); !errors.Is(err, domain.ErrRefreshRejected) {
		t.Errorf("the staff session survived a password change made at the student host: %v", err)
	}
	if _, err := refreshAt(svc, student.RefreshToken, access.AudienceStudent); err != nil {
		t.Errorf("the session that changed the password was ended: %v", err)
	}
}

// TestASetPasswordLinkAtTheWrongHostChangesNothing fails when the link's
// account is checked against the host only after the password has been set:
// the refusal must leave the password, the sessions and the link as they were.
func TestASetPasswordLinkAtTheWrongHostChangesNothing(t *testing.T) {
	w := newDBLinks(t)
	student := w.account(t, "hs", true)
	if _, err := w.issue(t, student, domain.LinkReset, "", true); err != nil {
		t.Fatal(err)
	}
	token := w.lastMail(t, student).token
	before := w.state(t, student)

	_, err := w.app.Commands.SetPasswordWithLink.Handle(context.Background(), command.SetPasswordWithLink{
		Token: token, NewPassword: linkNewPassword, Audience: access.AudienceStaff, UserAgent: "link-db-test", IP: "203.0.113.7",
	})
	if !errors.Is(err, domain.ErrWrongHost) {
		t.Fatalf("a student's link at the staff host: %v, want the wrong-host refusal", err)
	}
	if after := w.state(t, student); after != before {
		t.Errorf("the refusal changed the account: %+v, was %+v", after, before)
	}
	if !w.usable(token) {
		t.Fatal("the refusal used the link up")
	}
	session, err := w.app.Commands.SetPasswordWithLink.Handle(context.Background(), command.SetPasswordWithLink{
		Token: token, NewPassword: linkNewPassword, Audience: access.AudienceStudent, UserAgent: "link-db-test", IP: "203.0.113.7",
	})
	if err != nil {
		t.Fatalf("the link at the student's own host: %v", err)
	}
	if got := hostOf(t, session.AccessToken); got != access.AudienceStudent {
		t.Errorf("the session a link opens is of %q", got)
	}
}

// TestAJoinCodeAtTheStaffHostIsRefusedBeforeGoogleIsAsked fails when the
// refusal moves behind the code exchange or the enrolment: a code makes a
// student, so at the staff host nothing may be created.
func TestAJoinCodeAtTheStaffHostIsRefusedBeforeGoogleIsAsked(t *testing.T) {
	pool := newPool(t)
	svc, google := googleService(t, pool, verifiedIdentity("moi-"+t.Name()+"@example.com"))
	_, err := svc.Commands.GoogleSignIn.Handle(context.Background(), command.GoogleSignIn{
		Code: "c", CodeVerifier: "v", RedirectURI: "https://portal.quizzivy.com/auth/google/callback",
		JoinCode: "K7M3-P9QR", Audience: access.AudienceStaff, IP: "203.0.113.11", UserAgent: "go-test",
	})
	if !errors.Is(err, domain.ErrWrongHost) {
		t.Fatalf("a join code at the staff host: %v, want the wrong-host refusal", err)
	}
	if google.exchanged {
		t.Error("Google was asked to exchange the code of a sign-in that could never be admitted")
	}
}

// TestAGoogleSignInAtTheWrongHostOpensNoSession fails when the Google path
// skips the admission the password path makes.
func TestAGoogleSignInAtTheWrongHostOpensNoSession(t *testing.T) {
	pool := newPool(t)
	id, email := makeUser(t, pool, googleOnly)
	svc, _ := googleService(t, pool, verifiedIdentity(email))
	held := &heldKeys{keys: map[string]access.Set{id: hostStudentKeys}}
	svc.SetPrincipals(held)
	at := func(audience access.Audience) error {
		_, err := svc.Commands.GoogleSignIn.Handle(context.Background(), command.GoogleSignIn{
			Code: "c", CodeVerifier: "v", RedirectURI: "https://my.quizzivy.com/auth/google/callback",
			Audience: audience, IP: "203.0.113.11", UserAgent: "go-test",
		})
		return err
	}
	if err := at(access.AudienceStaff); !errors.Is(err, domain.ErrWrongHost) {
		t.Fatalf("a student's Google sign-in at the staff host: %v, want the wrong-host refusal", err)
	}
	if stored := sessionsStored(t, pool, id); len(stored) != 0 {
		t.Errorf("the refusal left the sessions %v", stored)
	}
	if err := at(access.AudienceStudent); err != nil {
		t.Fatalf("the same account at the student host: %v", err)
	}
	if stored := sessionsStored(t, pool, id); !slices.Equal(stored, []string{"student"}) {
		t.Errorf("the stored sessions are of %v", stored)
	}
}
