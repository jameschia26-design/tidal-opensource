package main

import "testing"

// TestAssetPurchaseAndDisposal covers R10: an asset-account bill line capitalises into the register with the
// matching ledger postings, and a disposal removes the asset, records the gain or loss, and stops depreciation.
func TestAssetPurchaseAndDisposal(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "asset-link@example.com")
	org := orgFromList(t, a, owner, `{"name":"Asset link","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	o := itoa(org)
	supp := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201)["id"].(float64))
	// Posting seeds the reserved accounts, including the 1500 asset cost account.
	seed := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+itoa(seedContact(t, a, org, owner))+`,"invoice_date":"2026-01-01","due_date":"2026-01-01","lines":[{"description":"seed","quantity":1,"unit_price_cents":100}]}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(int64(seed["id"].(float64)))+"/post?org_id="+o, "", owner), 200)
	var costAcct, bank int64
	if err := a.db.QueryRow(t.Context(), `SELECT (SELECT id FROM accounts WHERE org_id=$1 AND code='1500'),(SELECT id FROM accounts WHERE org_id=$1 AND code='1000')`, org).Scan(&costAcct, &bank); err != nil {
		t.Fatal(err)
	}

	// An asset bill of 10,000 plus 900 GST: Dr asset 10,000, Dr input GST 900, Cr AP 10,900.
	bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+itoa(supp)+`,"supplier_reference":"AS-1","bill_date":"2026-01-05","due_date":"2026-01-05","lines":[{"account_id":`+itoa(costAcct)+`,"description":"Laptop","quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}`, owner), 201)
	billID := int64(bill["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(billID)+"/post?org_id="+o, "", owner), 200)

	ledger := func(code string) (debit, credit int64) {
		t.Helper()
		if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(sum(l.debit_cents),0),COALESCE(sum(l.credit_cents),0)
			FROM journal_lines l JOIN accounts a ON a.org_id=l.org_id AND a.id=l.account_id
			WHERE l.org_id=$1 AND a.code=$2 AND l.journal_id=(SELECT journal_id FROM bills WHERE org_id=$1 AND id=$3)`, org, code, billID).Scan(&debit, &credit); err != nil {
			t.Fatal(err)
		}
		return
	}
	if d, _ := ledger("1500"); d != 10000 {
		t.Fatalf("asset debit %d, want 10000", d)
	}
	if d, _ := ledger("2300"); d != 900 {
		t.Fatalf("input GST debit %d, want 900", d)
	}
	if _, c := ledger("2100"); c != 10900 {
		t.Fatalf("accounts payable credit %d, want 10900", c)
	}
	var entries int
	var regCost int64
	if err := a.db.QueryRow(t.Context(), `SELECT count(*), COALESCE(max(cost_cents),0) FROM fixed_assets WHERE org_id=$1 AND acquisition_journal_id IS NOT NULL`, org).Scan(&entries, &regCost); err != nil {
		t.Fatal(err)
	}
	if entries != 1 || regCost != 10000 {
		t.Fatalf("register entries=%d cost=%d, want one entry of 10000", entries, regCost)
	}

	// Disposal: cost 4,000 and no depreciation yet, sold for 3,000, so the loss is 1,000.
	sellAcct := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"1030","name":"Sale proceeds","type":"asset"}`, owner), 201)["id"].(float64))
	assetID := int64(expectStatus(t, do(t, h, "POST", "/api/v1/assets", `{"org_id":`+o+`,"name":"Press","acquired_on":"2025-01-01","cost_cents":4000,"useful_life_months":48}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"2025-01-01","memo":"Press purchase","idempotency_key":"press-buy","lines":[{"description":"Press","account_id":`+itoa(costAcct)+`,"debit_cents":4000},{"description":"Bank","account_id":`+itoa(bank)+`,"credit_cents":4000}]}`, owner), 201)
	disp := expectStatus(t, do(t, h, "POST", "/api/v1/assets/"+itoa(assetID)+"/dispose", `{"org_id":`+o+`,"disposed_on":"2026-02-10","proceeds_cents":3000,"proceeds_account_id":`+itoa(sellAcct)+`}`, owner), 200)
	if disp["gain_loss_cents"] != float64(-1000) {
		t.Fatalf("disposal gain/loss %v, want -1000", disp["gain_loss_cents"])
	}
	schedule := expectStatus(t, do(t, h, "GET", "/api/v1/reports/disposal-schedule?org_id="+o, "", owner), 200)
	disposals := schedule["rows"].([]any)
	if len(disposals) != 1 || disposals[0].(map[string]any)["gain_loss_cents"] != float64(-1000) {
		t.Fatalf("disposal schedule: %v", schedule)
	}
	var lossDebit, costCredit, proceedsDebit int64
	if err := a.db.QueryRow(t.Context(), `SELECT
		COALESCE(sum(l.debit_cents) FILTER (WHERE a.code='6900'),0),
		COALESCE(sum(l.credit_cents) FILTER (WHERE a.code='1500'),0),
		COALESCE(sum(l.debit_cents) FILTER (WHERE a.id=$2),0)
		FROM journal_lines l JOIN accounts a ON a.org_id=l.org_id AND a.id=l.account_id
		WHERE l.org_id=$1 AND l.journal_id=$3`, org, sellAcct, int64(disp["journal_id"].(float64))).Scan(&lossDebit, &costCredit, &proceedsDebit); err != nil {
		t.Fatal(err)
	}
	if lossDebit != 1000 || costCredit != 4000 || proceedsDebit != 3000 {
		t.Fatalf("disposal legs loss=%d cost=%d proceeds=%d, want 1000/4000/3000", lossDebit, costCredit, proceedsDebit)
	}
	// Depreciation stops: a later run posts nothing for the disposed asset.
	expectStatus(t, do(t, h, "POST", "/api/v1/assets/depreciation/run", `{"org_id":`+o+`,"month":"2026-03"}`, owner), 200)
	var depRows int
	if err := a.db.QueryRow(t.Context(), `SELECT count(*) FROM asset_depreciation WHERE org_id=$1 AND asset_id=$2`, org, assetID).Scan(&depRows); err != nil {
		t.Fatal(err)
	}
	if depRows != 0 {
		t.Fatalf("disposed asset posted %d depreciation months", depRows)
	}

	// The register and the ledger agree over the month the asset left.
	rec := expectStatus(t, do(t, h, "GET", "/api/v1/reports/fixed-asset-reconciliation?org_id="+o+"&from=2026-02-01&to=2026-02-28", "", owner), 200)
	diff := rec["difference"].(map[string]any)
	if diff["closing_cost_cents"].(float64) != 0 || diff["closing_accum_cents"].(float64) != 0 {
		t.Fatalf("reconciliation difference %v, want zero cost and accumulated", diff)
	}
}
