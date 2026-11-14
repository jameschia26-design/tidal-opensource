package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

func TestPDFStructureAndEscaping(t *testing.T) {
	d := newPDF()
	d.line(12, true, "Invoice (1) \\ test")
	d.line(10, false, "Café 中文 ok")
	out := d.Bytes()
	if !bytes.HasPrefix(out, []byte("%PDF-1.4\n")) || !bytes.HasSuffix(out, []byte("%%EOF\n")) {
		t.Fatal("missing PDF header or EOF")
	}
	if !bytes.Contains(out, []byte(`(Invoice \(1\) \\ test)`)) {
		t.Fatal("text not escaped")
	}
	if bytes.Contains(out, []byte("中")) {
		t.Fatal("non-ASCII leaked into content stream")
	}
	// Each xref entry must point at the matching "N 0 obj" header.
	s := string(out)
	xi := strings.LastIndex(s, "\nxref\n") + 1
	fields := strings.Fields(s[xi:])
	count, _ := strconv.Atoi(fields[3])
	for i := 1; i < count; i++ {
		off, _ := strconv.Atoi(fields[5+(i-1)*3])
		want := strconv.Itoa(i) + " 0 obj"
		if !strings.HasPrefix(s[off:], want) {
			t.Fatalf("xref object %d points at wrong offset", i)
		}
	}
}

func TestPDFPaginates(t *testing.T) {
	d := newPDF()
	for i := 0; i < 200; i++ {
		d.line(10, false, "row")
	}
	if len(d.pages) < 2 {
		t.Fatal("long document did not paginate")
	}
}
