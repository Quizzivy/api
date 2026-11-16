//go:build integration

package application_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/messaging/application/command"
	"quizzivy/internal/modules/messaging/application/ports"
	"quizzivy/internal/modules/messaging/application/query"
	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/platform/probe"
	"quizzivy/internal/shared/actor"
)

// files is an object store in memory: what was put, under which type, when,
// and what a signature named.
type files struct {
	mu      sync.Mutex
	objects map[string]stored
	deleted []string
	signed  []signature
	failDel map[string]bool
}

type stored struct {
	data      []byte
	mime      string
	writtenAt time.Time
}

type signature struct {
	key, filename, mime string
	ttl                 time.Duration
}

func newFiles() *files { return &files{objects: map[string]stored{}, failDel: map[string]bool{}} }

func (f *files) Put(_ context.Context, key, contentType string, body io.Reader, size int64) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return errors.New("files: size does not match the body")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = stored{data: data, mime: contentType, writtenAt: time.Now()}
	return nil
}

func (f *files) Delete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failDel[key] {
		return errors.New("files: delete refused")
	}
	delete(f.objects, key)
	f.deleted = append(f.deleted, key)
	return nil
}

func (f *files) SignedAttachmentURL(_ context.Context, key, filename, contentType string, ttl time.Duration) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signed = append(f.signed, signature{key, filename, contentType, ttl})
	return "https://files.example/" + key + "?sig=1", nil
}

func (f *files) ListAfter(_ context.Context, prefix, after string, limit int) ([]ports.StoredObject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keys []string
	for key := range f.objects {
		if strings.HasPrefix(key, prefix) && key > after {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	var out []ports.StoredObject
	for _, key := range keys[:min(limit, len(keys))] {
		out = append(out, ports.StoredObject{Key: key, WrittenAt: f.objects[key].writtenAt})
	}
	return out, nil
}

// put stores an object written at at, as the sweep finds what anonymisation
// or a failed delete left.
func (f *files) put(key string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = stored{data: []byte("x"), mime: "application/pdf", writtenAt: at}
}

func (f *files) has(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.objects[key]
	return ok
}

// audio is the platform probe, as the composition root's adapter wires it.
type audio struct{}

func (audio) Audio(r io.ReaderAt, size int64) (string, error) {
	mime, _, err := probe.Audio(r, size)
	if err != nil {
		return "", errors.Join(domain.ErrUnsupportedType, err)
	}
	return mime, nil
}

func (w *world) withFiles() *files {
	w.t.Helper()
	store := newFiles()
	w.app.WithAttachments(store, audio{})
	return store
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "..", "shared", "filetype", "testdata")
	if strings.HasSuffix(name, ".mp3") || strings.HasSuffix(name, ".m4a") {
		dir = filepath.Join("..", "..", "..", "..", "platform", "probe", "testdata")
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (w *world) upload(uploader, filename string, data []byte) (domain.Attachment, error) {
	w.t.Helper()
	return w.app.Commands.UploadAttachment.Handle(context.Background(), command.UploadAttachment{
		UploaderID: uploader, Filename: filename, Body: bytes.NewReader(data),
	})
}

func (w *world) mustUpload(uploader, filename string, data []byte) domain.Attachment {
	w.t.Helper()
	a, err := w.upload(uploader, filename, data)
	if err != nil {
		w.t.Fatalf("uploading %s: %v", filename, err)
	}
	return a
}

func TestAnUploadIsTypedByItsContentAndKeptAsItCame(t *testing.T) {
	w := newWorld(t)
	store := w.withFiles()
	teacher := w.user("Cô Hạnh", "teacher")

	cases := []struct {
		fixture, filename string
		mime, stored      string
		err               error
	}{
		{"document.pdf", "Bài tập tuần 3.pdf", domain.MimePDF, "Bài tập tuần 3.pdf", nil},
		{"document.pdf", "phieu-on-tap", domain.MimePDF, "phieu-on-tap.pdf", nil},
		{"document.pdf", `C:\Users\co\de-thi.exe`, domain.MimePDF, "de-thi.exe.pdf", nil},
		{"word.docx", "Đề cương.docx", domain.MimeDOCX, "Đề cương.docx", nil},
		{"word.doc", "de-cu.DOC", domain.MimeDOC, "de-cu.DOC", nil},
		{"photo.png", "anh.png", domain.MimePNG, "anh.png", nil},
		{"photo.jpg", "anh.jpeg", domain.MimeJPEG, "anh.jpeg", nil},
		{"cbr-128k.mp3", "bai-nghe.mp3", domain.MimeMP3, "bai-nghe.mp3", nil},
		{"mvhd-v0.m4a", "ghi-am.m4a", domain.MimeM4A, "ghi-am.m4a", nil},
		{"program.exe", "bai-tap.pdf", "", "", domain.ErrUnsupportedType},
		{"huge.png", "anh-lon.png", "", "", domain.ErrImageDimensions},
		{"photo.webp", "anh.webp", "", "", domain.ErrUnsupportedType},
		{"plain.zip", "bai-tap.docx", "", "", domain.ErrUnsupportedType},
		{"sheet.xlsx", "diem.xlsx", "", "", domain.ErrUnsupportedType},
		{"sheet.xls", "diem.xls", "", "", domain.ErrUnsupportedType},
		{"embedded-word.xls", "diem.doc", "", "", domain.ErrUnsupportedType},
		{"macros.docm", "de.docm", "", "", domain.ErrHasMacros},
		{"macros.doc", "de.doc", "", "", domain.ErrHasMacros},
		{"wav-renamed.mp3", "bai-nghe.mp3", "", "", domain.ErrUnsupportedType},
	}
	for _, c := range cases {
		data := fixture(t, c.fixture)
		before := len(store.objects)
		got, err := w.upload(teacher, c.filename, data)
		if c.err != nil {
			wantErr(t, c.fixture+" as "+c.filename, err, c.err)
			if len(store.objects) != before {
				t.Errorf("%s: a refused file was stored", c.fixture)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s as %s: %v", c.fixture, c.filename, err)
			continue
		}
		if got.MimeType != c.mime || got.Filename != c.stored || got.Bytes != len(data) {
			t.Errorf("%s as %s: %+v, want %s named %q of %d bytes", c.fixture, c.filename, got, c.mime, c.stored, len(data))
		}
		key := domain.StorageKey(got.ID, c.mime)
		object, ok := store.objects[key]
		if !ok || !bytes.Equal(object.data, data) || object.mime != c.mime {
			t.Errorf("%s: the object at %s holds %d bytes of %s, want the %d bytes sent, as %s", c.fixture, key, len(object.data), object.mime, len(data), c.mime)
		}
		var rowKey, rowName string
		var checksum []byte
		w.value(`SELECT storage_key FROM app.message_attachments WHERE id = $1::uuid AND uploaded_by = $2::uuid`, &rowKey, got.ID, teacher)
		w.value(`SELECT original_filename FROM app.message_attachments WHERE id = $1::uuid`, &rowName, got.ID)
		w.value(`SELECT checksum_sha256 FROM app.message_attachments WHERE id = $1::uuid`, &checksum, got.ID)
		sum := sha256.Sum256(data)
		if rowKey != key || !strings.HasPrefix(rowKey, "messages/") || rowName != c.stored || !bytes.Equal(checksum, sum[:]) {
			t.Errorf("%s: the row holds %s, %q, %x", c.fixture, rowKey, rowName, checksum)
		}
	}
	if _, err := w.upload(teacher, "a\x00b.pdf", fixture(t, "document.pdf")); !errors.Is(err, domain.ErrInvalidFilename) {
		t.Errorf("a filename holding U+0000: %v, want ErrInvalidFilename", err)
	}
	if _, err := w.upload(teacher, "rong.pdf", nil); !errors.Is(err, domain.ErrUnsupportedType) {
		t.Errorf("an empty file: %v, want ErrUnsupportedType", err)
	}
}

func TestAFileOfTwentyMiBIsTakenAndOneByteMoreIsNot(t *testing.T) {
	w := newWorld(t)
	store := w.withFiles()
	teacher := w.user("Cô Hạnh", "teacher")
	limit := make([]byte, domain.MaxAttachmentBytes)
	copy(limit, "%PDF-1.4\n")
	if got, err := w.upload(teacher, "lon.pdf", limit); err != nil || got.Bytes != domain.MaxAttachmentBytes {
		t.Errorf("a PDF of exactly 20 MiB: %+v, %v, want it stored", got, err)
	}
	stored := len(store.objects)
	over := append(limit, 0)
	if _, err := w.upload(teacher, "qua-lon.pdf", over); !errors.Is(err, domain.ErrTooLarge) {
		t.Errorf("20 MiB + 1: %v, want ErrTooLarge", err)
	}
	if len(store.objects) != stored {
		t.Error("a file over the limit was stored")
	}
}

func TestAnUploadWithoutAStoreAnswersThatItIsOff(t *testing.T) {
	w := newWorld(t)
	teacher := w.user("Cô Hạnh", "teacher")
	if _, err := w.upload(teacher, "a.pdf", fixture(t, "document.pdf")); !errors.Is(err, domain.ErrStorageOff) {
		t.Errorf("an upload with no store: %v, want ErrStorageOff", err)
	}
	_, err := w.app.Queries.StaffAttachmentURL.Handle(context.Background(), query.StaffAttachmentURL{ReaderID: teacher, ID: uuid.NewString()})
	wantErr(t, "a download with no store", err, domain.ErrStorageOff)
}

func (w *world) postFile(who actor.Actor, student, file string, body *string) (domain.Posted, error) {
	w.t.Helper()
	return w.app.Commands.PostDirect.Handle(context.Background(), command.PostDirect{
		Actor: who, StudentID: student, ClientID: uuid.NewString(), Body: body, AttachmentID: &file,
	})
}

func TestAPostSendsOnlyAFileItsSenderUploadedAndNobodySent(t *testing.T) {
	w := newWorld(t)
	w.withFiles()
	teacher, colleague := w.user("Cô Hạnh", "teacher"), w.user("Thầy Tùng", "teacher")
	class := w.class(teacher, "Lớp 8A")
	student := w.user("Học viên Mai", "student")
	w.enrol(class, student)
	w.enrol(w.class(colleague, "Lớp 8B"), student)
	ctx := context.Background()

	file := w.mustUpload(teacher, "Bài tập.pdf", fixture(t, "document.pdf"))
	_, err := w.postFile(by(colleague), student, file.ID, text("Của cô Hạnh"))
	wantErr(t, "another teacher's file", err, domain.ErrAttachmentNotFound)
	_, err = w.postFile(by(teacher), student, uuid.NewString(), nil)
	wantErr(t, "a missing file", err, domain.ErrAttachmentNotFound)

	clientID := uuid.NewString()
	sent := command.PostDirect{Actor: by(teacher), StudentID: student, ClientID: clientID, AttachmentID: &file.ID}
	posted, err := w.app.Commands.PostDirect.Handle(ctx, sent)
	if err != nil || !posted.Created || posted.Message.Body != nil || posted.Message.Attachment == nil || *posted.Message.Attachment != file {
		t.Fatalf("a file alone: %+v, %v, want a message holding %+v", posted, err, file)
	}
	again, err := w.app.Commands.PostDirect.Handle(ctx, sent)
	if err != nil || again.Created || again.Message.ID != posted.Message.ID {
		t.Errorf("the same clientId again: %+v, %v, want the message it made", again, err)
	}
	_, err = w.postFile(by(teacher), student, file.ID, text("Gửi lại"))
	wantErr(t, "a sent file in a new post", err, domain.ErrAttachmentNotFound)
	_, err = w.app.Commands.PostAnnouncement.Handle(ctx, command.PostAnnouncement{
		Actor: by(teacher), MayWrite: true, ClassID: class, ClientID: uuid.NewString(), Body: text("Cả lớp xem"), AttachmentID: &file.ID,
	})
	wantErr(t, "a sent file in an announcement", err, domain.ErrAttachmentNotFound)

	page, err := w.thread(teacher, student, domain.Cursor{})
	if err != nil || len(page.Page.Messages) != 1 || page.Page.Messages[0].Attachment == nil || page.Page.Messages[0].Attachment.Filename != "Bài tập.pdf" {
		t.Errorf("the thread: %+v, %v, want the one message with its file", page.Page.Messages, err)
	}
	mine, err := w.studentThread(student, teacher, domain.Cursor{})
	if err != nil || len(mine.Page.Messages) != 1 || mine.Page.Messages[0].Attachment == nil || mine.Page.Messages[0].Attachment.ID != file.ID {
		t.Errorf("the student's thread: %+v, %v, want the file", mine.Page.Messages, err)
	}
	var listed *string
	for _, row := range w.mine(w.list(by(teacher), domain.TabDirect, "")) {
		if row.Preview != nil {
			listed = row.Preview.AttachmentName
		}
	}
	if listed == nil || *listed != "Bài tập.pdf" {
		t.Errorf("the list's preview names %v, want the file", listed)
	}

	announced := w.mustUpload(teacher, "Lịch.png", fixture(t, "photo.png"))
	made, err := w.app.Commands.PostAnnouncement.Handle(ctx, command.PostAnnouncement{
		Actor: by(teacher), MayWrite: true, ClassID: class, ClientID: uuid.NewString(), Body: text("Lịch thi"), AttachmentID: &announced.ID,
	})
	if err != nil || made.Message.Attachment == nil || made.Message.Attachment.ID != announced.ID {
		t.Errorf("an announcement with a file: %+v, %v", made, err)
	}
}

func TestADownloadIsForTheThreadsParticipantsAndItsUploaderBeforeSending(t *testing.T) {
	w := newWorld(t)
	store := w.withFiles()
	now := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	w.app.WithClock(func() time.Time { return now })
	teacher, colleague, admin := w.user("Cô Hạnh", "teacher"), w.user("Thầy Tùng", "teacher"), w.user("Quản trị", "admin")
	class := w.class(teacher, "Lớp 8A")
	student, classmate, outsider := w.user("Học viên Mai", "student"), w.user("Học viên Lan", "student"), w.user("Học viên Bình", "student")
	w.enrol(class, student)
	w.enrol(class, classmate)
	w.enrol(w.class(colleague, "Lớp 8B"), outsider)
	ctx := context.Background()

	staff := func(who actor.Actor, id string) error {
		_, err := w.app.Queries.StaffAttachmentURL.Handle(ctx, query.StaffAttachmentURL{ReaderID: who.ID, Scope: who.Scope, ID: id})
		return err
	}
	learner := func(who, id string) error {
		_, err := w.app.Queries.StudentAttachmentURL.Handle(ctx, query.StudentAttachmentURL{StudentID: who, ID: id})
		return err
	}

	unsent := w.mustUpload(teacher, "Nháp.pdf", fixture(t, "document.pdf"))
	signed, err := w.app.Queries.StaffAttachmentURL.Handle(ctx, query.StaffAttachmentURL{ReaderID: teacher, Scope: by(teacher).Scope, ID: unsent.ID})
	if err != nil || signed.URL == "" || !signed.ExpiresAt.Equal(now.Add(5*time.Minute)) {
		t.Errorf("the uploader's unsent file: %+v, %v, want a URL expiring at %v", signed, err, now.Add(5*time.Minute))
	}
	last := store.signed[len(store.signed)-1]
	if last.key != domain.StorageKey(unsent.ID, domain.MimePDF) || last.filename != "Nháp.pdf" || last.mime != domain.MimePDF || last.ttl != 5*time.Minute {
		t.Errorf("the signature named %+v, want the file's key, name, sniffed type and five minutes", last)
	}
	wantErr(t, "an unsent file for another teacher", staff(by(colleague), unsent.ID), domain.ErrAttachmentNotFound)
	wantErr(t, "an unsent file for an admin", staff(byAdmin(admin), unsent.ID), domain.ErrAttachmentNotFound)
	wantErr(t, "an unsent file for a student", learner(student, unsent.ID), domain.ErrAttachmentNotFound)
	wantErr(t, "a missing file", staff(by(teacher), uuid.NewString()), domain.ErrAttachmentNotFound)

	direct := w.mustUpload(teacher, "Riêng.pdf", fixture(t, "document.pdf"))
	if _, err := w.postFile(by(teacher), student, direct.ID, nil); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"the teacher":   staff(by(teacher), direct.ID),
		"their student": learner(student, direct.ID),
	} {
		if err != nil {
			t.Errorf("a direct message's file for %s: %v", name, err)
		}
	}
	for name, err := range map[string]error{
		"another teacher":                staff(by(colleague), direct.ID),
		"an admin, whose scope is all":   staff(byAdmin(admin), direct.ID),
		"a classmate":                    learner(classmate, direct.ID),
		"a student of another class":     learner(outsider, direct.ID),
		"the teacher on the student URL": learner(teacher, direct.ID),
	} {
		wantErr(t, "a direct message's file for "+name, err, domain.ErrAttachmentNotFound)
	}

	channel := w.mustUpload(teacher, "Lịch thi.pdf", fixture(t, "document.pdf"))
	if _, err := w.app.Commands.PostAnnouncement.Handle(ctx, command.PostAnnouncement{
		Actor: by(teacher), MayWrite: true, ClassID: class, ClientID: uuid.NewString(), AttachmentID: &channel.ID,
	}); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"the class's teacher":                       staff(by(teacher), channel.ID),
		"an admin, who reads every class's channel": staff(byAdmin(admin), channel.ID),
		"a member":       learner(student, channel.ID),
		"another member": learner(classmate, channel.ID),
	} {
		if err != nil {
			t.Errorf("an announcement's file for %s: %v", name, err)
		}
	}
	wantErr(t, "an announcement's file for another teacher", staff(by(colleague), channel.ID), domain.ErrAttachmentNotFound)
	wantErr(t, "an announcement's file for a student of another class", learner(outsider, channel.ID), domain.ErrAttachmentNotFound)
	w.remove(class, classmate)
	wantErr(t, "an announcement's file for a student who left", learner(classmate, channel.ID), domain.ErrAttachmentNotFound)
	// The class passes to the colleague: the channel is theirs to read now,
	// and its uploader, no longer a reader, is not let in for having
	// uploaded it.
	w.exec(`UPDATE app.classes SET teacher_id = $2::uuid WHERE id = $1::uuid`, class, colleague)
	if err := staff(by(colleague), channel.ID); err != nil {
		t.Errorf("an announcement's file for the class's new teacher: %v", err)
	}
	wantErr(t, "an announcement's file for its uploader once the class is another's", staff(by(teacher), channel.ID), domain.ErrAttachmentNotFound)
	w.remove(class, student)
	if err := learner(student, direct.ID); err != nil {
		t.Errorf("a direct message's file for its student after leaving the class: %v, want the thread's file still theirs", err)
	}
}

func TestTheSweepsRemoveUnsentFilesAndUnnamedObjectsAndKeepSentOnes(t *testing.T) {
	w := newWorld(t)
	store := w.withFiles()
	teacher := w.user("Cô Hạnh", "teacher")
	class := w.class(teacher, "Lớp 8A")
	student := w.user("Học viên Mai", "student")
	w.enrol(class, student)
	ctx := context.Background()

	old := w.mustUpload(teacher, "Cũ.pdf", fixture(t, "document.pdf"))
	sent := w.mustUpload(teacher, "Đã gửi.pdf", fixture(t, "document.pdf"))
	fresh := w.mustUpload(teacher, "Mới.pdf", fixture(t, "document.pdf"))
	if _, err := w.postFile(by(teacher), student, sent.ID, nil); err != nil {
		t.Fatal(err)
	}
	w.exec(`UPDATE app.message_attachments SET created_at = now() - interval '25 hours' WHERE id = ANY($1::uuid[])`, []string{old.ID, sent.ID})
	keyOf := func(a domain.Attachment) string { return domain.StorageKey(a.ID, a.MimeType) }

	removed, err := w.app.Commands.SweepUnsentAttachments.Handle(ctx, command.SweepUnsentAttachments{Limit: 500})
	if err != nil || removed < 1 {
		t.Fatalf("the unsent sweep: %d, %v", removed, err)
	}
	var rows int
	w.value(`SELECT count(*)::int FROM app.message_attachments WHERE id = $1::uuid`, &rows, old.ID)
	if rows != 0 || store.has(keyOf(old)) {
		t.Errorf("a file unsent for 25 hours: row %d, object %v, want both gone", rows, store.has(keyOf(old)))
	}
	w.value(`SELECT count(*)::int FROM app.message_attachments WHERE id = ANY($1::uuid[])`, &rows, []string{sent.ID, fresh.ID})
	if rows != 2 || !store.has(keyOf(sent)) || !store.has(keyOf(fresh)) {
		t.Errorf("a sent file and one uploaded now: %d rows, objects %v %v, want both kept", rows, store.has(keyOf(sent)), store.has(keyOf(fresh)))
	}

	long := time.Now().Add(-48 * time.Hour)
	orphans := []string{"messages/" + uuid.NewString() + ".pdf", "messages/" + uuid.NewString() + ".png", "messages/" + uuid.NewString() + ".doc"}
	for _, key := range orphans {
		store.put(key, long)
	}
	young := "messages/" + uuid.NewString() + ".pdf"
	store.put(young, time.Now())
	elsewhere := "media/" + uuid.NewString() + ".png"
	store.put(elsewhere, long)
	store.put(keyOf(sent), long)

	// Two at a time: the sweep resumes where it stopped and starts over once
	// it reaches the end, so every orphan is found whatever the page.
	total := 0
	for range 6 {
		n, err := w.app.Commands.SweepAttachmentObjects.Handle(ctx, command.SweepAttachmentObjects{Limit: 2})
		if err != nil {
			t.Fatalf("the object sweep: %v", err)
		}
		total += n
	}
	if total != len(orphans) {
		t.Errorf("the object sweep removed %d, want the %d unnamed old objects", total, len(orphans))
	}
	for _, key := range orphans {
		if store.has(key) {
			t.Errorf("an unnamed object written two days ago (%s) was kept", key)
		}
	}
	for name, key := range map[string]string{"a sent file's": keyOf(sent), "a fresh upload's": keyOf(fresh), "an object written now": young, "an object outside messages/": elsewhere} {
		if !store.has(key) {
			t.Errorf("%s object was removed", name)
		}
	}

	failing := "messages/" + uuid.NewString() + ".pdf"
	store.put(failing, long)
	store.failDel[failing] = true
	survivor := "messages/" + uuid.NewString() + ".pdf"
	store.put(survivor, long)
	_, err = w.app.Commands.SweepAttachmentObjects.Handle(ctx, command.SweepAttachmentObjects{Limit: 500})
	if err == nil || store.has(survivor) {
		t.Errorf("a delete that fails: %v, survivor kept %v; want the error and the rest still removed", err, store.has(survivor))
	}
}

func TestTwoPostsOfOneFileSendItOnce(t *testing.T) {
	p := newPooled(t)
	store := newFiles()
	p.app.WithAttachments(store, audio{})
	teacher := p.user("Cô Hạnh", "teacher")
	class := p.class(teacher, "Lớp 8A")
	student := p.user("Học viên Mai", "student", teacher)
	if _, err := p.pool.Exec(context.Background(), `INSERT INTO app.class_members (class_id, user_id, joined_via) VALUES ($1::uuid, $2::uuid, 'admin')`, class, student); err != nil {
		t.Fatal(err)
	}
	hanh := by(teacher)

	for attempt := range 4 {
		file, err := p.app.Commands.UploadAttachment.Handle(context.Background(), command.UploadAttachment{
			UploaderID: teacher, Filename: "Bài tập.pdf", Body: bytes.NewReader(fixture(t, "document.pdf")),
		})
		if err != nil {
			t.Fatal(err)
		}
		posts := [2]func() (domain.Posted, error){
			func() (domain.Posted, error) {
				return p.app.Commands.PostDirect.Handle(context.Background(), command.PostDirect{Actor: hanh, StudentID: student, ClientID: uuid.NewString(), AttachmentID: &file.ID})
			},
			func() (domain.Posted, error) {
				return p.app.Commands.PostAnnouncement.Handle(context.Background(), command.PostAnnouncement{Actor: hanh, MayWrite: true, ClassID: class, ClientID: uuid.NewString(), AttachmentID: &file.ID})
			},
		}
		var results [2]result
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range 2 {
			wg.Go(func() {
				<-start
				posted, err := posts[i]()
				results[i] = result{posted, err}
			})
		}
		close(start)
		wg.Wait()
		sent, missing := 0, 0
		for _, r := range results {
			switch {
			case r.err == nil:
				sent++
			case errors.Is(r.err, domain.ErrAttachmentNotFound):
				missing++
			default:
				t.Errorf("attempt %d: %v, want a post or a missing file", attempt, r.err)
			}
		}
		holding := p.count(`SELECT count(*)::int FROM app.messages WHERE attachment_id = $1::uuid`, file.ID)
		if sent != 1 || missing != 1 || holding != 1 {
			t.Errorf("attempt %d: %d sent, %d answered missing, %d messages hold the file; want one of each and one message", attempt, sent, missing, holding)
		}
	}

	// The same race, held: a post that names the file is open when the
	// announcement checks it. The check passes, as nobody has sent it yet;
	// the announcement's INSERT waits on the UNIQUE of attachment_id and,
	// once the other commits, answers the file as missing, not a 500.
	file := p.upload(teacher)
	thread := p.id(`SELECT id::text FROM app.conversations WHERE kind = 'direct' AND staff_id = $1::uuid AND student_id = $2::uuid`, teacher, student)
	holder := p.begin()
	if _, err := holder.Exec(context.Background(), `INSERT INTO app.messages (conversation_id, sender_id, client_id, attachment_id) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid)`,
		thread, teacher, uuid.NewString(), file); err != nil {
		t.Fatal(err)
	}
	r := p.waitOn(holder, func() (domain.Posted, error) {
		return p.app.Commands.PostAnnouncement.Handle(context.Background(), command.PostAnnouncement{Actor: hanh, MayWrite: true, ClassID: class, ClientID: uuid.NewString(), AttachmentID: &file})
	})
	wantErr(t, "an announcement whose file a held post sent", r.err, domain.ErrAttachmentNotFound)

	// And against the sweep: the file's row is being deleted when a post
	// names it. The post's foreign key check waits on the row, and once the
	// delete commits the file is a missing one.
	file = p.upload(teacher)
	holder = p.begin()
	if _, err := holder.Exec(context.Background(), `DELETE FROM app.message_attachments WHERE id = $1::uuid`, file); err != nil {
		t.Fatal(err)
	}
	r = p.waitOn(holder, func() (domain.Posted, error) {
		return p.app.Commands.PostDirect.Handle(context.Background(), command.PostDirect{Actor: hanh, StudentID: student, ClientID: uuid.NewString(), AttachmentID: &file})
	})
	wantErr(t, "a post whose file the sweep removed meanwhile", r.err, domain.ErrAttachmentNotFound)
}

func (p *pooled) upload(teacher string) string {
	p.t.Helper()
	file, err := p.app.Commands.UploadAttachment.Handle(context.Background(), command.UploadAttachment{
		UploaderID: teacher, Filename: "Bài tập.pdf", Body: bytes.NewReader(fixture(p.t, "document.pdf")),
	})
	if err != nil {
		p.t.Fatal(err)
	}
	return file.ID
}

func (p *pooled) begin() pgx.Tx {
	p.t.Helper()
	tx, err := p.pool.Begin(context.Background())
	if err != nil {
		p.t.Fatal(err)
	}
	return tx
}

// waitOn runs post while tx is open, checks that it waits, then commits tx
// and returns what the post answered.
func (p *pooled) waitOn(tx pgx.Tx, post func() (domain.Posted, error)) result {
	p.t.Helper()
	done := make(chan result, 1)
	go func() {
		posted, err := post()
		done <- result{posted, err}
	}()
	select {
	case r := <-done:
		_ = tx.Rollback(context.Background())
		p.t.Fatalf("the post finished (%v) while the other writer was open: it did not reach the file", r.err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(context.Background()); err != nil {
		p.t.Fatal(err)
	}
	return <-done
}
