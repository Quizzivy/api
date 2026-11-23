package router_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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

// TestAColleagueThreadsPostsHaveABudgetOfTheirOwn: a staff member's posts
// to colleagues spend 30 a minute of their own (F2), apart from the posts to
// students and classes, and the colleague list and thread are read at most
// 60 times a minute.
func TestAColleagueThreadsPostsHaveABudgetOfTheirOwn(t *testing.T) {
	issuer := testIssuer(t)
	h := roleRouter(t, issuer, rolePrincipals())
	post := `{"clientId":"01935000-0000-7000-8000-0000000000d7","body":"Chị gửi em đề nghe"}`
	colleague := "/teacher/messages/colleagues/01935000-0000-7000-8000-0000000000d8"
	direct := "/teacher/messages/students/01935000-0000-7000-8000-0000000000d2"
	for i := range 30 {
		if rec := sendFrom(t, h, issuer, http.MethodPost, colleague, teacherUser, centreAddress, post); rec.Code != http.StatusNotImplemented {
			t.Fatalf("post %d to a colleague: %d, want it to reach the handler within the budget of 30 a minute", i+1, rec.Code)
		}
	}
	if rec := sendFrom(t, h, issuer, http.MethodPost, colleague, teacherUser, homeAddress, post); rec.Code != http.StatusTooManyRequests {
		t.Errorf("the 31st post to a colleague, from another address: %d, want 429", rec.Code)
	}
	if rec := sendFrom(t, h, issuer, http.MethodPost, direct, teacherUser, centreAddress, post); rec.Code != http.StatusNotImplemented {
		t.Errorf("a post to a student once the colleague budget is spent: %d, want the handler: the budgets are apart", rec.Code)
	}
	for _, path := range []string{"/teacher/messages/colleagues", colleague} {
		for i := range 60 {
			if rec := sendFrom(t, h, issuer, http.MethodGet, path, adminUser, centreAddress, ""); rec.Code != http.StatusNotImplemented {
				t.Fatalf("read %d of %s: %d, want the handler within 60 a minute", i+1, path, rec.Code)
			}
		}
		if rec := sendFrom(t, h, issuer, http.MethodGet, path, adminUser, homeAddress, ""); rec.Code != http.StatusTooManyRequests {
			t.Errorf("the 61st read of %s in a minute: %d, want 429", path, rec.Code)
		}
	}
}

func TestAStudentsPostsHaveTheirOwnBudget(t *testing.T) {
	issuer := testIssuer(t)
	h := roleRouter(t, issuer, rolePrincipals())
	post := `{"clientId":"01935000-0000-7000-8000-0000000000d4","body":"Em chào cô"}`
	teacher := "/app/messages/teachers/01935000-0000-7000-8000-0000000000d5"
	for i := range 10 {
		if rec := sendFrom(t, h, issuer, http.MethodPost, teacher, studentUser, centreAddress, post); rec.Code != http.StatusNotImplemented {
			t.Fatalf("post %d: %d, want it to reach the handler within the budget of 10 a minute", i+1, rec.Code)
		}
	}
	if rec := sendFrom(t, h, issuer, http.MethodPost, teacher, studentUser, homeAddress, post); rec.Code != http.StatusTooManyRequests {
		t.Errorf("the student's 11th post in a minute, from another address: %d, want 429", rec.Code)
	}
	if rec := sendFrom(t, h, issuer, http.MethodGet, teacher, studentUser, centreAddress, ""); rec.Code != http.StatusNotImplemented {
		t.Errorf("the spent poster reading the thread: %d, want reads outside the posts' budget", rec.Code)
	}
	if rec := sendFrom(t, h, issuer, http.MethodPost, "/teacher/messages/students/01935000-0000-7000-8000-0000000000d2", teacherUser, centreAddress, post); rec.Code != http.StatusNotImplemented {
		t.Errorf("a teacher's post behind the same address: %d, want the staff budget, untouched by the student's", rec.Code)
	}
}

// A class's channel is read-only for a student: /app/messages has no post
// operation for it, and the channel's post operation is gated on
// teaching.classes.write.
func TestAStudentCannotPostInAClassChannel(t *testing.T) {
	issuer := testIssuer(t)
	h := roleRouter(t, issuer, rolePrincipals())
	post := `{"clientId":"01935000-0000-7000-8000-0000000000d6","body":"Em chào cả lớp"}`
	class := "01935000-0000-7000-8000-0000000000d3"
	rec := sendFrom(t, h, issuer, http.MethodPost, "/app/messages/classes/"+class, studentUser, centreAddress, post)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET" {
		t.Errorf("a student's post to their class's channel under /app/messages: %d, Allow %q, want 405 and GET alone", rec.Code, rec.Header().Get("Allow"))
	}
	if rec := sendFrom(t, h, issuer, http.MethodPost, "/teacher/messages/classes/"+class, studentUser, centreAddress, post); rec.Code != http.StatusForbidden {
		t.Errorf("a student's announcement to a class: %d, want 403", rec.Code)
	}
}

func TestAStaffMembersUploadsHaveABudgetOfTwentyAnHour(t *testing.T) {
	issuer := testIssuer(t)
	h := roleRouter(t, issuer, rolePrincipals())
	upload := func(userID, address string) int {
		t.Helper()
		var b bytes.Buffer
		form := multipart.NewWriter(&b)
		part, err := form.CreateFormFile("file", "bai-tap.pdf")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte("%PDF-1.4\n")); err != nil {
			t.Fatal(err)
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/teacher/messages/attachments", &b)
		req.RemoteAddr = address + ":40000"
		req.Header.Set("Content-Type", form.FormDataContentType())
		raw, err := issuer.Issue(userID, 0)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+raw)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := range 20 {
		if code := upload(teacherUser, centreAddress); code != http.StatusNotImplemented {
			t.Fatalf("upload %d: %d, want it to reach the handler within the budget of 20 an hour", i+1, code)
		}
	}
	if code := upload(teacherUser, homeAddress); code != http.StatusTooManyRequests {
		t.Errorf("the 21st upload in an hour, from another address: %d, want 429", code)
	}
	if code := upload(adminUser, centreAddress); code != http.StatusNotImplemented {
		t.Errorf("a second user's first upload behind the same address: %d, want it to reach the handler", code)
	}
}
