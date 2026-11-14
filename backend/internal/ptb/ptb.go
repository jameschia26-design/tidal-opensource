// Package ptb reads Peachtree (Sage) .ptb backups: a ZIP of Btrieve/Pervasive
// data files. It decodes records only; it does not interpret accounting meaning.
//
// Format notes, all verified against a real backup:
//   - Data files start with "FC" and hold a signed 16-bit version at offset 74
//     whose magnitude is 0x900 (Btrieve 9.0).
//   - Page size is byte 43 times 256. Allocation pages carry a page-type marker
//     "P\0" at offset 4, a group at 0 and a generation at 6; the newest
//     generation per group wins. Entries start at offset 20, six bytes each:
//     a 4-byte physical page number and a 2-byte kind ("D\0" data, "V\0"
//     variable).
//   - Header byte 55 of 2 or 3 means masked pages, an XOR scheme this package
//     does not implement. Masked files are rejected rather than guessed at.
package ptb

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"time"
)

// Errors callers are expected to distinguish.
var (
	ErrFormat      = errors.New("ptb: not a Btrieve data file")
	ErrMasked      = errors.New("ptb: file uses masked pages, which are not supported")
	ErrCorrupt     = errors.New("ptb: corrupt record structure")
	ErrTooLarge    = errors.New("ptb: record exceeds the size limit")
	ErrUnsupported = errors.New("ptb: unsupported dictionary type")
)

const (
	version    = 0x900
	maxChain   = 128 << 10 // fragment chain limit per record
	maxLiteral = 65535
)

// Archive is an opened .ptb backup.
type Archive struct {
	z *zip.ReadCloser
}

// Open opens a .ptb backup. Callers must Close it.
func Open(path string) (*Archive, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("ptb: open backup: %w", err)
	}
	return &Archive{z: z}, nil
}

// Close releases the underlying file.
func (a *Archive) Close() error { return a.z.Close() }

// Names lists the entries in the backup in archive order.
func (a *Archive) Names() []string {
	names := make([]string, 0, len(a.z.File))
	for _, f := range a.z.File {
		names = append(names, f.Name)
	}
	return names
}

// Read returns one entry, verifying its checksum.
func (a *Archive) Read(name string) ([]byte, error) {
	return a.ReadLimit(name, 256<<20)
}

// ReadLimit bounds decompression before allocating the entry.
func (a *Archive) ReadLimit(name string, limit int64) ([]byte, error) {
	for _, f := range a.z.File {
		if f.Name != name {
			continue
		}
		if limit < 0 || f.UncompressedSize64 > uint64(limit) {
			return nil, ErrTooLarge
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("ptb: read %s: %w", name, err)
		}
		defer rc.Close()
		body, err := io.ReadAll(io.LimitReader(rc, limit+1))
		if int64(len(body)) > limit {
			return nil, ErrTooLarge
		}
		if err != nil {
			return nil, fmt.Errorf("ptb: read %s: %w", name, err)
		}
		return body, nil
	}
	return nil, fmt.Errorf("ptb: %s not in archive", name)
}

// Records decodes every live record in a Btrieve data file.
func Records(data []byte) ([][]byte, error) {
	return RecordsLimit(data, 500000, 256<<20)
}

// RecordsLimit limits decoded records and their combined payload size.
func RecordsLimit(data []byte, maxRecords, maxBytes int) ([][]byte, error) {
	geom, err := geometry(data)
	if err != nil {
		return nil, err
	}
	dataPages, varPages, err := geom.allocate()
	if err != nil {
		return nil, err
	}
	var out [][]byte
	total := 0
	add := func(body []byte) error {
		if len(out) >= maxRecords || len(body) > maxBytes-total {
			return ErrTooLarge
		}
		total += len(body)
		out = append(out, body)
		return nil
	}
	for _, page := range dataPages {
		if geom.fixed > 0 {
			for pos := 8; pos+geom.slot <= geom.pageSize; pos += geom.slot {
				rec := page[pos : pos+geom.slot]
				if rec[0] == 0 && rec[1] == 0 {
					continue
				}
				if 2+geom.fixed > len(rec) {
					return nil, fmt.Errorf("%w: fixed record overflows slot", ErrCorrupt)
				}
				if err := add(rec[2 : 2+geom.fixed]); err != nil {
					return nil, err
				}
			}
			continue
		}
		for pos := 8; pos+geom.slot <= geom.pageSize; pos += geom.slot {
			rec := page[pos : pos+geom.slot]
			if rec[6] != 1 {
				continue
			}
			body, err := geom.chain(rec, varPages)
			if err != nil {
				return nil, err
			}
			if err := add(body); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

type pageGeometry struct {
	pageSize int
	slot     int
	fixed    int
	span     int
	capacity int
	data     []byte
}

func geometry(data []byte) (*pageGeometry, error) {
	if len(data) < 76 || data[0] != 'F' || data[1] != 'C' {
		return nil, ErrFormat
	}
	if v := int16(binary.LittleEndian.Uint16(data[74:76])); v != version && v != -version {
		return nil, fmt.Errorf("%w: unexpected version 0x%x", ErrFormat, uint16(v))
	}
	g := &pageGeometry{
		pageSize: int(data[43]) * 256,
		slot:     int(binary.LittleEndian.Uint16(data[24:26])),
		fixed:    int(binary.LittleEndian.Uint16(data[22:24])),
		data:     data,
	}
	if g.pageSize < 256 || g.slot < 8 || g.slot > g.pageSize-8 || len(data)%g.pageSize != 0 {
		return nil, fmt.Errorf("%w: page size %d, slot %d, %d bytes", ErrFormat, g.pageSize, g.slot, len(data))
	}
	if data[55] == 2 || data[55] == 3 {
		return nil, ErrMasked
	}
	g.span = (8192 + g.pageSize - 1) / g.pageSize
	if g.span < 1 {
		g.span = 1
	}
	unit := g.pageSize
	if unit < 1024 {
		unit = 1024
	}
	g.capacity = 20*(unit/64) - 4
	return g, nil
}

// pageAt returns up to n pages starting at index i, clamped to the file end.
func (g *pageGeometry) pageAt(i, n int) []byte {
	start, end := i*g.pageSize, (i+n)*g.pageSize
	if start > len(g.data) {
		return nil
	}
	if end > len(g.data) {
		end = len(g.data)
	}
	return g.data[start:end]
}

// allocate resolves allocation pages into data and variable pages.
func (g *pageGeometry) allocate() (dataPages [][]byte, varPages map[uint32][]byte, err error) {
	type patRef struct {
		generation uint16
		index      int
	}
	pats := map[uint16]patRef{}
	for i := 0; i < len(g.data)/g.pageSize; i++ {
		page := g.pageAt(i, g.span)
		if len(page) < 8 || page[4] != 'P' || page[5] != 0 {
			continue
		}
		group := binary.LittleEndian.Uint16(page[0:2])
		generation := binary.LittleEndian.Uint16(page[6:8])
		if prev, ok := pats[group]; !ok || generation > prev.generation {
			pats[group] = patRef{generation, i}
		}
	}
	if len(pats) == 0 {
		return nil, nil, fmt.Errorf("%w: no allocation pages", ErrCorrupt)
	}
	varPages = map[uint32][]byte{}
	pages := len(g.data) / g.pageSize
	groups := make([]int, 0, len(pats))
	for group := range pats {
		groups = append(groups, int(group))
	}
	sort.Ints(groups)
	for _, groupNumber := range groups {
		group := uint16(groupNumber)
		ref := pats[group]
		pat := g.pageAt(ref.index, g.span)
		limit := 20 + g.capacity*6
		for pos := 20; pos+6 <= limit && pos+6 <= len(pat); pos += 6 {
			physical := binary.LittleEndian.Uint32(pat[pos : pos+4])
			kind := pat[pos+4 : pos+6]
			if (kind[0] != 'D' && kind[0] != 'V') || kind[1] != 0 {
				continue
			}
			if uint64(physical) >= uint64(pages) {
				return nil, nil, fmt.Errorf("%w: allocation page out of range", ErrCorrupt)
			}
			page := g.pageAt(int(physical), 1)
			logical := uint32(int(group)-1)*uint32(g.capacity) + uint32((pos-20)/6) + 1
			if page[4] != kind[0] || page[5] != kind[1] || binary.LittleEndian.Uint32(page[0:4]) != logical {
				return nil, nil, fmt.Errorf("%w: allocation page mismatch at group %d", ErrCorrupt, group)
			}
			if kind[0] == 'D' {
				dataPages = append(dataPages, page)
				continue
			}
			key := binary.LittleEndian.Uint32(page[0:4])
			if _, dup := varPages[key]; dup {
				return nil, nil, fmt.Errorf("%w: duplicate variable page %d", ErrCorrupt, key)
			}
			varPages[key] = page
		}
	}
	return dataPages, varPages, nil
}

// chain walks a record's fragment list and decompresses the result.
func (g *pageGeometry) chain(rec []byte, varPages map[uint32][]byte) ([]byte, error) {
	logical, index := binary.LittleEndian.Uint32(rec[0:4]), binary.LittleEndian.Uint16(rec[4:6])
	var chain []byte
	seen := map[[2]uint32]bool{}
	for logical != 0xffffffff {
		key := [2]uint32{logical, uint32(index)}
		if seen[key] || int(index) >= g.pageSize/2-1 {
			return nil, fmt.Errorf("%w: invalid fragment pointer", ErrCorrupt)
		}
		seen[key] = true
		tail, ok := varPages[logical]
		if !ok {
			return nil, fmt.Errorf("%w: missing variable page %d", ErrCorrupt, logical)
		}
		start := int(binary.LittleEndian.Uint16(tail[g.pageSize-2-int(index)*2:]) & 0x7fff)
		endIndex := int(index) + 1
		for endIndex < g.pageSize/2 && binary.LittleEndian.Uint16(tail[g.pageSize-2-endIndex*2:]) == 0xffff {
			endIndex++
		}
		if endIndex >= g.pageSize/2 {
			return nil, fmt.Errorf("%w: missing fragment boundary", ErrCorrupt)
		}
		end := int(binary.LittleEndian.Uint16(tail[g.pageSize-2-endIndex*2:]) & 0x7fff)
		if start < 16 || end > g.pageSize || start >= end || end-start < 6 {
			return nil, fmt.Errorf("%w: invalid fragment bounds", ErrCorrupt)
		}
		fragment := tail[start:end]
		logical, index = binary.LittleEndian.Uint32(fragment[0:4]), binary.LittleEndian.Uint16(fragment[4:6])
		chain = append(chain, fragment[6:]...)
		if len(chain) > maxChain {
			return nil, fmt.Errorf("%w: fragment chain too long", ErrTooLarge)
		}
	}
	return decompress(chain, maxChain)
}

// decompress expands the literal-and-run scheme Btrieve uses for variable
// records: a 2-byte length then that many literal bytes, then a 2-byte repeat
// count and one byte to repeat.
func decompress(data []byte, maximum int) ([]byte, error) {
	out := make([]byte, 0, len(data))
	for pos := 0; pos < len(data); {
		if pos+2 > len(data) {
			return nil, fmt.Errorf("%w: truncated literal length", ErrCorrupt)
		}
		size := int(binary.LittleEndian.Uint16(data[pos:]))
		pos += 2
		if pos+size > len(data) || len(out)+size > maximum {
			return nil, fmt.Errorf("%w: invalid literal length", ErrCorrupt)
		}
		out = append(out, data[pos:pos+size]...)
		pos += size
		if pos == len(data) {
			break
		}
		if pos+3 > len(data) {
			return nil, fmt.Errorf("%w: truncated run", ErrCorrupt)
		}
		count := int(binary.LittleEndian.Uint16(data[pos:]))
		value := data[pos+2]
		pos += 3
		if len(out)+count > maximum {
			return nil, fmt.Errorf("%w: invalid run length", ErrCorrupt)
		}
		for i := 0; i < count; i++ {
			out = append(out, value)
		}
	}
	return out, nil
}

// PackedDecimal decodes a Btrieve packed decimal, scaled by 1e9, matching the
// way Peachtree stores money (scale 9). The final nibble carries the sign:
// c and f positive, d negative.
func PackedDecimal(b []byte) (int64, error) {
	if len(b) == 0 {
		return 0, fmt.Errorf("%w: empty packed decimal", ErrCorrupt)
	}
	var value int64
	for i, by := range b {
		hi, lo := by>>4, by&0x0f
		if i == len(b)-1 {
			if hi > 9 || (lo != 0x0c && lo != 0x0d && lo != 0x0f) {
				return 0, fmt.Errorf("%w: bad packed decimal sign", ErrCorrupt)
			}
			if value > (math.MaxInt64-int64(hi))/10 {
				return 0, fmt.Errorf("%w: packed decimal overflow", ErrTooLarge)
			}
			value = value*10 + int64(hi)
			if lo == 0x0d {
				value = -value
			}
			continue
		}
		if hi > 9 || lo > 9 {
			return 0, fmt.Errorf("%w: bad packed decimal digit", ErrCorrupt)
		}
		digits := int64(hi)*10 + int64(lo)
		if value > (math.MaxInt64-digits)/100 {
			return 0, fmt.Errorf("%w: packed decimal overflow", ErrTooLarge)
		}
		value = value*100 + digits
	}
	return value, nil
}

// Date decodes a Btrieve date: day, month, then a 16-bit year.
func Date(b []byte) (time.Time, error) {
	if len(b) < 4 {
		return time.Time{}, fmt.Errorf("%w: short date", ErrCorrupt)
	}
	year := int(binary.LittleEndian.Uint16(b[2:4]))
	day, month := int(b[0]), int(b[1])
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if t.Year() != year || int(t.Month()) != month || t.Day() != day {
		return time.Time{}, fmt.Errorf("%w: invalid date %d-%d-%d", ErrCorrupt, day, month, year)
	}
	return t, nil
}

// Text returns a NUL-terminated string. Bytes map one-to-one to runes, which
// matches Windows-1252 for the ASCII range these files use; the 0x80-0x9F
// block would need a table if a company name ever uses it.
func Text(b []byte) string {
	for i, c := range b {
		if c == 0 {
			b = b[:i]
			break
		}
	}
	runes := make([]rune, len(b))
	for i, c := range b {
		runes[i] = rune(c)
	}
	return string(runes)
}
