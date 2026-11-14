package ptb

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDecompress(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"literal only", []byte{0x01, 0x00, 'a', 0x04, 0x00, 'z'}, "azzzz"},
		{"mixed", []byte{0x03, 0x00, 'a', 'b', 'c', 0x05, 0x00, 'x', 0x02, 0x00, 'y', 'z'}, "abcxxxxxyz"},
		{"empty", nil, ""},
	}
	for _, c := range cases {
		got, err := decompress(c.in, 1024)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if string(got) != c.want {
			t.Fatalf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	for _, bad := range [][]byte{{0x04, 0x00, 'a'}, {0x00, 0x00, 0x01}} {
		if _, err := decompress(bad, 1024); err == nil {
			t.Fatalf("accepted invalid stream %v", bad)
		}
	}
}

func TestPackedDecimal(t *testing.T) {
	cases := []struct {
		in   []byte
		want int64
	}{
		{[]byte{0x00, 0x12, 0x3d}, -123},
		{[]byte{0x00, 0x12, 0x3c}, 123},
		{[]byte{0x00, 0x12, 0x3f}, 123},
		{[]byte{0x00, 0x00, 0x0c}, 0},
		{[]byte{0x12, 0x34, 0x56, 0x78, 0x90, 0x1d}, -12345678901},
	}
	for _, c := range cases {
		got, err := PackedDecimal(c.in)
		if err != nil {
			t.Fatalf("%x: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("%x: got %d want %d", c.in, got, c.want)
		}
	}
	for _, bad := range [][]byte{{}, {0x00, 0x1a, 0x3c}, {0x00, 0x12, 0x35}} {
		if _, err := PackedDecimal(bad); err == nil {
			t.Fatalf("accepted invalid packed decimal %x", bad)
		}
	}
}

func TestArchiveReadLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	w, err := z.Create("test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err := a.ReadLimit("test", 100); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected limit error, got %v", err)
	}
	if got, err := a.ReadLimit("test", 1024); err != nil || len(got) != 1024 {
		t.Fatalf("read: %d bytes, %v", len(got), err)
	}
}

func TestLimits(t *testing.T) {
	if _, err := PackedDecimal([]byte{0x99, 0x99, 0x99, 0x99, 0x99, 0x99, 0x99, 0x99, 0x99, 0x99, 0x9c}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected decimal overflow, got %v", err)
	}
	if _, err := RecordsLimit(synthetic(), 1, 1000); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected record limit, got %v", err)
	}
	if _, err := RecordsLimit(synthetic(), 100, 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected byte limit, got %v", err)
	}
}

func TestDate(t *testing.T) {
	// 28 February 2022: day, month, then year.
	got, err := Date([]byte{0x1c, 0x02, 0xe6, 0x07})
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2022, 2, 28, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if _, err := Date([]byte{0x01, 0x0d, 0xe6, 0x07}); err == nil {
		t.Fatal("accepted month 13")
	}
	if _, err := Date([]byte{0x1e, 0x02, 0xe6, 0x07}); err == nil {
		t.Fatal("accepted 30 February")
	}
	if _, err := Date([]byte{0x01}); err == nil {
		t.Fatal("accepted short date")
	}
}

func TestText(t *testing.T) {
	if got := Text([]byte("ROTI\x00junk")); got != "ROTI" {
		t.Fatalf("got %q", got)
	}
	if got := Text([]byte{'C', 'a', 'f', 0xe9, 0x00}); got != "Café" {
		t.Fatalf("got %q", got)
	}
}

// synthetic builds a minimal fixed-length Btrieve file: a header, one
// allocation page and one data page with three records.
func synthetic() []byte {
	const pageSize, slot, fixed = 1024, 32, 10
	header := make([]byte, pageSize)
	copy(header, "FC")
	header[43] = pageSize / 256
	binary.LittleEndian.PutUint16(header[22:], fixed)
	binary.LittleEndian.PutUint16(header[24:], slot)
	binary.LittleEndian.PutUint16(header[74:], version)

	pat := make([]byte, pageSize)
	pat[4], pat[5] = 'P', 0
	binary.LittleEndian.PutUint16(pat[0:], 1)  // group
	binary.LittleEndian.PutUint16(pat[6:], 0)  // generation
	binary.LittleEndian.PutUint32(pat[20:], 2) // physical page 2
	pat[24], pat[25] = 'D', 0

	data := make([]byte, pageSize)
	data[4], data[5] = 'D', 0
	binary.LittleEndian.PutUint32(data[0:], 1) // logical record 1
	for i, body := range []string{"AAAAAAAAAA", "BBBBBBBBBB", "CCCCCCCCCC"} {
		data[8+i*slot] = 1 // live-record marker in the first two bytes
		copy(data[8+i*slot+2:], body)
	}
	return append(append(header, pat...), data...)
}

func TestRecordsFixed(t *testing.T) {
	rows, err := Records(synthetic())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d records", len(rows))
	}
	if string(rows[1]) != "BBBBBBBBBB" {
		t.Fatalf("got %q", rows[1])
	}
}

func TestRecordsRejects(t *testing.T) {
	if _, err := Records([]byte("not a btrieve file")); !errors.Is(err, ErrFormat) {
		t.Fatalf("got %v", err)
	}
	masked := synthetic()
	masked[55] = 2
	if _, err := Records(masked); !errors.Is(err, ErrMasked) {
		t.Fatalf("got %v", err)
	}
	truncated := synthetic()
	if _, err := Records(truncated[:len(truncated)-100]); !errors.Is(err, ErrFormat) {
		t.Fatalf("got %v", err)
	}
}
