package main

import (
	"encoding/binary"
	"testing"
)

// TestPTBRowExceptions proves the review list names the record that failed, not just the table:
// every problem carries its position in the decoded table.
func TestPTBRowExceptions(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ptb-ex@example.com")
	org := orgFromList(t, a, owner, `{"name":"PTB exceptions","fiscal_year_end":"12-31"}`)

	entries := sizedTables()
	// A journal row that points at a document that does not exist must be reported with its position.
	row := miniDATSize(100)
	page := row[len(row)-1024:]
	page[8] = 1
	binary.LittleEndian.PutUint32(page[10+4:], 4242) // post order with no header
	entries["JRNLROW.DAT"] = row
	res := expectStatus(t, uploadPTB(t, h, owner, itoa(org), "backup.ptb", miniBackup(t, entries)), 201)
	id := int64(res["id"].(float64))

	got := expectStatus(t, previewPTB(t, h, owner, itoa(org), id, `{"period":0,"receivables":0}`), 200)
	found := false
	for _, e := range got["exceptions"].([]any) {
		entry := e.(map[string]any)
		if entry["ordinal"] == nil {
			continue
		}
		if entry["table"] == "JRNLROW.DAT" && entry["post_order"] == float64(4242) {
			found = true
			if entry["reason"] == "" {
				t.Fatalf("exception has no reason: %v", entry)
			}
		}
	}
	if !found {
		t.Fatalf("the bad row was not reported with its position: %v", got["exceptions"])
	}
}

// TestPTBReconciliationNeedsFigures proves the panel cannot pass without a figure to check: an empty
// reconciliation leaves the import unreconciled and the commit is refused.
func TestPTBReconciliationNeedsFigures(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "ptb-empty@example.com")
	org := orgFromList(t, a, owner, `{"name":"PTB empty","fiscal_year_end":"12-31"}`)
	entries := sizedTables()
	res := expectStatus(t, uploadPTB(t, h, owner, itoa(org), "backup.ptb", miniBackup(t, entries)), 201)
	id := int64(res["id"].(float64))
	o := itoa(org)

	got := expectStatus(t, previewPTB(t, h, owner, o, id, `{"period":0}`), 200)
	if got["reconciliation"].(map[string]any)["matched"] != false {
		t.Fatalf("an empty reconciliation must not match: %v", got["reconciliation"])
	}
	if got["status"] == "previewed" {
		t.Fatalf("status must stay staged without figures: %v", got["status"])
	}
	expectStatus(t, commitPTB(t, h, owner, o, id, `{}`), 409)
}

// sizedTables is a minimal backup whose records are wide enough for the balance fields, so the
// preview can read the chart, customer and vendor figures.
func sizedTables() map[string][]byte {
	entries := requiredTables()
	chart := miniDATSize(1610)
	zeroBalance(chart, 70, 82, 94)
	entries["CHART.DAT"] = chart
	customer := miniDATSize(200)
	zeroBalance(customer, 162)
	entries["CUSTOMER.DAT"] = customer
	vendor := miniDATSize(240)
	zeroBalance(vendor, 206)
	entries["VENDOR.DAT"] = vendor
	entries["JRNLHDR.DAT"] = miniDATSize(100)
	return entries
}
