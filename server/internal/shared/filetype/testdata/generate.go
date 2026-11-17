//go:build ignore

// generate writes the fixtures the filetype tests, the messaging attachment
// tests and the end-to-end attachment flow read: small files of each kind an
// attachment may be, and of the kinds it is refused. Run it from this
// directory with `go run generate.go`; the output is checked in.
package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"unicode/utf16"
)

func main() {
	write("word.docx", zipOf(docxParts(docxMain)))
	write("macros.docm", zipOf(append(docxParts("application/vnd.ms-word.document.macroEnabled.main+xml"),
		part{"word/vbaProject.bin", "\x00VBA"})))
	write("sheet.xlsx", zipOf([]part{
		{"[Content_Types].xml", `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/></Types>`},
		{"xl/workbook.xml", `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"/>`},
	}))
	write("plain.zip", zipOf([]part{{"bai-tap/readme.txt", "Bài tập tuần 3"}}))

	write("word.doc", compound([]entry{{name: "WordDocument", kind: stream}, {name: "\x01CompObj", kind: stream}}))
	write("macros.doc", compound([]entry{{name: "WordDocument", kind: stream}, {name: "Macros", kind: storage}}))
	write("sheet.xls", compound([]entry{{name: "Workbook", kind: stream}, {name: "\x05SummaryInformation", kind: stream}}))
	write("embedded-word.xls", compound([]entry{{name: "Workbook", kind: stream}, {name: "ObjectPool", kind: storage, children: []entry{{name: "WordDocument", kind: stream}}}}))

	write("program.exe", append([]byte("MZ\x90\x00\x03\x00\x00\x00\x04\x00\x00\x00\xff\xff\x00\x00"), []byte("This program cannot be run in DOS mode.\r\n")...))
	write("document.pdf", []byte("%PDF-1.4\n1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n2 0 obj << /Type /Pages /Kids [] /Count 0 >> endobj\ntrailer << /Root 1 0 R >>\n%%EOF\n"))
	write("huge.png", pngHeader(50000, 50000))
	write("photo.png", encoded(func(b *bytes.Buffer, m image.Image) error { return png.Encode(b, m) }))
	write("photo.jpg", encoded(func(b *bytes.Buffer, m image.Image) error { return jpeg.Encode(b, m, nil) }))
	// A 1 × 1 lossless WebP.
	write("photo.webp", []byte("RIFF\x1a\x00\x00\x00WEBPVP8L\x0d\x00\x00\x00\x2f\x00\x00\x00\x10\x07\x10\x11\x11\x88\x88\xfe\x07\x00"))
}

func write(name string, data []byte) {
	if err := os.WriteFile(name, data, 0o644); err != nil {
		panic(err)
	}
}

const docxMain = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"

type part struct{ name, body string }

func docxParts(main string) []part {
	return []part{
		{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="` + main + `"/></Types>`},
		{"_rels/.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="r1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
		{"word/document.xml", `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Bài tập về nhà</w:t></w:r></w:p></w:body></w:document>`},
	}
}

func zipOf(parts []part) []byte {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, p := range parts {
		f, err := w.Create(p.name)
		if err != nil {
			panic(err)
		}
		if _, err := f.Write([]byte(p.body)); err != nil {
			panic(err)
		}
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return b.Bytes()
}

const (
	storage = 1
	stream  = 2
	root    = 5

	noStream   = 0xFFFFFFFF
	endOfChain = 0xFFFFFFFE
	fatSector  = 0xFFFFFFFD
	free       = 0xFFFFFFFF
)

type entry struct {
	name     string
	kind     byte
	children []entry
}

// compound writes a version 3 compound file (512-byte sectors) whose root
// holds entries, each storage's children hanging below it. Sector 0 is the
// FAT, the directory follows from sector 1, and every stream is empty, so no
// data sector is needed. Siblings are chained through their right sibling.
func compound(entries []entry) []byte {
	var flat [][]byte
	var add func(e entry) uint32
	chain := func(list []entry) uint32 {
		first := uint32(noStream)
		var prev int = -1
		for _, e := range list {
			id := add(e)
			if prev < 0 {
				first = id
			} else {
				binary.LittleEndian.PutUint32(flat[prev][0x48:], id)
			}
			prev = int(id)
		}
		return first
	}
	add = func(e entry) uint32 {
		id := uint32(len(flat))
		flat = append(flat, dirEntry(e.name, e.kind))
		child := chain(e.children)
		binary.LittleEndian.PutUint32(flat[id][0x4C:], child)
		return id
	}
	flat = append(flat, dirEntry("Root Entry", root))
	binary.LittleEndian.PutUint32(flat[0][0x4C:], chain(entries))

	for len(flat)%4 != 0 {
		flat = append(flat, make([]byte, 128))
	}
	dirSectors := len(flat) / 4

	header := make([]byte, 512)
	copy(header, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
	binary.LittleEndian.PutUint16(header[0x18:], 0x3E)
	binary.LittleEndian.PutUint16(header[0x1A:], 3)
	binary.LittleEndian.PutUint16(header[0x1C:], 0xFFFE)
	binary.LittleEndian.PutUint16(header[0x1E:], 9)
	binary.LittleEndian.PutUint16(header[0x20:], 6)
	binary.LittleEndian.PutUint32(header[0x2C:], 1)
	binary.LittleEndian.PutUint32(header[0x30:], 1)
	binary.LittleEndian.PutUint32(header[0x38:], 4096)
	binary.LittleEndian.PutUint32(header[0x3C:], endOfChain)
	binary.LittleEndian.PutUint32(header[0x44:], endOfChain)
	for i := 0; i < 109; i++ {
		binary.LittleEndian.PutUint32(header[0x4C+4*i:], free)
	}
	binary.LittleEndian.PutUint32(header[0x4C:], 0)

	fat := make([]byte, 512)
	for i := 0; i < 128; i++ {
		binary.LittleEndian.PutUint32(fat[4*i:], free)
	}
	binary.LittleEndian.PutUint32(fat[0:], fatSector)
	for s := 1; s <= dirSectors; s++ {
		next := uint32(s + 1)
		if s == dirSectors {
			next = endOfChain
		}
		binary.LittleEndian.PutUint32(fat[4*s:], next)
	}

	out := append(header, fat...)
	for _, e := range flat {
		out = append(out, e...)
	}
	return out
}

func dirEntry(name string, kind byte) []byte {
	b := make([]byte, 128)
	units := utf16.Encode([]rune(name))
	for i, u := range units {
		binary.LittleEndian.PutUint16(b[2*i:], u)
	}
	binary.LittleEndian.PutUint16(b[0x40:], uint16(2*len(units)+2))
	b[0x42] = kind
	b[0x43] = 1
	binary.LittleEndian.PutUint32(b[0x44:], noStream)
	binary.LittleEndian.PutUint32(b[0x48:], noStream)
	binary.LittleEndian.PutUint32(b[0x4C:], noStream)
	binary.LittleEndian.PutUint32(b[0x74:], endOfChain)
	return b
}

// pngHeader is a PNG's signature, an IHDR declaring width × height and an
// IEND: enough for a header read, with no pixel to decode.
func pngHeader(width, height uint32) []byte {
	chunk := func(kind string, data []byte) []byte {
		out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
		out = append(out, kind...)
		out = append(out, data...)
		return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(kind), data...)))
	}
	ihdr := binary.BigEndian.AppendUint32(nil, width)
	ihdr = binary.BigEndian.AppendUint32(ihdr, height)
	ihdr = append(ihdr, 8, 6, 0, 0, 0)
	out := []byte("\x89PNG\r\n\x1a\n")
	out = append(out, chunk("IHDR", ihdr)...)
	return append(out, chunk("IEND", nil)...)
}

func encoded(encode func(*bytes.Buffer, image.Image) error) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.RGBA{R: 200, G: 40, B: 40, A: 255})
	var b bytes.Buffer
	if err := encode(&b, img); err != nil {
		panic(err)
	}
	return b.Bytes()
}
