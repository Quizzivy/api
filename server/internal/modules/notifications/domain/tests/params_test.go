package domain_test

import (
	"encoding/json"
	"errors"
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/notifications/domain"
)

var closes = time.Date(2026, 10, 24, 14, 0, 0, 0, time.UTC)

var everyKind = []struct {
	params domain.Params
	kind   domain.Kind
	names  []string
}{
	{domain.Submitted{Title: "Đề giữa kỳ", Count: 6, ToGrade: 2}, domain.AttemptSubmitted, []string{"count", "title", "toGrade"}},
	{domain.Flagged{StudentName: "Lê Hoàng Nam", Title: "Đề giữa kỳ", FocusLost: 3}, domain.AttemptFlagged, []string{"focusLost", "studentName", "title"}},
	{domain.Closing{Title: "Đề giữa kỳ", NotSubmitted: 4}, domain.AssignmentClosing, []string{"notSubmitted", "title"}},
	{domain.Joined{StudentName: "Nguyễn Gia Bảo", ClassName: "IELTS Foundation A"}, domain.ClassJoined, []string{"className", "studentName"}},
	{domain.CodesRotated{Count: 7, ClassNames: []string{"Lớp A", "Lớp B"}}, domain.JoinCodesRotated, []string{"classNames", "count"}},
	{domain.Opened{Title: "Đề giữa kỳ", ClosesAt: closes}, domain.AssignmentOpened, []string{"closesAt", "title"}},
	{domain.DueSoon{Title: "Đề giữa kỳ", ClosesAt: closes}, domain.AssignmentDueSoon, []string{"closesAt", "title"}},
	{domain.Extended{Title: "Đề giữa kỳ", ClosesAt: closes}, domain.AssignmentExtended, []string{"closesAt", "title"}},
	{domain.Ready{Title: "Đề giữa kỳ"}, domain.ResultReady, []string{"title"}},
	{domain.AllowanceWarning{Used: 16_500, Allowance: 20_000}, domain.AIAllowanceWarning, []string{"allowance", "used"}},
	{domain.CreditRequest{TeacherName: "Nguyễn Thị Lan", Credits: 20_000}, domain.AICreditRequest, []string{"credits", "teacherName"}},
	{domain.CreditRequestDecided{Credits: 20_000, Decision: domain.DecisionApproved}, domain.AICreditRequestDecided, []string{"credits", "decision"}},
	{domain.Transferred{TeacherName: "Nguyễn Thị Lan", Count: 42}, domain.ContentTransferred, []string{"count", "teacherName"}},
	{domain.TeacherChanged{ClassName: "IELTS Foundation A"}, domain.ClassTeacherChanged, []string{"className"}},
	{domain.TeacherChanged{ClassName: "IELTS Foundation A", TeacherName: &newTeacher}, domain.ClassTeacherChanged, []string{"className", "teacherName"}},
	{domain.Shared{SharerName: "Nguyễn Thị Lan", ContentKind: domain.ShareTest, Name: "Đề giữa kỳ", Access: domain.ShareEdit}, domain.ContentShared, []string{"access", "contentKind", "name", "sharerName"}},
	{domain.Shared{SharerName: "Nguyễn Thị Lan", ContentKind: domain.ShareQuestionSet, Name: "Bộ ngữ pháp", Access: domain.ShareUse, Message: &sharerMessage}, domain.ContentShared, []string{"access", "contentKind", "message", "name", "sharerName"}},
	{domain.PublishedByCollaborator{EditorName: "Trần Minh Khoa", Title: "Đề giữa kỳ", Version: 3}, domain.TestPublishedByCollaborator, []string{"editorName", "title", "version"}},
	{domain.MessageNotice{SenderName: "Lê Hà My", Count: 2, Excerpt: "Em nộp bài muộn được không ạ?"}, domain.MessageReceived, []string{"count", "excerpt", "senderName"}},
	{domain.TeacherMessageNotice{SenderName: "Cô Thương", Count: 1, Excerpt: "bai-tap.pdf"}, domain.MessageFromTeacher, []string{"count", "excerpt", "senderName"}},
	{domain.TeacherMessageNotice{SenderName: "Cô Thương", Count: 3, Excerpt: "Lớp nghỉ thứ Sáu", ClassName: &announcedIn}, domain.MessageFromTeacher, []string{"className", "count", "excerpt", "senderName"}},
	{domain.Weekly{Count: 1, ToGrade: 1, Flagged: 0, ClassNames: []string{"Lớp A", "Lớp B"}}, domain.SummaryWeekly, []string{"classNames", "count", "flagged", "toGrade"}},
}

var announcedIn = "IELTS Foundation A"

var sharerMessage = "Cô dùng bộ này cho lớp tối nhé."

var newTeacher = "Trần Minh Khoa"

func distinctKinds() int {
	seen := map[domain.Kind]bool{}
	for _, c := range everyKind {
		seen[c.kind] = true
	}
	return len(seen)
}

func contract(t *testing.T) *openapi3.T {
	t.Helper()
	spec, err := openapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func schema(t *testing.T, name string) *openapi3.Schema {
	t.Helper()
	ref := contract(t).Components.Schemas[name]
	if ref == nil || ref.Value == nil {
		t.Fatalf("the contract has no %s", name)
	}
	return ref.Value
}

func encoded(t *testing.T, p domain.Params) map[string]any {
	t.Helper()
	raw, err := domain.Encode(p)
	if err != nil {
		t.Fatalf("%T: %v", p, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%T encodes %s: %v", p, raw, err)
	}
	return out
}

func enum(t *testing.T, s *openapi3.Schema) []string {
	t.Helper()
	out := make([]string, 0, len(s.Enum))
	for _, v := range s.Enum {
		out = append(out, v.(string))
	}
	slices.Sort(out)
	return out
}

func TestEachKindEncodesExactlyItsOwnNames(t *testing.T) {
	for _, c := range everyKind {
		if got := c.params.Kind(); got != c.kind {
			t.Errorf("%T is for %s, want %s", c.params, got, c.kind)
		}
		if got := slices.Sorted(maps.Keys(encoded(t, c.params))); !slices.Equal(got, c.names) {
			t.Errorf("%s encodes %v, want exactly %v", c.kind, got, c.names)
		}
	}
}

func TestTheEncodedParamsAreTheContracts(t *testing.T) {
	params := schema(t, "NotificationParams")
	used := map[string]bool{}
	for _, c := range everyKind {
		object := encoded(t, c.params)
		if err := params.VisitJSON(object); err != nil {
			t.Errorf("%s: the contract refuses %v: %v", c.kind, object, err)
		}
		for name := range object {
			used[name] = true
		}
	}
	declared := slices.Sorted(maps.Keys(params.Properties))
	if got := slices.Sorted(maps.Keys(used)); !slices.Equal(got, declared) {
		t.Errorf("the kinds encode %v, the contract declares %v", got, declared)
	}
	if params.AdditionalProperties.Has == nil || *params.AdditionalProperties.Has {
		t.Error("NotificationParams is not a closed object")
	}
}

var kindRow = regexp.MustCompile("^\\| `([a-z_.]+)` \\| [^|]+ \\| (.+) \\|$")

var optionalName = regexp.MustCompile("^`([a-zA-Z]+)` (?:for|when) ")

func TestTheContractsKindTableIsWhatEachKindEncodes(t *testing.T) {
	kinds := schema(t, "NotificationKind")
	required, optional := map[string][]string{}, map[string][]string{}
	for _, line := range strings.Split(kinds.Description, "\n") {
		row := kindRow.FindStringSubmatch(strings.TrimSpace(line))
		if row == nil {
			continue
		}
		cells := strings.SplitN(row[2], ", and ", 2)
		names := strings.Split(strings.ReplaceAll(cells[0], "`", ""), ", ")
		slices.Sort(names)
		required[row[1]] = names
		if len(cells) == 2 {
			optional[row[1]] = optionalName.FindStringSubmatch(cells[1])[1:]
		}
	}
	var known []string
	encodes := map[string]map[string]bool{}
	for _, c := range everyKind {
		if !slices.Contains(known, string(c.kind)) {
			known = append(known, string(c.kind))
		}
		if !c.kind.Known() {
			t.Errorf("%s is not a kind the domain knows", c.kind)
		}
		want := required[string(c.kind)]
		if extra := len(c.names) - len(want); extra > 0 {
			want = slices.Sorted(slices.Values(append(slices.Clone(want), optional[string(c.kind)]...)))
		}
		if !slices.Equal(want, c.names) {
			t.Errorf("the contract says %s carries %v (and may carry %v), it encodes %v", c.kind, required[string(c.kind)], optional[string(c.kind)], c.names)
		}
		if encodes[string(c.kind)] == nil {
			encodes[string(c.kind)] = map[string]bool{}
		}
		encodes[string(c.kind)][strings.Join(c.names, ",")] = true
	}
	for kind, names := range optional {
		if len(encodes[kind]) != 2 {
			t.Errorf("%s may carry %v: everyKind needs a case with it and one without", kind, names)
		}
	}
	slices.Sort(known)
	if declared := enum(t, kinds); !slices.Equal(declared, known) {
		t.Errorf("the contract's kinds are %v, the domain's %v", declared, known)
	}
	if len(required) != len(known) {
		t.Errorf("the contract's table describes %d kinds, want %d", len(required), len(known))
	}
	if domain.Kind("content.unknown").Known() {
		t.Error("a kind outside the contract is known")
	}
}

func TestATimeIsEncodedInUTC(t *testing.T) {
	saigon := time.FixedZone("ICT", 7*3600)
	object := encoded(t, domain.DueSoon{Title: "Đề giữa kỳ", ClosesAt: time.Date(2026, 10, 24, 21, 0, 0, 0, saigon)})
	if object["closesAt"] != "2026-10-24T14:00:00Z" {
		t.Errorf("closesAt = %v, want 2026-10-24T14:00:00Z", object["closesAt"])
	}
}

func TestParamsOutsideTheContractsBoundsAreRefused(t *testing.T) {
	params := schema(t, "NotificationParams")
	limit := func(name string) int {
		property := params.Properties[name].Value
		if property.MaxLength == nil {
			t.Fatalf("%s has no maxLength in the contract", name)
		}
		return int(*property.MaxLength)
	}
	title, name, excerpt := limit("title"), limit("studentName"), limit("excerpt")
	if limit("senderName") != name {
		t.Fatal("the contract bounds a sender's name unlike the other names")
	}
	if limit("className") != name || int(*params.Properties["classNames"].Value.Items.Value.MaxLength) != name {
		t.Fatal("the contract bounds its three name fields differently")
	}
	names := int(*params.Properties["classNames"].Value.MaxItems)
	text := func(n int) string { return strings.Repeat("ệ", n) }
	classes := func(n int) []string { return slices.Repeat([]string{"Lớp"}, n) }

	for label, c := range map[string]struct {
		params domain.Params
		ok     bool
	}{
		"a title at the limit":         {domain.Ready{Title: text(title)}, true},
		"a title over the limit":       {domain.Ready{Title: text(title + 1)}, false},
		"an empty title":               {domain.Ready{}, false},
		"a name at the limit":          {domain.Joined{StudentName: text(name), ClassName: text(name)}, true},
		"a student name over":          {domain.Joined{StudentName: text(name + 1), ClassName: "Lớp"}, false},
		"a class name over":            {domain.Joined{StudentName: "Nam", ClassName: text(name + 1)}, false},
		"an empty class name":          {domain.Joined{StudentName: "Nam"}, false},
		"zero counts":                  {domain.Submitted{Title: "Đề"}, true},
		"a negative count":             {domain.Submitted{Title: "Đề", Count: -1}, false},
		"a negative to-grade":          {domain.Submitted{Title: "Đề", ToGrade: -1}, false},
		"a negative focus count":       {domain.Flagged{StudentName: "Nam", Title: "Đề", FocusLost: -1}, false},
		"a negative not-submitted":     {domain.Closing{Title: "Đề", NotSubmitted: -1}, false},
		"the most class names":         {domain.CodesRotated{Count: names, ClassNames: classes(names)}, true},
		"one class name too many":      {domain.CodesRotated{Count: names + 1, ClassNames: classes(names + 1)}, false},
		"no class name":                {domain.CodesRotated{Count: 1}, false},
		"fewer classes than names":     {domain.CodesRotated{Count: 1, ClassNames: classes(2)}, false},
		"more classes than names":      {domain.CodesRotated{Count: 9, ClassNames: classes(2)}, true},
		"an overlong name in a list":   {domain.CodesRotated{Count: 1, ClassNames: []string{text(name + 1)}}, false},
		"an empty name in a list":      {domain.CodesRotated{Count: 1, ClassNames: []string{""}}, false},
		"no closing time":              {domain.Opened{Title: "Đề"}, false},
		"a share of an unknown kind":   {domain.Shared{SharerName: "Lan", ContentKind: "course", Name: "Đề", Access: domain.ShareUse}, false},
		"a share with no access":       {domain.Shared{SharerName: "Lan", ContentKind: domain.ShareTest, Name: "Đề", Access: "own"}, false},
		"an empty share message":       {domain.Shared{SharerName: "Lan", ContentKind: domain.ShareTest, Name: "Đề", Access: domain.ShareUse, Message: new("")}, false},
		"a share message at the limit": {domain.Shared{SharerName: "Lan", ContentKind: domain.ShareTest, Name: "Đề", Access: domain.ShareUse, Message: new(text(500))}, true},
		"a share message over":         {domain.Shared{SharerName: "Lan", ContentKind: domain.ShareTest, Name: "Đề", Access: domain.ShareUse, Message: new(text(501))}, false},
		"version zero":                 {domain.PublishedByCollaborator{EditorName: "Khoa", Title: "Đề", Version: 0}, false},
		"an excerpt at the limit":      {domain.MessageNotice{SenderName: text(name), Count: 1, Excerpt: text(excerpt)}, true},
		"an excerpt over the limit":    {domain.MessageNotice{SenderName: "My", Count: 1, Excerpt: text(excerpt + 1)}, false},
		"an empty excerpt":             {domain.TeacherMessageNotice{SenderName: "Thương", Count: 1}, false},
		"a sender name over":           {domain.TeacherMessageNotice{SenderName: text(name + 1), Count: 1, Excerpt: "Chào"}, false},
		"an empty announcement class":  {domain.TeacherMessageNotice{SenderName: "Thương", Count: 1, Excerpt: "Chào", ClassName: new("")}, false},
	} {
		raw, err := domain.Encode(c.params)
		if c.ok && err != nil {
			t.Errorf("%s: refused with %v", label, err)
		}
		if !c.ok && !errors.Is(err, domain.ErrInvalidParams) {
			t.Errorf("%s: err = %v, want ErrInvalidParams; encoded %s", label, err, raw)
		}
		if !c.ok {
			continue
		}
		var object map[string]any
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		if err := params.VisitJSON(object); err != nil {
			t.Errorf("%s: the domain accepts what the contract refuses: %v", label, err)
		}
	}
}
