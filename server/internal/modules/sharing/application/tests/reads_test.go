//go:build integration

package application_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"quizzivy/internal/modules/sharing/application/query"
	"quizzivy/internal/modules/sharing/domain"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/retention"
)

func (w *world) directory(caller, q string, limit int) []domain.Person {
	w.t.Helper()
	people, err := w.app.Queries.Directory.Handle(context.Background(), query.Directory{ActorID: caller, Query: q, Limit: limit})
	if err != nil {
		w.t.Fatal(err)
	}
	return people
}

func ids(people []domain.Person) []string {
	out := make([]string, len(people))
	for i, p := range people {
		out[i] = p.ID
	}
	return out
}

func TestTheDirectoryMatchesWithoutAccentsAndFlagsDisabledAccounts(t *testing.T) {
	w := newWorld(t)
	caller := w.user("Trần Đức", "teacher")
	duc, disabled := w.user("Phan Minh Đức", "teacher"), w.user("Lê Văn Đức", "teacher")
	w.disable(disabled)
	anonymised := w.user("Hồ Văn Đức", "teacher")
	w.disable(anonymised)
	w.exec(`UPDATE app.users u SET email = `+retention.AnonymousEmail+` WHERE u.id = $1::uuid`, anonymised)
	w.user("Võ Đức", "student")
	ha := w.user("Đặng Thu Hà", "teacher")
	admin := w.user("Quản Trị Đức", "admin")
	w.exec(`UPDATE app.users SET display_name = 'Cô Hà' WHERE id = $1::uuid`, ha)

	people := w.directory(caller, "duc "+w.marker, 0)
	if got := ids(people); !slices.Equal(got, []string{disabled, duc, admin}) {
		t.Fatalf("\"duc\" found %v, want Lê Văn Đức (disabled), Phan Minh Đức and the Admin, by name, never the caller, a student or an anonymised account", got)
	}
	for _, p := range people {
		if p.Disabled != (p.ID == disabled) {
			t.Errorf("%s is flagged disabled = %v", p.FullName, p.Disabled)
		}
		if p.AvatarURL == nil || p.Email == "" || p.DisplayName != p.FullName {
			t.Errorf("%s reads %+v", p.FullName, p)
		}
	}
	if got := ids(w.directory(caller, "CÔ HÀ", 10)); !slices.Contains(got, ha) {
		t.Errorf("the display name \"Cô Hà\" is not found accent-insensitively: %v", got)
	}
	if got := ids(w.directory(caller, w.marker+"-", 10)); len(got) != 4 || slices.Contains(got, caller) {
		t.Errorf("the email finds %v, want the four colleagues", got)
	}
	if got := w.directory(caller, w.marker, 2); len(got) != 2 {
		t.Errorf("limit 2 answered %d rows", len(got))
	}
	if got := w.directory(caller, w.marker, 50); len(got) != 4 {
		t.Errorf("a limit past ten answered %d rows", len(got))
	}
	all := w.directory(caller, "", 0)
	if len(all) == 0 || len(all) > domain.MaxDirectory || slices.Contains(ids(all), caller) {
		t.Errorf("without a query the directory lists %d colleagues (caller included: %v)", len(all), slices.Contains(ids(all), caller))
	}
	for _, p := range all {
		if p.ID == anonymised || p.Email == "anonymous-"+p.ID+"@anonymous.invalid" {
			t.Errorf("without a query the directory lists the anonymised account %+v", p)
		}
	}

	anh, lan := w.user("Thu An", "teacher"), w.user("Bé Lan", "teacher")
	if got := ids(w.directory(caller, "an "+w.marker, 10)); !slices.Equal(got, []string{anh, lan}) {
		t.Errorf("\"an\" found %v, want the name with a word starting \"an\" first, then the one holding it", got)
	}
}

func TestSharedWithMeListsLiveGrantsNewestFirstWithTheirOwners(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	reader := w.user("Người nhận", "teacher")
	dat, giau := w.user("Nguyễn Văn Đạt", "teacher"), w.user("Bùi Thị Giàu", "teacher")
	grammar := w.test(dat, "Đề kiểm tra Ngữ pháp")
	words := w.set(giau, "Bộ từ vựng", 2, 1)
	gone := w.test(giau, "Đề đã xoá")
	w.share(by(dat), domain.KindTest, grammar, domain.AccessEdit, reader)
	w.share(by(giau), domain.KindQuestionSet, words, domain.AccessUse, reader)
	w.share(by(giau), domain.KindTest, gone, domain.AccessUse, reader)
	w.exec(`UPDATE app.content_shares SET created_at = created_at - make_interval(mins => CASE WHEN test_id = $2::uuid THEN 2 ELSE 1 END)
		 WHERE grantee_id = $1::uuid`, reader, grammar)
	w.exec(`UPDATE app.tests SET deleted_at = now() WHERE id = $1::uuid`, gone)
	w.disable(dat)

	read := func(f domain.SharedFilter) query.SharedPage {
		t.Helper()
		f.UserID = reader
		page, err := w.app.Queries.SharedWithMe.Handle(ctx, query.SharedWithMe{Filter: f})
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	page := read(domain.SharedFilter{})
	if page.Counts != (domain.Counts{All: 2, Test: 1, QuestionSet: 1}) || page.Page.Total != 2 || page.Page.Size != 20 || len(page.Items) != 2 {
		t.Fatalf("Shared with me reads %+v, want the live set and test, the deleted test left out", page)
	}
	set, test := page.Items[0], page.Items[1]
	if set.ResourceID != words || set.Kind != domain.KindQuestionSet || set.Name != "Bộ từ vựng" || set.Access != domain.AccessUse ||
		set.QuestionCount == nil || *set.QuestionCount != 2 || set.Owner.ID != giau || set.Owner.Disabled || set.Owner.AvatarURL == nil {
		t.Errorf("the newest row is %+v, want the set with its two live questions", set)
	}
	if test.ResourceID != grammar || test.QuestionCount != nil || !test.Owner.Disabled || test.Owner.Name != "Nguyễn Văn Đạt "+w.marker || test.Access != domain.AccessEdit {
		t.Errorf("the older row is %+v, want the test of a disabled owner, still listed", test)
	}

	if byName := read(domain.SharedFilter{Query: "ngu phap"}); len(byName.Items) != 1 || byName.Items[0].ResourceID != grammar || byName.Counts != (domain.Counts{All: 1, Test: 1}) {
		t.Errorf("\"ngu phap\" found %+v", byName)
	}
	if byOwner := read(domain.SharedFilter{Query: "DAT"}); len(byOwner.Items) != 1 || byOwner.Items[0].ResourceID != grammar {
		t.Errorf("the owner \"dat\" found %+v", byOwner)
	}
	if sets := read(domain.SharedFilter{Kind: domain.KindQuestionSet}); len(sets.Items) != 1 || sets.Page.Total != 1 || sets.Counts.All != 2 {
		t.Errorf("the sets tab reads %+v, want one row and the counts before the kind", sets)
	}
	if second := read(domain.SharedFilter{Page: 2, Limit: 1}); len(second.Items) != 1 || second.Items[0].ResourceID != grammar || second.Page.Total != 2 {
		t.Errorf("page 2 of 1 reads %+v", second)
	}
	if past := read(domain.SharedFilter{Page: 3, Limit: 1}); len(past.Items) != 0 || past.Page.Total != 2 {
		t.Errorf("a page past the end reads %+v", past)
	}
	if _, err := w.app.Queries.SharedWithMe.Handle(ctx, query.SharedWithMe{Filter: domain.SharedFilter{UserID: reader, Kind: "course"}}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("an unknown kind: %v", err)
	}
	if n, err := w.app.Queries.CountSharedWithMe.Handle(ctx, query.CountSharedWithMe{UserID: reader}); err != nil || n != 2 {
		t.Errorf("the summary counts %d (%v), want the two live grants", n, err)
	}
	if _, err := w.app.Queries.ListShares.Handle(ctx, query.ListShares{Scope: access.Scope{UserID: reader}, Kind: domain.KindTest, ResourceID: gone}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a grantee of a deleted test reading its grants: %v, want not found", err)
	}
}
