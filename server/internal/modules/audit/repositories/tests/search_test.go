//go:build integration

package repositories_test

import (
	"context"
	"slices"
	"testing"

	"quizzivy/internal/modules/audit/application/query"
	"quizzivy/internal/modules/audit/domain"
)

func (w *world) search(text string) query.ListResult {
	w.t.Helper()
	q := inMarch()
	q.Query = text
	return w.list(q)
}

func (w *world) emailOf(userID string) string {
	w.t.Helper()
	var email string
	if err := w.pool.QueryRow(context.Background(), `SELECT email FROM app.users WHERE id = $1::uuid`, userID).Scan(&email); err != nil {
		w.t.Fatal(err)
	}
	return email
}

func TestASearchCanHitOnlyTheActorsName(t *testing.T) {
	w := newWorld(t)
	vo := w.user("teacher", "Võ Thị Bảo "+w.nonce, "")
	ha := w.user("teacher", "Hà Văn Cường "+w.nonce, "")
	mine := w.write(entry{actor: vo, action: "x.y", at: march(10, 9, 0)})
	w.write(entry{actor: ha, action: "x.y", at: march(10, 10, 0)})

	for _, text := range []string{"Võ Thị Bảo " + w.nonce, "vo thi bao " + w.nonce, "VO THI BAO " + w.nonce, "thị bảo " + w.nonce} {
		if got := w.search(text); !slices.Equal(ids(got.Entries), []int64{mine}) {
			t.Errorf("%q reads %v, want only %d", text, ids(got.Entries), mine)
		}
	}
}

func TestASearchCanHitOnlyTheActorsDisplayNameOrEmail(t *testing.T) {
	w := newWorld(t)
	byDisplay := w.user("teacher", "Tên khai sinh "+nonce(t), "Cô Mai Hương "+w.nonce)
	byEmail := w.user("teacher", "Người thứ ba "+nonce(t), "")
	displayRow := w.write(entry{actor: byDisplay, action: "x.y", at: march(10, 9, 0)})
	emailRow := w.write(entry{actor: byEmail, action: "x.y", at: march(10, 10, 0)})

	if got := w.search("co mai huong " + w.nonce); !slices.Equal(ids(got.Entries), []int64{displayRow}) {
		t.Errorf("display name: %v, want %d", ids(got.Entries), displayRow)
	}
	if got := w.search(w.emailOf(byEmail)); !slices.Equal(ids(got.Entries), []int64{emailRow}) {
		t.Errorf("email: %v, want %d", ids(got.Entries), emailRow)
	}
}

func TestASearchCanHitOnlyTheSentenceThatWordsTheAction(t *testing.T) {
	w := newWorld(t)
	published := w.write(entry{action: "test.published", entity: "test_version", at: march(10, 9, 0)})
	deleted := w.write(entry{action: "test.deleted", entity: "test", at: march(10, 10, 0)})
	assignment := w.write(entry{action: "assignment.published", entity: "assignment", at: march(10, 11, 0)})

	cases := []struct {
		text string
		want []int64
	}{
		{"phát hành bài kiểm tra", []int64{published}},
		{"PHAT HANH BAI KIEM TRA", []int64{published}},
		{"published a test", []int64{published}},
		{"Published A Test", []int64{published}},
		{"phát hành", []int64{assignment, published}},
		{"published", []int64{assignment, published}},
		{"xoá bài kiểm tra", []int64{deleted}},
		{"Deleted a test", []int64{deleted}},
	}
	for _, c := range cases {
		if got := w.search(c.text); !slices.Equal(ids(got.Entries), c.want) {
			t.Errorf("%q reads %v, want %v", c.text, ids(got.Entries), c.want)
		}
	}
}

func TestASearchCanHitOnlyTheLabelOfWhatAnEntryIsAbout(t *testing.T) {
	w := newWorld(t)
	teacher := w.user("teacher", "Giáo viên "+nonce(t), "")
	named := w.class("Lớp Độc Nhất "+w.nonce, teacher)
	other := w.class("Lớp Khác "+nonce(t), teacher)
	byClass := w.write(entry{action: "x.y", entity: "class", entityID: named, at: march(10, 9, 0)})
	byMember := w.write(entry{action: "x.y", entity: "class_member", entityID: named, at: march(10, 10, 0)})
	w.write(entry{action: "x.y", entity: "class", entityID: other, at: march(10, 11, 0)})

	for _, text := range []string{"Lớp Độc Nhất " + w.nonce, "lop doc nhat " + w.nonce, "ĐỘC NHẤT " + w.nonce} {
		if got := w.search(text); !slices.Equal(ids(got.Entries), []int64{byMember, byClass}) {
			t.Errorf("%q reads %v, want %v", text, ids(got.Entries), []int64{byMember, byClass})
		}
	}
}

func TestALabelOfEveryKindIsSearchable(t *testing.T) {
	w := newWorld(t)
	f := newFixtures(w)
	for name, fixture := range f.all() {
		text := "Tìm " + name + " " + w.nonce
		row := w.write(entry{action: "x.y", entity: fixture.entity, entityID: fixture.id(text), at: march(11, 9, 0)})
		got := w.search("tim " + name + " " + w.nonce)
		if !slices.Equal(ids(got.Entries), []int64{row}) {
			t.Errorf("a %s named %q: the search reads %v, want %d", fixture.entity, text, ids(got.Entries), row)
		}
	}
}

func TestTheTextOfASearchIsLiteral(t *testing.T) {
	w := newWorld(t)
	plain := w.user("teacher", "Người làm "+w.nonce, "")
	w.write(entry{actor: plain, action: "x.y", at: march(10, 9, 0)})
	named := w.user("teacher", "Tỉ lệ 100% đúng_nhé "+w.nonce, "")
	literal := w.write(entry{actor: named, action: "x.y", at: march(10, 10, 0)})

	for _, text := range []string{"%" + w.nonce, w.nonce + "%", "Người_làm", "100_ đúng", `\` + w.nonce, "đúng%nhé"} {
		if got := w.search(text); len(got.Entries) != 0 {
			t.Errorf("%q reads %v, want nothing: it is not a pattern", text, ids(got.Entries))
		}
	}
	for _, text := range []string{"%", "_", "100% đúng_nhé " + w.nonce, "100%"} {
		if got := w.search(text); !slices.Equal(ids(got.Entries), []int64{literal}) {
			t.Errorf("%q reads %v, want only the name that holds it, %d", text, ids(got.Entries), literal)
		}
	}
}

func TestABlankSearchIsNoSearch(t *testing.T) {
	w := newWorld(t)
	row := w.write(entry{at: march(10, 9, 0)})
	for _, text := range []string{"", "   "} {
		if got := w.search(text); !slices.Contains(ids(got.Entries), row) {
			t.Errorf("%q hides the row", text)
		}
	}
}

func TestSearchAndCategoryNarrowTogether(t *testing.T) {
	w := newWorld(t)
	actor := w.user("teacher", "Người làm "+w.nonce, "")
	w.manyBy(actor, "class", 2, 12)
	w.manyBy(actor, "test", 1, 12)
	q := inMarch()
	q.Query, q.Category = w.nonce, domain.Tests
	if got := w.list(q); len(got.Entries) != 1 || got.Page.Total != 1 || got.Facets.All != 3 {
		t.Errorf("%d entries, total %d, facets %+v", len(got.Entries), got.Page.Total, got.Facets)
	}
}

// An entry with no actor is shown as "Hệ thống" or "System" (the web's list,
// the export's first column), so a reader who searches for that name means
// those entries (AU-10). The name is matched in either language, without case
// or accents, from its beginning, so the list follows the reader as they type
// it and a person called Hồng is not taken for "hệ thống".
func TestASearchForTheSystemFindsTheEntriesWithNoActor(t *testing.T) {
	w := newWorld(t)
	someone := w.user("teacher", "Người Thật "+w.nonce, "")
	bySystem := w.write(entry{action: "x.y", at: march(10, 9, 0)})
	alsoBySystem := w.write(entry{action: "x.y", entity: "maintenance_window", at: march(10, 10, 0)})
	w.write(entry{actor: someone, action: "x.y", at: march(10, 11, 0)})

	for _, text := range []string{"System", "system", "SYSTEM", "Hệ thống", "he thong", "HỆ THỐNG", "syst", "hệ th"} {
		if got := w.search(text); !slices.Equal(ids(got.Entries), []int64{alsoBySystem, bySystem}) {
			t.Errorf("%q reads %v, want the two entries with no actor, %v", text, ids(got.Entries), []int64{alsoBySystem, bySystem})
		} else if got.Facets.All != 2 {
			t.Errorf("%q counts %d entries in all, want the 2 it lists", text, got.Facets.All)
		}
	}
	for _, text := range []string{"systems " + w.nonce, "hệ thống " + w.nonce, "Hồng " + w.nonce, "thống", "yste"} {
		if got := w.search(text); slices.Contains(ids(got.Entries), bySystem) || slices.Contains(ids(got.Entries), alsoBySystem) {
			t.Errorf("%q reads %v, which holds an entry with no actor: it is not the system's name from its beginning", text, ids(got.Entries))
		}
	}
	if got := w.search("nguoi that " + w.nonce); len(got.Entries) != 1 {
		t.Errorf("a search for a person reads %d entries, want only that person's: no entry without an actor", len(got.Entries))
	}
}

func TestAnExportOfASearchForTheSystemHoldsTheSameEntries(t *testing.T) {
	w := newWorld(t)
	someone := w.user("teacher", "Người Thật "+w.nonce, "")
	bySystem := w.write(entry{action: "x.y", at: march(10, 9, 0)})
	w.write(entry{actor: someone, action: "x.y", at: march(10, 11, 0)})

	filter := w.march()
	filter.Query, filter.System = "hệ thống", domain.NamesSystem("hệ thống")
	counted, err := w.repo.CountExport(context.Background(), filter)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := w.repo.ExportBatch(context.Background(), filter, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if counted.Rows != 1 || len(batch) != 1 || batch[0].ID != bySystem {
		t.Errorf("counted %d, read %d entries; want the one entry with no actor, %d", counted.Rows, len(batch), bySystem)
	}
}
