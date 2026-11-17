//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func attachmentFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "internal", "shared", "filetype", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestAStaffMemberSendsAFileAndTheStudentDownloadsIt is message attachments
// through HTTP (T-R7.11): an upload typed by its content and refused with
// the code the dialog words, a post that sends the file once, and a download
// URL for the thread's participants that the object store serves as an
// attachment under the file's name and sniffed type, never cached.
func TestAStaffMemberSendsAFileAndTheStudentDownloadsIt(t *testing.T) {
	w := bootWithStorage(t)
	teacher := w.signedIn(w.createStaff("teacher"))
	other := w.signedIn(w.createStaff("teacher"))
	admin := w.signedIn(w.createStaff("admin"))
	class := teacher.class("Lớp đính kèm " + nonce(t))
	created := teacher.must(http.StatusCreated, http.MethodPost, "/teacher/students", map[string]any{
		"email": "dinh-kem-" + nonce(t) + "@example.com", "fullName": "Học viên đính kèm", "classIds": []string{class},
	})
	studentID := id(created["user"].(map[string]any))
	student := w.settledIn(created["user"].(map[string]any)["email"].(string), created["temporaryPassword"].(string))

	upload := func(c *client, name string, data []byte) sent {
		return c.send(http.MethodPost, "/teacher/messages/attachments", new(filePayload(t, name, data)))
	}
	for name, c := range map[string]struct {
		fixture string
		data    []byte
		status  int
		code    string
	}{
		"an executable named .pdf":             {fixture: "program.exe", status: http.StatusUnsupportedMediaType, code: "MEDIA_TYPE_UNSUPPORTED"},
		"a PNG declaring 50000 × 50000 pixels": {fixture: "huge.png", status: http.StatusUnsupportedMediaType, code: "IMAGE_DIMENSIONS"},
		"a WebP":                               {fixture: "photo.webp", status: http.StatusUnsupportedMediaType, code: "MEDIA_TYPE_UNSUPPORTED"},
		"a ZIP named .docx":                    {fixture: "plain.zip", status: http.StatusUnsupportedMediaType, code: "MEDIA_TYPE_UNSUPPORTED"},
		"an XLSX":                              {fixture: "sheet.xlsx", status: http.StatusUnsupportedMediaType, code: "MEDIA_TYPE_UNSUPPORTED"},
		"an XLS":                               {fixture: "sheet.xls", status: http.StatusUnsupportedMediaType, code: "MEDIA_TYPE_UNSUPPORTED"},
		"a DOCM":                               {fixture: "macros.docm", status: http.StatusUnsupportedMediaType, code: "ATTACHMENT_HAS_MACROS"},
		"a DOC with a Macros storage":          {fixture: "macros.doc", status: http.StatusUnsupportedMediaType, code: "ATTACHMENT_HAS_MACROS"},
		"20 MiB and one byte":                  {data: append(append([]byte("%PDF-1.4\n"), make([]byte, 20<<20-9)...), 0), status: http.StatusRequestEntityTooLarge, code: "MEDIA_TOO_LARGE"},
	} {
		data := c.data
		if data == nil {
			data = attachmentFixture(t, c.fixture)
		}
		if got := upload(teacher, "bai-tap.pdf", data); got.status != c.status || errorCodeOf(got) != c.code {
			t.Errorf("%s: %s, want %d %s", name, answer(got), c.status, c.code)
		}
	}
	empty := teacher.send(http.MethodPost, "/teacher/messages/attachments", &payload{contentType: "multipart/form-data; boundary=x", data: []byte("--x--\r\n")})
	if empty.status != http.StatusBadRequest {
		t.Errorf("a form with no file: %s, want 400", answer(empty))
	}

	word := attachmentFixture(t, "word.docx")
	docx := upload(teacher, "Đề cương ôn tập.docx", word)
	if docx.status != http.StatusCreated || docx.json["filename"] != "Đề cương ôn tập.docx" || docx.json["bytes"] != float64(len(word)) ||
		docx.json["mimeType"] != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Fatalf("a DOCX: %s", answer(docx))
	}
	if doc := upload(teacher, "de-cu.doc", attachmentFixture(t, "word.doc")); doc.status != http.StatusCreated || doc.json["mimeType"] != "application/msword" {
		t.Errorf("a DOC: %s", answer(doc))
	}
	file := docx.json["id"].(string)
	staffURL := "/teacher/messages/attachments/" + file
	studentURL := "/app/messages/attachments/" + file

	fetch := func(c *client, path string) (int, http.Header, map[string]any) {
		t.Helper()
		status, header, raw := c.download(path)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		return status, header, body
	}
	before := time.Now()
	status, header, signed := fetch(teacher, staffURL)
	if status != http.StatusOK || header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("the uploader's URL for their unsent file: %d, Cache-Control %q, %v", status, header.Get("Cache-Control"), signed)
	}
	expires, err := time.Parse(time.RFC3339, signed["expiresAt"].(string))
	if err != nil || expires.Before(before.Add(4*time.Minute)) || expires.After(time.Now().Add(5*time.Minute+time.Second)) {
		t.Errorf("expiresAt %v (%v), want five minutes from now", signed["expiresAt"], err)
	}
	link, err := url.Parse(signed["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	q := link.Query()
	if q.Get("X-Amz-Expires") != "300" || q.Get("response-content-type") != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" ||
		q.Get("response-content-disposition") != "attachment; filename*=UTF-8''%C4%90%E1%BB%81%20c%C6%B0%C6%A1ng%20%C3%B4n%20t%E1%BA%ADp.docx" {
		t.Errorf("the URL signs %v, want five minutes, the sniffed type and the RFC 8187 disposition", q)
	}
	response, err := http.Get(link.String())
	if err != nil {
		t.Fatal("fetching the signed URL failed")
	}
	stored, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	kind, params, err := mime.ParseMediaType(response.Header.Get("Content-Disposition"))
	if response.StatusCode != http.StatusOK || !bytes.Equal(stored, word) || err != nil || kind != "attachment" || params["filename"] != "Đề cương ôn tập.docx" ||
		response.Header.Get("Content-Type") != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Errorf("the object store served %d, %d bytes (same %v), %q, %q", response.StatusCode, len(stored), bytes.Equal(stored, word),
			response.Header.Get("Content-Disposition"), response.Header.Get("Content-Type"))
	}

	missing := "/teacher/messages/attachments/" + uuid.NewString()
	absent, _, _ := fetch(teacher, missing)
	for name, c := range map[string]*client{"another teacher": other, "an admin": admin} {
		if got, _, body := fetch(c, staffURL); got != http.StatusNotFound || absent != http.StatusNotFound {
			t.Errorf("%s fetching an unsent file: %d %v, want 404 as for a missing one (%d)", name, got, body, absent)
		}
	}
	if got, _, _ := fetch(student, studentURL); got != http.StatusNotFound {
		t.Errorf("the student fetching an unsent file: %d, want 404", got)
	}

	thread := "/teacher/messages/students/" + studentID
	sent := teacher.must(http.StatusCreated, http.MethodPost, thread, map[string]any{"clientId": uuid.NewString(), "attachmentId": file})
	if attached, _ := sent["attachment"].(map[string]any); attached == nil || attached["id"] != file || sent["body"] != nil {
		t.Errorf("a post holding the file alone: %v", sent)
	}
	if got, body := teacher.call(http.MethodPost, thread, map[string]any{"clientId": uuid.NewString(), "body": "Gửi lại", "attachmentId": file}); got != http.StatusNotFound {
		t.Errorf("sending the file again: %d %v, want 404", got, body)
	}
	if got, body := other.call(http.MethodPost, "/teacher/messages/classes/"+other.class("Lớp khác "+nonce(t)), map[string]any{"clientId": uuid.NewString(), "attachmentId": file}); got != http.StatusNotFound {
		t.Errorf("another teacher sending the file: %d %v, want 404", got, body)
	}
	read := student.must(http.StatusOK, http.MethodGet, "/app/messages/teachers/"+teacher.me(t), nil)
	messages := read["messages"].([]any)
	if len(messages) != 1 || messages[0].(map[string]any)["attachment"].(map[string]any)["filename"] != "Đề cương ôn tập.docx" {
		t.Errorf("the student's thread: %v, want the file", messages)
	}
	status, header, signed = fetch(student, studentURL)
	if status != http.StatusOK || header.Get("Cache-Control") != "private, no-store" || signed["url"] == "" {
		t.Errorf("the student's URL for the file they were sent: %d %q %v", status, header.Get("Cache-Control"), signed)
	}
	if got, _, _ := fetch(teacher, staffURL); got != http.StatusOK {
		t.Errorf("the teacher's URL for the file they sent: %d", got)
	}
	for name, c := range map[string]*client{"another teacher": other, "an admin, whom scope.all does not let into a direct thread": admin} {
		if got, _, _ := fetch(c, staffURL); got != http.StatusNotFound {
			t.Errorf("%s fetching a direct message's file: %d, want 404", name, got)
		}
	}

	picture := upload(teacher, "lich-thi", attachmentFixture(t, "photo.png"))
	if picture.status != http.StatusCreated || picture.json["filename"] != "lich-thi.png" || picture.json["mimeType"] != "image/png" {
		t.Fatalf("a PNG with no extension: %s", answer(picture))
	}
	teacher.must(http.StatusCreated, http.MethodPost, "/teacher/messages/classes/"+class, map[string]any{
		"clientId": uuid.NewString(), "body": "Lịch thi", "attachmentId": picture.json["id"],
	})
	channelFile := "/teacher/messages/attachments/" + picture.json["id"].(string)
	if got, _, _ := fetch(admin, channelFile); got != http.StatusOK {
		t.Errorf("the Admin fetching an announcement's file: %d, want 200: a channel is class content", got)
	}
	if got, _, _ := fetch(other, channelFile); got != http.StatusNotFound {
		t.Errorf("another teacher fetching an announcement's file: %d, want 404", got)
	}
	if got, _, _ := fetch(student, "/app/messages/attachments/"+picture.json["id"].(string)); got != http.StatusOK {
		t.Errorf("a member fetching an announcement's file: %d, want 200", got)
	}
	if got := upload(student, "bai-lam.pdf", tinyPDF); got.status != http.StatusForbidden {
		t.Errorf("a student uploading: %s, want 403", answer(got))
	}
}
