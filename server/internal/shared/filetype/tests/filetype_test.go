package filetype_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"quizzivy/internal/shared/filetype"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func head(data []byte) []byte { return data[:min(len(data), filetype.HeadSize)] }

func TestSniffNamesTheImagesAndPDFByTheirMagicBytes(t *testing.T) {
	cases := []struct {
		file, image, sniffed string
	}{
		{"photo.png", filetype.PNG, filetype.PNG},
		{"photo.jpg", filetype.JPEG, filetype.JPEG},
		{"photo.webp", filetype.WebP, filetype.WebP},
		{"huge.png", filetype.PNG, filetype.PNG},
		{"document.pdf", "", filetype.PDF},
		{"program.exe", "", ""},
		{"word.docx", "", ""},
		{"word.doc", "", ""},
	}
	for _, c := range cases {
		data := fixture(t, c.file)
		if got := filetype.Image(head(data)); got != c.image {
			t.Errorf("Image(%s) = %q, want %q", c.file, got, c.image)
		}
		if got := filetype.Sniff(head(data)); got != c.sniffed {
			t.Errorf("Sniff(%s) = %q, want %q", c.file, got, c.sniffed)
		}
	}
	if got := filetype.Sniff([]byte(" %PDF-1.4")); got != "" {
		t.Errorf("a PDF signature past offset 0 sniffs as %q, want none", got)
	}
	if got := filetype.Image([]byte("%PDF-1.7\n")); got != "" {
		t.Errorf("a PDF sniffs as the image %q: media's allowlist must stay images", got)
	}
}

func TestDimensionsReadsTheHeaderAndRefusesASideOverTheCap(t *testing.T) {
	width, height, err := filetype.Dimensions(bytes.NewReader(fixture(t, "photo.png")), filetype.PNG, 10000)
	if err != nil || width != 4 || height != 3 {
		t.Errorf("a 4 × 3 PNG: %d × %d, %v", width, height, err)
	}
	width, height, err = filetype.Dimensions(bytes.NewReader(fixture(t, "photo.jpg")), filetype.JPEG, 10000)
	if err != nil || width != 4 || height != 3 {
		t.Errorf("a 4 × 3 JPEG: %d × %d, %v", width, height, err)
	}
	// huge.png is a signature, an IHDR and an IEND: there is no pixel to
	// decode, so the answer can only come from the header.
	if _, _, err := filetype.Dimensions(bytes.NewReader(fixture(t, "huge.png")), filetype.PNG, 10000); !errors.Is(err, filetype.ErrDimensions) {
		t.Errorf("a PNG header declaring 50000 × 50000: %v, want ErrDimensions", err)
	}
	if _, _, err := filetype.Dimensions(bytes.NewReader(fixture(t, "photo.png")), filetype.PNG, 3); !errors.Is(err, filetype.ErrDimensions) {
		t.Errorf("a 4-pixel side over a cap of 3: %v, want ErrDimensions", err)
	}
	if _, _, err := filetype.Dimensions(bytes.NewReader([]byte("\x89PNG\r\n\x1a\nbroken")), filetype.PNG, 10000); !errors.Is(err, filetype.ErrUnreadable) {
		t.Errorf("a cut PNG header: %v, want ErrUnreadable", err)
	}
	if _, _, err := filetype.Dimensions(bytes.NewReader(fixture(t, "photo.webp")), filetype.WebP, 10000); !errors.Is(err, filetype.ErrUnreadable) {
		t.Errorf("a WebP: %v, want ErrUnreadable, the standard library has no WebP decoder", err)
	}
}

func word(t *testing.T, data []byte) (string, error) {
	t.Helper()
	return filetype.Word(bytes.NewReader(data), int64(len(data)))
}

func TestWordTellsADocxAndADocFromOtherContainers(t *testing.T) {
	cases := []struct {
		file string
		want string
		err  error
	}{
		{"word.docx", filetype.DOCX, nil},
		{"word.doc", filetype.DOC, nil},
		{"macros.docm", "", filetype.ErrMacros},
		{"macros.doc", "", filetype.ErrMacros},
		{"plain.zip", "", nil},
		{"sheet.xlsx", "", nil},
		{"sheet.xls", "", nil},
		{"embedded-word.xls", "", nil},
		{"document.pdf", "", nil},
		{"program.exe", "", nil},
		{"photo.png", "", nil},
	}
	for _, c := range cases {
		got, err := word(t, fixture(t, c.file))
		if got != c.want || !errors.Is(err, c.err) || (c.err == nil && err != nil) {
			t.Errorf("Word(%s) = %q, %v, want %q, %v", c.file, got, err, c.want, c.err)
		}
	}
}

func zipOf(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, body := range parts {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

const (
	typesOpen = `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`
	docxMain  = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
)

func TestWordReadsTheContentTypesOfTheDocumentPart(t *testing.T) {
	cases := []struct {
		name  string
		parts map[string]string
		want  string
		err   error
	}{
		{"the main type by its Default", map[string]string{
			"[Content_Types].xml": typesOpen + `<Default Extension="XML" ContentType="` + docxMain + `"/></Types>`,
			"word/document.xml":   "<w:document/>",
		}, filetype.DOCX, nil},
		{"an Override naming the part in another case", map[string]string{
			"[Content_Types].xml": typesOpen + `<Default Extension="xml" ContentType="application/xml"/><Override PartName="/WORD/Document.xml" ContentType="` + docxMain + `"/></Types>`,
			"word/document.xml":   "<w:document/>",
		}, filetype.DOCX, nil},
		{"a template", map[string]string{
			"[Content_Types].xml": typesOpen + `<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.template.main+xml"/></Types>`,
			"word/document.xml":   "<w:document/>",
		}, "", nil},
		{"a macro-enabled template", map[string]string{
			"[Content_Types].xml": typesOpen + `<Override PartName="/word/document.xml" ContentType="application/vnd.ms-word.template.macroEnabledTemplate.main+xml"/></Types>`,
			"word/document.xml":   "<w:document/>",
		}, "", filetype.ErrMacros},
		{"a DOCX carrying a VBA project's content type", map[string]string{
			"[Content_Types].xml": typesOpen + `<Default Extension="bin" ContentType="application/vnd.ms-office.vbaProject"/><Override PartName="/word/document.xml" ContentType="` + docxMain + `"/></Types>`,
			"word/document.xml":   "<w:document/>",
		}, "", filetype.ErrMacros},
		{"a DOCX carrying a VBA project's part", map[string]string{
			"[Content_Types].xml": typesOpen + `<Override PartName="/word/document.xml" ContentType="` + docxMain + `"/></Types>`,
			"word/document.xml":   "<w:document/>",
			"word/vbaProject.bin": "\x00",
		}, "", filetype.ErrMacros},
		{"no document part", map[string]string{
			"[Content_Types].xml": typesOpen + `<Override PartName="/word/document.xml" ContentType="` + docxMain + `"/></Types>`,
		}, "", nil},
		{"no content types", map[string]string{"word/document.xml": "<w:document/>"}, "", nil},
		{"content types over the cap", map[string]string{
			"[Content_Types].xml": typesOpen + `<Override PartName="/word/document.xml" ContentType="` + docxMain + `"/>` + strings.Repeat(`<Default Extension="x" ContentType="a/b"/>`, 8000) + `</Types>`,
			"word/document.xml":   "<w:document/>",
		}, "", nil},
		{"content types that are no XML", map[string]string{
			"[Content_Types].xml": "<Types",
			"word/document.xml":   "<w:document/>",
		}, "", nil},
	}
	for _, c := range cases {
		got, err := word(t, zipOf(t, c.parts))
		if got != c.want || !errors.Is(err, c.err) || (c.err == nil && err != nil) {
			t.Errorf("%s: %q, %v, want %q, %v", c.name, got, err, c.want, c.err)
		}
	}
}

func TestWordRefusesAZipWhoseDirectoryHoldsTooManyEntries(t *testing.T) {
	parts := map[string]string{
		"[Content_Types].xml": typesOpen + `<Override PartName="/word/document.xml" ContentType="` + docxMain + `"/></Types>`,
		"word/document.xml":   "<w:document/>",
	}
	for i := range 10001 {
		parts["word/media/p"+strconv.Itoa(i)+".png"] = ""
	}
	if got, err := word(t, zipOf(t, parts)); got != "" || err != nil {
		t.Errorf("a DOCX of 10003 entries: %q, %v, want none: the directory is bounded before archive/zip loads it", got, err)
	}
}

func TestWordSurvivesACutOrLoopingCompoundFile(t *testing.T) {
	doc := fixture(t, "word.doc")
	for _, cut := range []int{8, 100, 512, 600, 1024} {
		if got, err := word(t, doc[:cut]); got != "" || err != nil {
			t.Errorf("a DOC cut at %d bytes: %q, %v, want none", cut, got, err)
		}
	}
	looping := bytes.Clone(doc)
	// The FAT is sector 0, at 512; entry 1 is the directory's sector. Point
	// it at itself.
	looping[512+4], looping[512+5], looping[512+6], looping[512+7] = 1, 0, 0, 0
	if got, err := word(t, looping); got != "" || err != nil {
		t.Errorf("a directory chain that loops: %q, %v, want none", got, err)
	}
	cyclic := bytes.Clone(doc)
	// The WordDocument entry (1) names itself as its right sibling.
	at := 1024 + 128 + 0x48
	cyclic[at], cyclic[at+1], cyclic[at+2], cyclic[at+3] = 1, 0, 0, 0
	if got, err := word(t, cyclic); got != filetype.DOC || err != nil {
		t.Errorf("a sibling tree that loops: %q, %v, want DOC, each entry visited once", got, err)
	}
}
