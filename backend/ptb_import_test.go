package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// miniDAT builds a Btrieve data file with one fixed-length record: a header, one allocation page
// and one data page.
func miniDAT() []byte { return miniDATSize(10) }

func miniDATSize(fixed int) []byte {
	slot := 32
	if fixed+2 > slot {
		slot = ((fixed + 17) / 16) * 16
	}
	pageSize := 1024
	for pageSize < slot*4 {
		pageSize += 256
	}
	header := make([]byte, pageSize)
	copy(header, "FC")
	header[43] = byte(pageSize / 256)
	binary.LittleEndian.PutUint16(header[22:], uint16(fixed))
	binary.LittleEndian.PutUint16(header[24:], uint16(slot))
	binary.LittleEndian.PutUint16(header[74:], 0x900)

	// An allocation page spans as many pages as needed to reach 8 KiB, so the data page sits after
	// it. Putting them adjacent would let the allocation scan read the data page as entries.
	span := (8192 + pageSize - 1) / pageSize
	pat := make([]byte, pageSize*span)
	pat[4], pat[5] = 'P', 0
	binary.LittleEndian.PutUint16(pat[0:], 1)
	binary.LittleEndian.PutUint32(pat[20:], uint32(1+span))
	pat[24], pat[25] = 'D', 0

	data := make([]byte, pageSize)
	data[4], data[5] = 'D', 0
	binary.LittleEndian.PutUint32(data[0:], 1)
	data[8] = 1
	copy(data[10:], []byte("1100-cash "))
	return append(append(header, pat...), data...)
}

// zeroBalance fills 12-byte packed-decimal fields with a valid zero (last nibble c).
func zeroBalance(raw []byte, offsets ...int) {
	pageSize := int(raw[43]) * 256
	lastPage := raw[len(raw)-pageSize:]
	for _, off := range offsets {
		// The record body starts after the two live-record bytes, at page offset 8+2.
		for pos := 10 + off; pos+12 <= len(lastPage); pos += 36 {
			lastPage[pos+11] = 0x0c
		}
	}
}

func miniBackup(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range entries {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func requiredTables() map[string][]byte {
	return map[string][]byte{"CHART.DAT": miniDAT(), "CUSTOMER.DAT": miniDAT(), "VENDOR.DAT": miniDAT(), "JRNLHDR.DAT": miniDAT(), "JRNLROW.DAT": miniDAT()}
}

func uploadPTB(t *testing.T, h http.Handler, token, org, name string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("file", name)
	part.Write(data)
	w.Close()
	req := httptest.NewRequest("POST", "/api/v1/imports/ptb?org_id="+org, &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Origin", "http://app.test")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPTBImportUpload(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ptb-owner@example.com")
	viewer := seedSession(t, a, "ptb-viewer@example.com")
	outsider := seedSession(t, a, "ptb-outsider@example.com")
	second := seedSession(t, a, "ptb-second@example.com") // one company per owner unless premier
	org := orgFromList(t, a, owner, `{"name":"PTB co","fiscal_year_end":"12-31"}`)
	other := orgFromList(t, a, second, `{"name":"PTB other","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(t.Context(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='ptb-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	o, oo := itoa(org), itoa(other)
	backup := miniBackup(t, requiredTables())

	// Access and input checks.
	expectStatus(t, uploadPTB(t, h, viewer, o, "backup.ptb", backup), 403)
	expectStatus(t, uploadPTB(t, h, outsider, o, "backup.ptb", backup), 403)
	expectStatus(t, uploadPTB(t, h, owner, o, "notes.txt", backup), 422)
	expectStatus(t, uploadPTB(t, h, owner, o, "backup.ptb", nil), 422)
	expectStatus(t, uploadPTB(t, h, owner, o, "backup.ptb", []byte("not a zip")), 422)
	empty := miniBackup(t, map[string][]byte{"Details.ini": []byte("x")})
	expectStatus(t, uploadPTB(t, h, owner, o, "backup.ptb", empty), 422)
	bad := requiredTables()
	bad["JRNLROW.DAT"] = []byte("corrupt")
	expectStatus(t, uploadPTB(t, h, owner, o, "backup.ptb", miniBackup(t, bad)), 422)
	bad = requiredTables()
	bad["../escape.txt"] = []byte("x")
	expectStatus(t, uploadPTB(t, h, owner, o, "backup.ptb", miniBackup(t, bad)), 422)
	many := map[string][]byte{}
	for i := 0; i <= ptbMaxEntries; i++ {
		many[fmt.Sprintf("f%04d.txt", i)] = []byte("x")
	}
	expectStatus(t, uploadPTB(t, h, owner, o, "backup.ptb", miniBackup(t, many)), 422)

	// Staging.
	res := expectStatus(t, uploadPTB(t, h, owner, o, "backup.ptb", backup), 201)
	if res["records"].(map[string]any)["CHART.DAT"].(float64) != 1 {
		t.Fatalf("unexpected counts: %v", res["records"])
	}
	if res["sha256"].(string) == "" || res["status"] != "staged" {
		t.Fatalf("unexpected response: %v", res)
	}
	id := int64(res["id"].(float64))
	var staged int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM ptb_import_records WHERE import_id=$1`, id).Scan(&staged); err != nil {
		t.Fatal(err)
	}
	if staged != 5 {
		t.Fatalf("staged %d records, want 5", staged)
	}

	// The same file must not stage twice for one company, but another company may import it.
	dup := uploadPTB(t, h, owner, o, "backup.ptb", backup)
	expectStatus(t, dup, 409)
	if got := expectStatus(t, uploadPTB(t, h, second, oo, "backup.ptb", backup), 201); got["id"] == nil {
		t.Fatalf("second company should import: %v", got)
	}
	var imports int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM ptb_imports WHERE org_id=$1`, org).Scan(&imports); err != nil {
		t.Fatal(err)
	}
	if imports != 1 {
		t.Fatalf("got %d imports for the first company, want 1", imports)
	}
}

// A small upload that expands past the per-entry cap must be refused before it is decoded, and the
// temporary working directory must be removed either way.
func TestPTBImportOversizedEntry(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ptb-bomb@example.com")
	org := orgFromList(t, a, owner, `{"name":"PTB bomb","fiscal_year_end":"12-31"}`)

	before, _ := filepath.Glob(filepath.Join(os.TempDir(), "ptb-import-*"))
	entries := requiredTables()
	entries["BIG.DAT"] = make([]byte, ptbMaxEntryBytes+1)
	expectStatus(t, uploadPTB(t, h, owner, itoa(org), "backup.ptb", miniBackup(t, entries)), 422)
	after, _ := filepath.Glob(filepath.Join(os.TempDir(), "ptb-import-*"))
	if len(after) > len(before) {
		t.Fatalf("temporary import directories left behind: %v", after)
	}
}

func TestSafeEntryName(t *testing.T) {
	for name, want := range map[string]bool{
		"CHART.DAT": true, "sub/CHART.DAT": true,
		"": false, "/etc/passwd": false, `..\x.DAT`: false, "../../x": false, `a\b`: false, "C:x": false,
	} {
		if got := safeEntryName(name); got != want {
			t.Fatalf("safeEntryName(%q) = %v, want %v", name, got, want)
		}
	}
}

// Encrypted (masked) tables are refused with a reason rather than silently ignored.
func TestPTBImportSkipsMasked(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ptb-masked@example.com")
	org := orgFromList(t, a, owner, `{"name":"PTB masked","fiscal_year_end":"12-31"}`)

	masked := miniDAT()
	masked[55] = 2
	entries := requiredTables()
	entries["AUDITTR.DAT"] = masked
	backup := miniBackup(t, entries)
	res := expectStatus(t, uploadPTB(t, h, owner, itoa(org), "backup.ptb", backup), 201)
	skipped, _ := res["skipped"].(map[string]any)
	if skipped["AUDITTR.DAT"] == nil {
		t.Fatalf("masked table should be reported as skipped: %v", res)
	}
	var tables int
	if err := pool.QueryRow(t.Context(), `SELECT count(DISTINCT table_name) FROM ptb_import_records`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 5 {
		t.Fatalf("got %d staged tables, want 5", tables)
	}
}

func previewPTB(t *testing.T, h http.Handler, token, org string, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/imports/ptb/"+itoa(id)+"/preview?org_id="+org, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://app.test")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPTBImportPreview(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ptb-preview@example.com")
	viewer := seedSession(t, a, "ptb-preview-viewer@example.com")
	org := orgFromList(t, a, owner, `{"name":"PTB preview","fiscal_year_end":"12-31"}`)
	if _, err := a.db.Exec(t.Context(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='ptb-preview-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	entries := requiredTables()
	chart := miniDATSize(1610)
	zeroBalance(chart, 70, 82, 94)
	entries["CHART.DAT"] = chart
	entries["JRNLHDR.DAT"] = miniDATSize(100)
	entries["JRNLROW.DAT"] = miniDATSize(100)
	customer := miniDATSize(200)
	zeroBalance(customer, 162)
	entries["CUSTOMER.DAT"] = customer
	vendor := miniDATSize(240)
	zeroBalance(vendor, 206)
	entries["VENDOR.DAT"] = vendor
	res := expectStatus(t, uploadPTB(t, h, owner, itoa(org), "backup.ptb", miniBackup(t, entries)), 201)
	id := int64(res["id"].(float64))
	o := itoa(org)

	// A matching preview reports counts, documents and a reconciled panel.
	got := expectStatus(t, previewPTB(t, h, owner, o, id, `{"period":0,"trial_balance_debit":0,"trial_balance_credit":0,"receivables":0,"payables":0}`), 200)
	if got["status"] != "previewed" {
		t.Fatalf("status: %v", got["status"])
	}
	counts := got["counts"].(map[string]any)
	if counts["accounts"].(float64) != 1 || counts["customers"].(float64) != 1 || counts["suppliers"].(float64) != 1 {
		t.Fatalf("counts: %v", counts)
	}
	rec := got["reconciliation"].(map[string]any)
	if rec["matched"] != true {
		t.Fatalf("expected match: %v", rec)
	}

	// One cent of drift is allowed; more is reported and not marked reconciled.
	got = expectStatus(t, previewPTB(t, h, owner, o, id, `{"period":0,"trial_balance_debit":1}`), 200)
	if got["reconciliation"].(map[string]any)["matched"] != true {
		t.Fatalf("one cent should match: %v", got["reconciliation"])
	}
	got = expectStatus(t, previewPTB(t, h, owner, o, id, `{"period":0,"trial_balance_debit":2}`), 200)
	if got["reconciliation"].(map[string]any)["matched"] != false {
		t.Fatalf("two cents should not match: %v", got["reconciliation"])
	}
	if got["reconciliation"].(map[string]any)["differences"].(map[string]any)["trial_balance_debit"].(float64) != -2 {
		t.Fatalf("difference: %v", got["reconciliation"])
	}

	// Access checks.
	expectStatus(t, previewPTB(t, h, viewer, o, id, `{}`), 200)
	foreign := seedSession(t, a, "ptb-preview-foreign@example.com")
	expectStatus(t, previewPTB(t, h, foreign, o, id, `{}`), 403)
	expectStatus(t, previewPTB(t, h, owner, o, id+999, `{}`), 404)
	expectStatus(t, previewPTB(t, h, owner, o, id, `{"period":-1}`), 422)
}

// encodePacked writes a value scaled by 1e9 as a 12-byte Btrieve packed decimal.
func encodePacked(v int64) []byte {
	neg := v < 0
	if neg {
		v = -v
	}
	digits := fmt.Sprintf("%023d", v)
	out := make([]byte, 12)
	for i := 0; i < 23; i++ {
		n := digits[i] - '0'
		if i%2 == 0 {
			out[i/2] = n << 4
		} else {
			out[i/2] |= n
		}
	}
	if neg {
		out[11] |= 0x0d
	} else {
		out[11] |= 0x0c
	}
	return out
}

// miniJournals builds headers and rows for one balanced pair plus an optional second account.
func miniJournals() ([]byte, []byte) {
	header := miniDATSize(200)
	pageSize := int(header[43]) * 256
	last := header[len(header)-pageSize:]
	last[10+10] = 'R'                              // module
	binary.LittleEndian.PutUint16(last[10+7:], 3)  // sales journal
	binary.LittleEndian.PutUint32(last[10+11:], 1) // party
	copy(last[10+15:], []byte{30, 9, 230, 7})      // 30 September 2026
	binary.LittleEndian.PutUint32(last[10+35:], 7) // post order
	copy(last[10+82:], []byte("SALES 1"))

	row := miniDATSize(100)
	pageSize = int(row[43]) * 256
	last = row[len(row)-pageSize:]
	binary.LittleEndian.PutUint32(last[10:], 0)
	binary.LittleEndian.PutUint32(last[10+4:], 7)
	copy(last[10+53:], encodePacked(100_000000000))
	// A second live record on the same document balances the entry.
	slot := ((100 + 17) / 16) * 16
	base := 8 + slot
	last[base] = 1
	binary.LittleEndian.PutUint32(last[base+2:], 0)
	binary.LittleEndian.PutUint32(last[base+2+4:], 7)
	copy(last[base+2+53:], encodePacked(-100_000000000))
	return header, row
}

func commitPTB(t *testing.T, h http.Handler, token, org string, id int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/imports/ptb/"+itoa(id)+"/commit?org_id="+org, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://app.test")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPTBImportCommit(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ptb-commit@example.com")
	org := orgFromList(t, a, owner, `{"name":"PTB commit","fiscal_year_end":"12-31"}`)
	entries := requiredTables()
	chart := miniDATSize(1610)
	zeroBalance(chart, 70, 82, 94)
	binary.LittleEndian.PutUint32(chart[len(chart)-1024+10+66:], 0)
	entries["CHART.DAT"] = chart
	header, row := miniJournals()
	entries["JRNLHDR.DAT"] = header
	entries["JRNLROW.DAT"] = row
	customer := miniDATSize(200)
	copy(customer[len(customer)-1024+10:], []byte("IFC"))
	zeroBalance(customer, 162)
	entries["CUSTOMER.DAT"] = customer
	vendor := miniDATSize(240)
	copy(vendor[len(vendor)-1024+10+37:], []byte("RBPL"))
	zeroBalance(vendor, 206)
	entries["VENDOR.DAT"] = vendor
	res := expectStatus(t, uploadPTB(t, h, owner, itoa(org), "backup.ptb", miniBackup(t, entries)), 201)
	id := int64(res["id"].(float64))
	o := itoa(org)

	// Unreconciled uploads cannot post.
	expectStatus(t, commitPTB(t, h, owner, o, id, `{}`), 409)

	expectStatus(t, previewPTB(t, h, owner, o, id, `{"period":0,"trial_balance_debit":0,"trial_balance_credit":0,"receivables":0,"payables":0}`), 200)

	// A dry run reports the work without writing anything.
	dry := expectStatus(t, commitPTB(t, h, owner, o, id, `{"dry_run":true}`), 200)
	plan := dry["would_create"].(map[string]any)
	if plan["journals"].(float64) != 1 || plan["accounts"].(float64) != 1 {
		t.Fatalf("plan: %v", plan)
	}
	var journals int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM journals WHERE org_id=$1`, org).Scan(&journals); err != nil {
		t.Fatal(err)
	}
	if journals != 0 {
		t.Fatalf("dry run wrote %d journals", journals)
	}

	// The post writes one balanced journal, the chart account, the contacts and an audit event.
	done := expectStatus(t, commitPTB(t, h, owner, o, id, `{}`), 200)
	if done["created"].(float64) != 1 {
		t.Fatalf("commit: %v", done)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM journals WHERE org_id=$1`, org).Scan(&journals); err != nil {
		t.Fatal(err)
	}
	if journals != 1 {
		t.Fatalf("posted %d journals", journals)
	}
	var debit, credit int64
	if err := pool.QueryRow(t.Context(), `SELECT sum(debit_cents), sum(credit_cents) FROM journal_lines WHERE org_id=$1`, org).Scan(&debit, &credit); err != nil {
		t.Fatal(err)
	}
	if debit != 10000 || credit != 10000 {
		t.Fatalf("posted debit %d credit %d", debit, credit)
	}
	var contacts, audits int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM contacts WHERE org_id=$1`, org).Scan(&contacts); err != nil {
		t.Fatal(err)
	}
	if contacts != 2 {
		t.Fatalf("contacts: %d", contacts)
	}
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE org_id=$1 AND action='ptb_import'`, org).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("audit events: %d", audits)
	}

	// A second post is refused and adds nothing.
	expectStatus(t, commitPTB(t, h, owner, o, id, `{}`), 409)
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM journals WHERE org_id=$1`, org).Scan(&journals); err != nil {
		t.Fatal(err)
	}
	if journals != 1 {
		t.Fatalf("repeat post added journals: %d", journals)
	}

	// An unbalanced backup fails the post and leaves no journalling behind.
	bad := map[string][]byte{}
	for name, body := range entries {
		bad[name] = body
	}
	badRow := append([]byte(nil), row...)
	badSlot := ((100 + 17) / 16) * 16
	badRow[len(badRow)-1024+8+badSlot] = 0 // drop the balancing credit
	copy(badRow[len(badRow)-1024+10+53:], encodePacked(55_000000000))
	bad["JRNLROW.DAT"] = badRow
	res = expectStatus(t, uploadPTB(t, h, owner, o, "bad.ptb", miniBackup(t, bad)), 201)
	badID := int64(res["id"].(float64))
	expectStatus(t, previewPTB(t, h, owner, o, badID, `{"period":0,"trial_balance_debit":0}`), 200)
	expectStatus(t, commitPTB(t, h, owner, o, badID, `{}`), 422)
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM journals WHERE org_id=$1`, org).Scan(&journals); err != nil {
		t.Fatal(err)
	}
	if journals != 1 {
		t.Fatalf("failed post left %d journals", journals)
	}
}
