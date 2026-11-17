//go:build integration

package application_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"quizzivy/internal/modules/sharing/application/command"
	"quizzivy/internal/modules/sharing/application/query"
	"quizzivy/internal/modules/sharing/domain"
	"quizzivy/internal/shared/access"
)

func TestAGrantIsMadeListedChangedAndRevoked(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	owner, colleague, other := w.user("Bùi Thị Giàu", "teacher"), w.user("Phan Minh Đức", "teacher"), w.user("Lê Thu Hà", "teacher")
	test := w.test(owner, "Đề giữa kỳ Đọc hiểu")

	message := "  Dùng cho lớp tối  "
	granted, err := w.app.Commands.CreateShares.Handle(ctx, command.CreateShares{
		Actor: by(owner), Kind: domain.KindTest, ResourceID: test, GranteeIDs: []string{colleague, other},
		Access: domain.AccessEdit, Notify: true, Message: &message,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(granted.Grants) != 2 || len(granted.Skipped) != 0 || granted.Grants[0].Grantee.ID != colleague || granted.Grants[1].Grantee.ID != other {
		t.Fatalf("the share made %+v", granted)
	}
	first := granted.Grants[0]
	if first.Access != domain.AccessEdit || first.Kind != domain.KindTest || first.ResourceID != test || first.GrantedBy.ID != owner ||
		first.Grantee.FullName != "Phan Minh Đức "+w.marker || first.Grantee.AvatarURL == nil || !strings.HasPrefix(*first.Grantee.AvatarURL, "https://photos.example/avatars/") {
		t.Fatalf("the first grant reads %+v", first)
	}

	listing, err := w.app.Queries.ListShares.Handle(ctx, query.ListShares{Scope: access.Scope{UserID: owner}, Kind: domain.KindTest, ResourceID: test})
	if err != nil {
		t.Fatal(err)
	}
	if listing.Owner.ID != owner || listing.Owner.AvatarURL == nil || len(listing.Grants) != 2 || listing.Grants[0].ID != first.ID || listing.Grants[1].Grantee.ID != other {
		t.Fatalf("People with access reads %+v", listing)
	}

	changed, err := w.app.Commands.UpdateShare.Handle(ctx, command.UpdateShare{Actor: by(owner), ShareID: first.ID, Access: domain.AccessUse})
	if err != nil || changed.Access != domain.AccessUse || changed.Grantee.ID != colleague {
		t.Fatalf("changing to Can use: %+v, %v", changed, err)
	}
	if again, err := w.app.Commands.UpdateShare.Handle(ctx, command.UpdateShare{Actor: by(owner), ShareID: first.ID, Access: domain.AccessUse}); err != nil || again.Access != domain.AccessUse {
		t.Fatalf("setting the level it has: %+v, %v", again, err)
	}
	if _, err := w.app.Commands.UpdateShare.Handle(ctx, command.UpdateShare{Actor: by(owner), ShareID: first.ID, Access: "owner"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("an unknown level: %v", err)
	}

	if _, err := w.app.Commands.DeleteShare.Handle(ctx, command.DeleteShare{Actor: by(owner), ShareID: first.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.app.Commands.DeleteShare.Handle(ctx, command.DeleteShare{Actor: by(owner), ShareID: first.ID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("stopping a share twice: %v", err)
	}
	listing, err = w.app.Queries.ListShares.Handle(ctx, query.ListShares{Scope: access.Scope{UserID: owner}, Kind: domain.KindTest, ResourceID: test})
	if err != nil || len(listing.Grants) != 1 || listing.Grants[0].Grantee.ID != other {
		t.Fatalf("after Stop sharing: %+v, %v", listing, err)
	}
	if exists := w.id(`SELECT count(*)::text FROM app.tests WHERE id = $1::uuid AND deleted_at IS NULL`, test); exists != "1" {
		t.Fatal("stopping a share touched the test")
	}

	want := []audited{
		{Action: "share.granted", Entity: "test", Actor: owner, Diff: sharedWith(nil, grantee(colleague, domain.AccessEdit))},
		{Action: "share.granted", Entity: "test", Actor: owner, Diff: sharedWith(nil, grantee(other, domain.AccessEdit))},
		{Action: "share.changed", Entity: "test", Actor: owner, Diff: sharedWith(grantee(colleague, domain.AccessEdit), grantee(colleague, domain.AccessUse))},
		{Action: "share.revoked", Entity: "test", Actor: owner, Diff: sharedWith(grantee(colleague, domain.AccessUse), nil)},
	}
	got := w.audits(test)
	if len(got) != len(want) {
		t.Fatalf("audit rows %+v, want %d: setting the level a grant has writes none", got, len(want))
	}
	for i := range want {
		if got[i].Action != want[i].Action || got[i].Entity != want[i].Entity || got[i].Actor != want[i].Actor || !reflect.DeepEqual(got[i].Diff, want[i].Diff) {
			t.Errorf("audit row %d is %+v, want %+v", i, got[i], want[i])
		}
		for _, name := range []string{"Đức", "Hà", "Giàu", "Dùng cho"} {
			if strings.Contains(got[i].Raw, name) {
				t.Errorf("audit row %d names %q: %s", i, name, got[i].Raw)
			}
		}
	}

	for _, user := range []string{colleague, other} {
		notes := w.notices(user)
		if len(notes) != 1 || !strings.HasPrefix(notes[0], "content.shared ") ||
			!strings.Contains(notes[0], `"name": "Đề giữa kỳ Đọc hiểu"`) || !strings.Contains(notes[0], `"access": "edit"`) ||
			!strings.Contains(notes[0], `"contentKind": "test"`) || !strings.Contains(notes[0], `"sharerName": "Bùi Thị Giàu `+w.marker+`"`) ||
			!strings.Contains(notes[0], `"message": "Dùng cho lớp tối"`) || !strings.Contains(notes[0], `{"route": "sharedWithMe"}`) ||
			!strings.HasSuffix(notes[0], "shared:test:"+test) {
			t.Errorf("%s was told %v", user, notes)
		}
	}
	if notes := w.notices(owner); len(notes) != 0 {
		t.Errorf("the sharer was told %v", notes)
	}
}

func TestANotificationIsSentOnlyWhenTheSharerAsks(t *testing.T) {
	w := newWorld(t)
	owner, colleague := w.user("Chủ đề", "teacher"), w.user("Đồng nghiệp", "teacher")
	set := w.set(owner, "Bộ ngữ pháp", 1, 0)
	w.share(by(owner), domain.KindQuestionSet, set, domain.AccessUse, colleague)
	if notes := w.notices(colleague); len(notes) != 0 {
		t.Fatalf("a share without notify told the recipient %v", notes)
	}
	if got := w.audits(set); len(got) != 1 || got[0].Entity != "question_set" || !reflect.DeepEqual(got[0].Diff, sharedWith(nil, grantee(colleague, domain.AccessUse))) {
		t.Fatalf("the set's grant audits %+v", got)
	}
}

func TestARecipientLeavesWithRemoveFromMyList(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	owner, colleague := w.user("Chủ bộ", "teacher"), w.user("Người nhận", "teacher")
	admin := w.user("Quản trị", "admin")
	set := w.set(owner, "Bộ từ vựng", 2, 0)
	grant := w.share(by(owner), domain.KindQuestionSet, set, domain.AccessEdit, colleague).Grants[0]

	for name, who := range map[string]struct {
		id  string
		all bool
	}{"the owner": {owner, false}, "an Admin": {admin, true}, "another teacher": {w.user("Người khác", "teacher"), false}} {
		a := by(who.id)
		a.Scope.All = who.all
		if _, err := w.app.Commands.RemoveSharedWithMe.Handle(ctx, command.RemoveSharedWithMe{Actor: a, ShareID: grant.ID}); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s removing the recipient's grant from their list: %v, want not found", name, err)
		}
	}
	if _, err := w.app.Commands.RemoveSharedWithMe.Handle(ctx, command.RemoveSharedWithMe{Actor: by(colleague), ShareID: grant.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.app.Commands.RemoveSharedWithMe.Handle(ctx, command.RemoveSharedWithMe{Actor: by(colleague), ShareID: grant.ID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("removing it twice: %v", err)
	}
	got := w.audits(set)
	if len(got) != 2 || got[1].Action != "share.removed" || got[1].Actor != colleague || got[1].Entity != "question_set" ||
		!reflect.DeepEqual(got[1].Diff, sharedWith(grantee(colleague, domain.AccessEdit), nil)) {
		t.Fatalf("audit rows %+v", got)
	}
	if n, err := w.app.Queries.CountSharedWithMe.Handle(ctx, query.CountSharedWithMe{UserID: colleague}); err != nil || n != 0 {
		t.Fatalf("after leaving, the recipient counts %d (%v)", n, err)
	}
}

func TestOnlyTheOwnerAndScopeAllManageAShare(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	owner, editor, stranger, fresh := w.user("Chủ", "teacher"), w.user("Biên tập", "teacher"), w.user("Người lạ", "teacher"), w.user("Người mới", "teacher")
	admin := w.user("Quản trị", "admin")
	test, set := w.test(owner, "Đề chung"), w.set(owner, "Bộ chung", 1, 0)
	grants := map[domain.Kind]domain.Grant{
		domain.KindTest:        w.share(by(owner), domain.KindTest, test, domain.AccessEdit, editor).Grants[0],
		domain.KindQuestionSet: w.share(by(owner), domain.KindQuestionSet, set, domain.AccessEdit, editor).Grants[0],
	}
	resources := map[domain.Kind]string{domain.KindTest: test, domain.KindQuestionSet: set}

	for kind, resource := range resources {
		grant := grants[kind]
		for who, want := range map[string]error{editor: domain.ErrOwnerOnly, stranger: domain.ErrNotFound} {
			_, err := w.app.Commands.CreateShares.Handle(ctx, command.CreateShares{Actor: by(who), Kind: kind, ResourceID: resource, GranteeIDs: []string{fresh}, Access: domain.AccessUse})
			if !errors.Is(err, want) {
				t.Errorf("%s: %s sharing it further: %v, want %v", kind, who, err, want)
			}
			if _, err := w.app.Queries.ListShares.Handle(ctx, query.ListShares{Scope: access.Scope{UserID: who}, Kind: kind, ResourceID: resource}); !errors.Is(err, want) {
				t.Errorf("%s: %s reading People with access: %v, want %v", kind, who, err, want)
			}
			if _, err := w.app.Commands.UpdateShare.Handle(ctx, command.UpdateShare{Actor: by(who), ShareID: grant.ID, Access: domain.AccessUse}); !errors.Is(err, want) {
				t.Errorf("%s: %s changing the grant: %v, want %v", kind, who, err, want)
			}
			if _, err := w.app.Commands.DeleteShare.Handle(ctx, command.DeleteShare{Actor: by(who), ShareID: grant.ID}); !errors.Is(err, want) {
				t.Errorf("%s: %s stopping the grant: %v, want %v", kind, who, err, want)
			}
		}
		if _, err := w.app.Queries.ListShares.Handle(ctx, query.ListShares{Scope: access.Scope{UserID: owner}, Kind: kind, ResourceID: uuid.NewString()}); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: a missing resource: %v", kind, err)
		}

		listing, err := w.app.Queries.ListShares.Handle(ctx, query.ListShares{Scope: access.Scope{UserID: admin, All: true}, Kind: kind, ResourceID: resource})
		if err != nil || listing.Owner.ID != owner || len(listing.Grants) != 1 {
			t.Fatalf("%s: the Admin reading People with access: %+v, %v", kind, listing, err)
		}
		made := w.share(byAdmin(admin), kind, resource, domain.AccessUse, owner, fresh)
		if len(made.Grants) != 1 || made.Grants[0].Grantee.ID != fresh || made.Grants[0].GrantedBy.ID != admin ||
			len(made.Skipped) != 1 || made.Skipped[0] != (domain.Skipped{UserID: owner, Code: domain.SkipSelf}) {
			t.Fatalf("%s: the Admin sharing it: %+v", kind, made)
		}
		if changed, err := w.app.Commands.UpdateShare.Handle(ctx, command.UpdateShare{Actor: byAdmin(admin), ShareID: grant.ID, Access: domain.AccessUse}); err != nil || changed.Access != domain.AccessUse {
			t.Fatalf("%s: the Admin changing the grant: %+v, %v", kind, changed, err)
		}
		if _, err := w.app.Commands.DeleteShare.Handle(ctx, command.DeleteShare{Actor: byAdmin(admin), ShareID: made.Grants[0].ID}); err != nil {
			t.Fatalf("%s: the Admin stopping a grant: %v", kind, err)
		}
	}
}

// The contract's uniqueItems compares the ids as written, so the same id in
// two spellings (upper case, braces, urn:uuid:) reaches the command twice
// once the handler has normalised it.
func TestARecipientNamedTwiceRefusesTheWholeShare(t *testing.T) {
	w := newWorld(t)
	owner, colleague, other := w.user("Chủ", "teacher"), w.user("Đồng nghiệp", "teacher"), w.user("Người khác", "teacher")
	test := w.test(owner, "Đề nêu hai lần")
	_, err := w.app.Commands.CreateShares.Handle(context.Background(), command.CreateShares{
		Actor: by(owner), Kind: domain.KindTest, ResourceID: test, GranteeIDs: []string{colleague, other, colleague},
		Access: domain.AccessUse, Notify: true,
	})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("a recipient named twice: %v, want ErrInvalid", err)
	}
	if got := w.audits(test); len(got) != 0 {
		t.Fatalf("a refused share was audited: %+v", got)
	}
	if got := w.notices(colleague); len(got) != 0 {
		t.Fatalf("a refused share notified the recipient: %v", got)
	}
}

func TestEachRecipientWithoutAGrantIsSkippedWithItsReason(t *testing.T) {
	w := newWorld(t)
	owner, holder, student, disabled, fresh := w.user("Chủ", "teacher"), w.user("Đã có", "teacher"), w.user("Học viên", "student"), w.user("Đã khoá", "teacher"), w.user("Mới", "teacher")
	w.disable(disabled)
	test := w.test(owner, "Đề bỏ qua")
	w.share(by(owner), domain.KindTest, test, domain.AccessUse, holder)

	missing := uuid.NewString()
	made := w.share(by(owner), domain.KindTest, test, domain.AccessEdit, owner, holder, student, missing, disabled, fresh)
	if len(made.Grants) != 1 || made.Grants[0].Grantee.ID != fresh || made.Grants[0].Access != domain.AccessEdit {
		t.Fatalf("grants %+v, want the one fresh teacher", made.Grants)
	}
	want := []domain.Skipped{
		{UserID: owner, Code: domain.SkipSelf}, {UserID: holder, Code: domain.SkipAlreadyShared},
		{UserID: student, Code: domain.SkipNotATeacher}, {UserID: missing, Code: domain.SkipNotATeacher},
		{UserID: disabled, Code: domain.SkipDisabled},
	}
	if !slices.Equal(made.Skipped, want) {
		t.Fatalf("skipped %+v, want %+v", made.Skipped, want)
	}
	if got := w.audits(test); len(got) != 2 {
		t.Fatalf("a skipped recipient was audited: %+v", got)
	}
	listing, err := w.app.Queries.ListShares.Handle(context.Background(), query.ListShares{Scope: access.Scope{UserID: owner}, Kind: domain.KindTest, ResourceID: test})
	if err != nil || len(listing.Grants) != 2 || listing.Grants[0].Grantee.ID != holder || listing.Grants[0].Access != domain.AccessUse {
		t.Fatalf("an ALREADY_SHARED recipient's grant changed: %+v, %v", listing, err)
	}
}
