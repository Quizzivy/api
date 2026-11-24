//go:build integration

package repositories_test

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"quizzivy/internal/core/adapters"
	"quizzivy/internal/modules/exports/domain"
)

// TestMessagesAreSummarisedPerConversationWithoutTheirContents fails when
// messages_summary.csv leaves the export, miscounts a conversation or its
// first and last messages, writes a colleague thread (F2) without its kind or
// its two participants, or carries a message's text, a file's name or
// anything of an attachment (D17: counts, never contents).
func TestMessagesAreSummarisedPerConversationWithoutTheirContents(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	teacher, student := w.user("teacher", "Cô Hoa"), w.user("student", "Em Lan")
	colleague := w.user("teacher", "Thầy Quân")
	low, high := min(teacher, colleague), max(teacher, colleague)
	var class string
	if err := w.pool.QueryRow(ctx, `INSERT INTO app.classes (name, teacher_id) VALUES ($1, $2::uuid) RETURNING id::text`, "Lớp "+nonce(t), teacher).Scan(&class); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := w.pool.Exec(ctx, `DELETE FROM app.classes WHERE id = $1::uuid`, class); err != nil {
			t.Errorf("removing the class: %v", err)
		}
	})
	const body, secret, filename = "=HYPERLINK(\"http://x\") bí mật", "Điểm của em là 6.5", "=bang-diem-rieng.pdf"
	const staffBody, staffFile = "Lương tháng này của em Lan", "=ho-so-giao-vien.docx"
	var direct, channel, peers, file, peersFile string
	if err := w.pool.QueryRow(ctx, `INSERT INTO app.conversations (kind, staff_id, student_id, last_message_at)
		VALUES ('direct', $1::uuid, $2::uuid, '2026-11-03 04:05:06+00') RETURNING id::text`, teacher, student).Scan(&direct); err != nil {
		t.Fatal(err)
	}
	if err := w.pool.QueryRow(ctx, `INSERT INTO app.conversations (kind, class_id, last_message_at)
		VALUES ('class', $1::uuid, '2026-11-02 00:00:00+00') RETURNING id::text`, class).Scan(&channel); err != nil {
		t.Fatal(err)
	}
	// Written as T-F2.4 writes the first post: the pair low-first, nothing else.
	if err := w.pool.QueryRow(ctx, `INSERT INTO app.conversations (kind, colleague_low_id, colleague_high_id, last_message_at)
		VALUES ('colleague', $1::uuid, $2::uuid, '2026-11-05 07:08:09+00') RETURNING id::text`, low, high).Scan(&peers); err != nil {
		t.Fatal(err)
	}
	for _, a := range []struct {
		into     *string
		by, name string
	}{{&file, teacher, filename}, {&peersFile, colleague, staffFile}} {
		if err := w.pool.QueryRow(ctx, `INSERT INTO app.message_attachments (uploaded_by, storage_key, mime_type, bytes, original_filename, checksum_sha256)
			VALUES ($1::uuid, $2, 'application/pdf', 10, $3, decode(repeat('ab', 32), 'hex')) RETURNING id::text`,
			a.by, "messages/"+uuid.NewString()+".pdf", a.name).Scan(a.into); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM app.messages WHERE conversation_id = ANY($1::uuid[])`,
			`DELETE FROM app.message_attachments WHERE id = ANY($1::uuid[])`,
			`DELETE FROM app.conversations WHERE id = ANY($1::uuid[])`,
		} {
			if _, err := w.pool.Exec(ctx, sql, []string{direct, channel, peers, file, peersFile}); err != nil {
				t.Errorf("%s: %v", sql, err)
			}
		}
	})
	w.exec(`INSERT INTO app.messages (conversation_id, sender_id, client_id, body, attachment_id, created_at) VALUES
		($1::uuid, $3::uuid, gen_random_uuid(), $5, NULL, '2026-11-01 01:02:03+00'),
		($1::uuid, $4::uuid, gen_random_uuid(), $6, NULL, '2026-11-02 01:02:03+00'),
		($1::uuid, $3::uuid, gen_random_uuid(), NULL, $7::uuid, '2026-11-03 04:05:06+00'),
		($2::uuid, $3::uuid, gen_random_uuid(), 'Chào lớp', NULL, '2026-11-02 00:00:00+00')`,
		direct, channel, teacher, student, body, secret, file)
	w.exec(`INSERT INTO app.messages (conversation_id, sender_id, client_id, body, attachment_id, created_at) VALUES
		($1::uuid, $2::uuid, gen_random_uuid(), $4, NULL, '2026-11-04 01:00:00+00'),
		($1::uuid, $3::uuid, gen_random_uuid(), NULL, $5::uuid, '2026-11-05 07:08:09+00')`,
		peers, teacher, colleague, staffBody, peersFile)

	files := domain.Files(true)
	i := slices.IndexFunc(files, func(f domain.File) bool { return f.Name == domain.MessagesFile })
	if i < 0 {
		t.Fatalf("%s is not a file of the export", domain.MessagesFile)
	}
	for _, c := range files[i].Columns {
		for _, banned := range []string{"body", "text", "file", "attachment", "excerpt", "name"} {
			if strings.Contains(c.Name, banned) {
				t.Errorf("%s has a column %s, which looks like a message's content (%q)", domain.MessagesFile, c.Name, banned)
			}
		}
	}

	got := w.read(false, nil)
	col := func(name string) int { return column(t, domain.MessagesFile, name) }
	for _, c := range []struct {
		id    string
		want  map[string]string
		label string
	}{
		{direct, map[string]string{"kind": "direct", "class_id": "", "staff_id": teacher, "student_id": student,
			"colleague_a_id": "", "colleague_b_id": "", "message_count": "3",
			"first_message_at": "2026-11-01T01:02:03Z", "last_message_at": "2026-11-03T04:05:06Z"}, "the direct thread"},
		{channel, map[string]string{"kind": "class", "class_id": class, "staff_id": "", "student_id": "",
			"colleague_a_id": "", "colleague_b_id": "", "message_count": "1",
			"first_message_at": "2026-11-02T00:00:00Z", "last_message_at": "2026-11-02T00:00:00Z"}, "the class's channel"},
		{peers, map[string]string{"kind": "colleague", "class_id": "", "staff_id": "", "student_id": "",
			"colleague_a_id": low, "colleague_b_id": high, "message_count": "2",
			"first_message_at": "2026-11-04T01:00:00Z", "last_message_at": "2026-11-05T07:08:09Z"}, "the colleague thread"},
	} {
		row := got.rowWith(domain.MessagesFile, col("id"), c.id)
		if row == nil {
			t.Fatalf("%s is not in %s", c.label, domain.MessagesFile)
		}
		for header, want := range c.want {
			if cell := row[col(header)]; cell != want {
				t.Errorf("%s: %s = %q, want %q", c.label, header, cell, want)
			}
		}
	}

	var csv bytes.Buffer
	table, err := adapters.ExportTable(&csv)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.Row(files[i].Header()...); err != nil {
		t.Fatal(err)
	}
	for _, row := range got[domain.MessagesFile] {
		if err := table.Row(row...); err != nil {
			t.Fatal(err)
		}
	}
	if err := table.Flush(); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"HYPERLINK", "bí mật", secret, "bang-diem", "Chào lớp", "messages/", staffBody, "ho-so-giao-vien", file, peersFile} {
		if strings.Contains(csv.String(), content) {
			t.Errorf("%s carries %q, a message's content", domain.MessagesFile, content)
		}
	}
}
