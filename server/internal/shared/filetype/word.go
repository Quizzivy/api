package filetype

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// ErrMacros is a Word file that carries macros: a DOCM (or a macro-enabled
// template) by its content types, or a DOC whose directory holds a Macros
// storage. Students download what a teacher attaches, so it is refused, and
// the teacher is told to save the file without them.
var ErrMacros = errors.New("filetype: the document carries macros")

// Word tells a Word document in r, which holds size bytes: DOCX, DOC,
// ErrMacros for one that carries macros, or "" for anything else (a ZIP that
// is no Word document, an XLSX, an XLS, a PPT). Of a ZIP it reads the central
// directory, once its headers are counted, and the capped
// [Content_Types].xml; of a compound file, its directory. No other entry or
// stream is opened, and nothing is converted.
func Word(r io.ReaderAt, size int64) (string, error) {
	head := make([]byte, 8)
	if n, _ := r.ReadAt(head, 0); n < len(head) {
		return "", nil
	}
	switch {
	case bytes.Equal(head[:4], []byte("PK\x03\x04")):
		return docx(r, size)
	case bytes.Equal(head, cfbSignature):
		return doc(r, size)
	}
	return "", nil
}

// The bounds of a DOCX's reading: the entries its central directory may
// hold, and the bytes of [Content_Types].xml that are read. A Word document
// holds tens of entries and a content types part of a few kilobytes.
const (
	maxZipEntries   = 10000
	maxContentTypes = 256 << 10
)

const (
	contentTypesPart = "[Content_Types].xml"
	documentPart     = "word/document.xml"

	docxMain = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
)

func docx(r io.ReaderAt, size int64) (string, error) {
	if !zipDirectoryWithin(r, size, maxZipEntries) {
		return "", nil
	}
	archive, err := zip.NewReader(r, size)
	if err != nil {
		return "", nil
	}
	var types *zip.File
	document := false
	for _, f := range archive.File {
		switch {
		case f.Name == contentTypesPart:
			types = f
		case f.Name == documentPart:
			document = true
		case strings.EqualFold(f.Name, "word/vbaProject.bin"):
			return "", ErrMacros
		}
	}
	if types == nil || !document {
		return "", nil
	}
	parsed, ok := readContentTypes(types)
	if !ok {
		return "", nil
	}
	if parsed.macros() {
		return "", ErrMacros
	}
	if parsed.of("/"+documentPart) != docxMain {
		return "", nil
	}
	return DOCX, nil
}

type contentTypes struct {
	Defaults []struct {
		Extension   string `xml:"Extension,attr"`
		ContentType string `xml:"ContentType,attr"`
	} `xml:"Default"`
	Overrides []struct {
		PartName    string `xml:"PartName,attr"`
		ContentType string `xml:"ContentType,attr"`
	} `xml:"Override"`
}

func readContentTypes(f *zip.File) (contentTypes, bool) {
	rc, err := f.Open()
	if err != nil {
		return contentTypes{}, false
	}
	defer rc.Close()
	raw, err := io.ReadAll(io.LimitReader(rc, maxContentTypes+1))
	if err != nil || len(raw) > maxContentTypes {
		return contentTypes{}, false
	}
	var parsed contentTypes
	if err := xml.Unmarshal(raw, &parsed); err != nil {
		return contentTypes{}, false
	}
	return parsed, true
}

// of is the content type of a part, its Override first and then the Default
// of its extension. Part names compare without regard to case, as OPC says.
func (c contentTypes) of(part string) string {
	for _, o := range c.Overrides {
		if strings.EqualFold(o.PartName, part) {
			return o.ContentType
		}
	}
	ext := part[strings.LastIndexByte(part, '.')+1:]
	for _, d := range c.Defaults {
		if strings.EqualFold(d.Extension, ext) {
			return d.ContentType
		}
	}
	return ""
}

// macros is a package any part of which is macro-enabled or a VBA project:
// a DOCM, a DOTM, or a DOCX someone added a project to.
func (c contentTypes) macros() bool {
	carries := func(t string) bool {
		t = strings.ToLower(t)
		return strings.Contains(t, "macroenabled") || strings.Contains(t, "vbaproject")
	}
	for _, o := range c.Overrides {
		if carries(o.ContentType) {
			return true
		}
	}
	for _, d := range c.Defaults {
		if carries(d.ContentType) {
			return true
		}
	}
	return false
}

// centralSignature begins each header of a ZIP's central directory
// (APPNOTE 4.3.12).
var centralSignature = []byte("PK\x01\x02")

// zipDirectoryWithin reports whether r holds at most limit central directory
// headers. archive/zip keeps every header it reads, wherever the end record
// points it, so the bound counts the headers' signature over the whole file:
// every header archive/zip could load begins with one, and the signature
// cannot overlap itself. Compressed data may hold the four bytes by chance,
// which only counts against the generous limit.
func zipDirectoryWithin(r io.ReaderAt, size int64, limit int) bool {
	const chunk = 64 << 10
	keep := len(centralSignature) - 1
	buf := make([]byte, chunk+keep)
	seen, carried := 0, 0
	for offset := int64(0); offset < size; offset += chunk {
		n, err := r.ReadAt(buf[carried:carried+int(min(chunk, size-offset))], offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return false
		}
		window := buf[:carried+n]
		seen += bytes.Count(window, centralSignature)
		if seen > limit {
			return false
		}
		carried = min(keep, len(window))
		copy(buf, window[len(window)-carried:])
	}
	return true
}
