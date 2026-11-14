package ptb

import "testing"

func TestClassifyRejectsUnknownAndDangling(t *testing.T) {
	header := func(po uint32, module byte, journal uint16) []byte {
		h := make([]byte, 40)
		h[hdrModule] = module
		putU16(h[hdrJournal:], journal)
		putU32(h[hdrPostOrder:], po)
		return h
	}
	row := func(po, account, link uint32) []byte {
		r := make([]byte, rowAmount+rowAmountLen)
		putU32(r[rowAccount:], account)
		putU32(r[rowPostOrder:], po)
		putU32(r[rowLink:], link)
		return r
	}
	if _, _, err := Classify([][]byte{header(1, 'X', 0), row(1, 1, 0)}, nil); err == nil {
		t.Fatal("accepted unknown module")
	}
	if _, _, err := Classify([][]byte{header(1, 'R', 1), row(1, 2000, 0), row(1, 2800, 0)}, [][]byte{row(1, 2000, 99)}); err == nil {
		t.Fatal("accepted dangling settlement")
	}
}

func putU16(b []byte, v uint16) { b[0], b[1] = byte(v), byte(v>>8) }
func putU32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}
