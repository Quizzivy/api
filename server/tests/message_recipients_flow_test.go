//go:build e2e

package e2e

import (
	"net/http"
	"net/url"
	"slices"
	"testing"
)

func recipientIDs(t *testing.T, c *client, query url.Values) ([]string, any) {
	t.Helper()
	got := c.must(http.StatusOK, http.MethodGet, "/teacher/messages/recipients?"+query.Encode(), nil)
	var out []string
	for _, row := range objects(got["recipients"]) {
		out = append(out, id(row))
	}
	return out, got["nextCursor"]
}

// TestTheNewMessagePickerListsStudents is listMessageRecipients through HTTP
// (F2, T-F2.3): any teacher lists every enabled student, their own, another
// teacher's and one in no class, each row carrying exactly the contract's
// fields and nothing the leak review keeps out; the filters, the paging and
// the 400s the contract names; another teacher's class answers 404 as a
// missing one does; colleagues answer 501 until T-F2.4; a student is refused.
func TestTheNewMessagePickerListsStudents(t *testing.T) {
	w := boot(t)
	teacher := w.signedIn(w.createStaff("teacher"))
	other := w.signedIn(w.createStaff("teacher"))
	tag := nonce(t)
	mine, theirs := teacher.class("Lớp chọn người nhận "+tag), other.class("Lớp khác "+tag)
	student := func(c *client, name string, classes ...string) string {
		if classes == nil {
			classes = []string{}
		}
		return id(c.must(http.StatusCreated, http.MethodPost, "/teacher/students", map[string]any{
			"email": "nguoi-nhan-" + nonce(t) + "@example.com", "fullName": name + " " + tag, "classIds": classes,
		})["user"].(map[string]any))
	}
	ours := student(teacher, "An Học viên", mine)
	elsewhere := student(other, "Bình Học viên", theirs)
	alone := student(other, "Chi Học viên")

	base := url.Values{"audience": {"students"}, "q": {tag}}
	with := func(k, v string) url.Values {
		q := url.Values{}
		for key, vs := range base {
			q[key] = vs
		}
		q.Set(k, v)
		return q
	}
	listed, next := recipientIDs(t, teacher, base)
	if want := []string{ours, elsewhere, alone}; !slices.Equal(listed, want) || next != nil {
		t.Errorf("the picker lists %v (next %v), want %v: every enabled student, by name", listed, next, want)
	}
	rows := objects(teacher.must(http.StatusOK, http.MethodGet, "/teacher/messages/recipients?"+base.Encode(), nil)["recipients"])
	for _, row := range rows {
		var keys []string
		for k := range row {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if want := []string{"avatarUrl", "canPost", "classNames", "id", "name", "otherClassNames", "roleBuiltinKey", "roleName", "threadExists"}; !slices.Equal(keys, want) {
			t.Errorf("a recipient row carries %v, want exactly %v", keys, want)
		}
		if row["roleName"] != nil || row["roleBuiltinKey"] != nil || row["canPost"] != true {
			t.Errorf("a student row: %v, want no role and canPost", row)
		}
	}
	if got := rows[1]; len(got["classNames"].([]any)) != 0 || !slices.Equal(stringsOf(got["otherClassNames"]), []string{"Lớp khác " + tag}) {
		t.Errorf("another teacher's student: %v, want the other class by name alone", got)
	}

	for name, c := range map[string]struct {
		query url.Values
		want  []string
	}{
		"my classes":               {with("mine", "true"), []string{ours}},
		"one of my classes":        {with("classId", mine), []string{ours}},
		"in no class":              {with("inNoClass", "true"), []string{alone}},
		"mine=false is no filter":  {with("mine", "false"), []string{ours, elsewhere, alone}},
		"inNoClass=false likewise": {with("inNoClass", "false"), []string{ours, elsewhere, alone}},
	} {
		if got, _ := recipientIDs(t, teacher, c.query); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}

	page, cursor := recipientIDs(t, teacher, with("limit", "2"))
	if !slices.Equal(page, []string{ours, elsewhere}) || cursor == nil {
		t.Fatalf("the first page of two: %v, next %v", page, cursor)
	}
	q := with("limit", "2")
	q.Set("cursor", cursor.(string))
	if page, cursor = recipientIDs(t, teacher, q); !slices.Equal(page, []string{alone}) || cursor != nil {
		t.Errorf("the last page: %v, next %v, want the third student and no cursor", page, cursor)
	}

	missingClass := with("classId", "0195a1b2-0000-7000-8000-000000000000")
	theirStatus, theirBody := teacher.call(http.MethodGet, "/teacher/messages/recipients?"+with("classId", theirs).Encode(), nil)
	missingStatus, missingBody := teacher.call(http.MethodGet, "/teacher/messages/recipients?"+missingClass.Encode(), nil)
	if theirStatus != http.StatusNotFound || missingStatus != http.StatusNotFound || errorCode(theirBody) != errorCode(missingBody) {
		t.Errorf("another teacher's class: %d %v, a missing one %d %v, want 404 alike", theirStatus, theirBody, missingStatus, missingBody)
	}

	bad := map[string]url.Values{
		"a class with mine":             {"audience": {"students"}, "classId": {mine}, "mine": {"true"}},
		"a class in no class":           {"audience": {"students"}, "classId": {mine}, "inNoClass": {"true"}},
		"mine in no class":              {"audience": {"students"}, "mine": {"true"}, "inNoClass": {"true"}},
		"a class for colleagues":        {"audience": {"colleagues"}, "classId": {mine}},
		"mine for colleagues":           {"audience": {"colleagues"}, "mine": {"true"}},
		"in no class for colleagues":    {"audience": {"colleagues"}, "inNoClass": {"true"}},
		"a cursor this list never gave": {"audience": {"students"}, "cursor": {"bm90LWEtY3Vyc29y"}},
	}
	for name, query := range bad {
		if status, got := teacher.call(http.MethodGet, "/teacher/messages/recipients?"+query.Encode(), nil); status != http.StatusBadRequest || errorCode(got) != "VALIDATION_FAILED" {
			t.Errorf("%s: %d %v, want 400 VALIDATION_FAILED", name, status, got)
		}
	}
	if status, _ := teacher.call(http.MethodGet, "/teacher/messages/recipients?audience=colleagues", nil); status != http.StatusNotImplemented {
		t.Errorf("the colleagues audience: %d, want 501 until T-F2.4 builds it", status)
	}

	created := teacher.must(http.StatusCreated, http.MethodPost, "/teacher/students", map[string]any{
		"email": "hoc-vien-chon-" + nonce(t) + "@example.com", "fullName": "Học viên thử " + tag, "classIds": []string{mine},
	})
	learner := w.settledIn(created["user"].(map[string]any)["email"].(string), created["temporaryPassword"].(string))
	if status, _ := learner.call(http.MethodGet, "/teacher/messages/recipients?audience=students", nil); status != http.StatusForbidden {
		t.Errorf("a student opening the picker: %d, want 403", status)
	}
}
