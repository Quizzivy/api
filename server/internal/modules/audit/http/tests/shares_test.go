package http_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/audit/domain"
)

func TestAShareNamesItsRecipientAsTheAdminDeckDoes(t *testing.T) {
	const removed = "01935000-0000-7000-8000-0000000000d9"
	test := "01935000-0000-7000-8000-0000000000b1"
	granted := record(3, march(10, 9, 0), "share.granted", "test")
	granted.EntityID = &test
	granted.Diff = []byte(`{"shared_with": {"old": null, "new": {"granteeId": "` + teacherID + `", "access": "edit"}}}`)
	changed := record(2, march(10, 8, 0), "share.changed", "test")
	changed.EntityID = &test
	changed.Diff = []byte(`{"shared_with": {"old": {"granteeId": "` + teacherID + `", "access": "edit"}, "new": {"granteeId": "` + teacherID + `", "access": "use"}}}`)
	gone := record(1, march(10, 7, 0), "share.revoked", "test")
	gone.EntityID = &test
	gone.Diff = []byte(`{"shared_with": {"old": {"granteeId": "` + removed + `", "access": "use"}, "new": null}}`)
	l := &log{records: []domain.Record{granted, changed, gone}, labels: map[domain.EntityRef]string{
		{Type: "test", ID: test}:      "Mid-term Reading Mock",
		{Type: "user", ID: teacherID}: "Phan Minh Đức",
	}}

	rec, _ := answered(t, "en", listing(l.transport(), openapi.ListAuditLogParams{}))
	var body page
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 3 {
		t.Fatalf("%d entries: %s", len(body.Items), rec.Body)
	}
	want := [][2]any{
		{nil, "Phan Minh Đức (can edit)"},
		{"Phan Minh Đức (can edit)", "Phan Minh Đức (can use)"},
		{map[string]any{"granteeId": removed, "access": "use"}, nil},
	}
	for i, item := range body.Items {
		if item.Entity.Label == nil || *item.Entity.Label != "Mid-term Reading Mock" {
			t.Errorf("%s names the test %v", item.Action, item.Entity.Label)
		}
		if len(item.Changes) != 1 || item.Changes[0].Field != domain.ShareField {
			t.Fatalf("%s changes %+v", item.Action, item.Changes)
		}
		if got := [2]any{item.Changes[0].From, item.Changes[0].To}; !reflect.DeepEqual(got, want[i]) {
			t.Errorf("%s shows %v, want %v: a known recipient by name and level, a removed one by the ids it was stored with", item.Action, got, want[i])
		}
	}
	if strings.Contains(string(granted.Diff), "Phan") {
		t.Error("the stored diff names the recipient")
	}
}
