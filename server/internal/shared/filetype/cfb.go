package filetype

import (
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"unicode/utf16"
)

// A compound file ([MS-CFB]) is how Word 97-2003 stores a DOC, and Excel and
// PowerPoint an XLS and a PPT, under the same signature. What tells them
// apart is the directory: a DOC holds a WordDocument stream beside its root,
// an XLS a Workbook, a PPT a "PowerPoint Document". The small reader here
// reads the header, the FAT and the directory's sectors, and no stream.

var cfbSignature = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

const (
	cfbHeaderBytes = 512
	cfbHeaderDIFAT = 109
	cfbEntryBytes  = 128

	cfbMaxRegular = 0xFFFFFFFA
	cfbEndOfChain = 0xFFFFFFFE
	cfbNoStream   = 0xFFFFFFFF

	cfbStorage = 1
	cfbStream  = 2
	cfbRoot    = 5

	// cfbMaxEntries bounds the directory read: a Word document holds tens
	// of entries.
	cfbMaxEntries = 1 << 16
)

func doc(r io.ReaderAt, size int64) (string, error) {
	c, ok := openCFB(r, size)
	if !ok {
		return "", nil
	}
	entries, ok := c.directory()
	if !ok {
		return "", nil
	}
	word, macros := false, false
	for _, e := range rootChildren(entries) {
		switch {
		case e.kind == cfbStream && strings.EqualFold(e.name, "WordDocument"):
			word = true
		case e.kind == cfbStorage && strings.EqualFold(e.name, "Macros"):
			macros = true
		}
	}
	switch {
	case !word:
		return "", nil
	case macros:
		return "", ErrMacros
	}
	return DOC, nil
}

type cfb struct {
	r        io.ReaderAt
	shift    uint
	sectors  uint32
	fat      []uint32
	firstDir uint32
}

func le32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }

// openCFB reads the header and the FAT, or answers false for anything a
// well-formed compound file would not hold.
func openCFB(r io.ReaderAt, size int64) (*cfb, bool) {
	header := make([]byte, cfbHeaderBytes)
	if _, err := r.ReadAt(header, 0); err != nil {
		return nil, false
	}
	shift := uint(binary.LittleEndian.Uint16(header[0x1E:]))
	if binary.LittleEndian.Uint16(header[0x1C:]) != 0xFFFE || (shift != 9 && shift != 12) {
		return nil, false
	}
	sectorBytes := int64(1) << shift
	count := (size + sectorBytes - 1) / sectorBytes
	if count < 2 {
		return nil, false
	}
	c := &cfb{r: r, shift: shift, sectors: uint32(count - 1), firstDir: le32(header[0x30:])}
	ids, ok := c.fatSectors(header)
	if !ok {
		return nil, false
	}
	c.fat = make([]uint32, 0, len(ids)*int(sectorBytes/4))
	for _, id := range ids {
		sector, ok := c.sector(id)
		if !ok {
			return nil, false
		}
		for i := 0; i+4 <= len(sector); i += 4 {
			c.fat = append(c.fat, le32(sector[i:]))
		}
	}
	return c, true
}

// fatSectors lists the FAT's sectors: the header's first 109, then the DIFAT
// chain's.
func (c *cfb) fatSectors(header []byte) ([]uint32, bool) {
	total := le32(header[0x2C:])
	if total == 0 || total > c.sectors {
		return nil, false
	}
	ids := make([]uint32, 0, total)
	for i := 0; i < cfbHeaderDIFAT && uint32(len(ids)) < total; i++ {
		ids = append(ids, le32(header[0x4C+4*i:]))
	}
	next, chain := le32(header[0x44:]), le32(header[0x48:])
	for step := uint32(0); uint32(len(ids)) < total; step++ {
		if step >= chain {
			return nil, false
		}
		sector, ok := c.sector(next)
		if !ok {
			return nil, false
		}
		last := len(sector) - 4
		for i := 0; i < last && uint32(len(ids)) < total; i += 4 {
			ids = append(ids, le32(sector[i:]))
		}
		next = le32(sector[last:])
	}
	return ids, true
}

// sector reads sector id, which follows the header's own sector. A last
// sector cut short reads as zeros past the end.
func (c *cfb) sector(id uint32) ([]byte, bool) {
	if id > cfbMaxRegular || id >= c.sectors {
		return nil, false
	}
	buf := make([]byte, 1<<c.shift)
	n, err := c.r.ReadAt(buf, (int64(id)+1)<<c.shift)
	if n == 0 || (err != nil && !errors.Is(err, io.EOF)) {
		return nil, false
	}
	return buf, true
}

type cfbEntry struct {
	name               string
	kind               byte
	left, right, child uint32
}

// directory reads every entry of the directory's chain, at most
// cfbMaxEntries and one visit a sector, so a chain that loops ends.
func (c *cfb) directory() ([]cfbEntry, bool) {
	var entries []cfbEntry
	id := c.firstDir
	for steps := 0; id != cfbEndOfChain; steps++ {
		if steps >= len(c.fat) || int(id) >= len(c.fat) {
			return nil, false
		}
		sector, ok := c.sector(id)
		if !ok {
			return nil, false
		}
		for off := 0; off+cfbEntryBytes <= len(sector); off += cfbEntryBytes {
			entries = append(entries, entryOf(sector[off:off+cfbEntryBytes]))
		}
		if len(entries) > cfbMaxEntries {
			return nil, false
		}
		id = c.fat[id]
	}
	return entries, len(entries) > 0
}

func entryOf(b []byte) cfbEntry {
	e := cfbEntry{kind: b[0x42], left: le32(b[0x44:]), right: le32(b[0x48:]), child: le32(b[0x4C:])}
	length := int(binary.LittleEndian.Uint16(b[0x40:]))
	if length < 2 || length > 64 || length%2 != 0 {
		return e
	}
	units := make([]uint16, 0, length/2-1)
	for i := 0; i+2 < length; i += 2 {
		units = append(units, binary.LittleEndian.Uint16(b[i:]))
	}
	e.name = string(utf16.Decode(units))
	return e
}

// rootChildren is the entries directly under the root storage: the tree of
// siblings that hangs from the root's child, each visited once. A stream
// inside a nested storage, such as a Word document embedded in a workbook,
// is not one of them.
func rootChildren(entries []cfbEntry) []cfbEntry {
	if entries[0].kind != cfbRoot {
		return nil
	}
	var out []cfbEntry
	seen := make([]bool, len(entries))
	seen[0] = true
	stack := []uint32{entries[0].child}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == cfbNoStream || int(id) >= len(entries) || seen[id] {
			continue
		}
		seen[id] = true
		e := entries[id]
		out = append(out, e)
		stack = append(stack, e.left, e.right)
	}
	return out
}
