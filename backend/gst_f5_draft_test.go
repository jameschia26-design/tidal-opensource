package main

import "testing"

// TestDraftF5TiesToLedger covers R24: the draft F5 is built from posted records, its output and input
// boxes equal the two GST account movements, the summary keeps them apart, conditional fields are
// accepted or refused, and an approved snapshot cannot change.
func TestDraftF5TiesToLedger(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "gst-f5@example.com")
	org := orgFromList(t, a, owner, `{"name":"F5 co","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	o := itoa(org)
	cust := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201)["id"].(float64))
	supp := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201)["id"].(float64))
	expAcct := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6200","name":"Parts","type":"expense"}`, owner), 201)["id"].(float64))
	// Seeded accounts exist after the first write.
	expectStatus(t, do(t, h, "POST", "/api/v1/expenses", `{"org_id":`+o+`,"expense_date":"2025-01-01","payee":"Seed","description":"Seed","expense_account_id":`+itoa(expAcct)+`,"amount_cents":100}`, owner), 201)
	var sales int64
	if err := a.db.QueryRow(t.Context(), `SELECT id FROM accounts WHERE org_id=$1 AND code='4000'`, org).Scan(&sales); err != nil {
		t.Fatal(err)
	}
	invoice := func(code string, net int64, gst int, ref string) int64 {
		t.Helper()
		body := `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"reference":"` + ref + `","invoice_date":"2026-05-05","due_date":"2026-05-31","lines":[{"description":"Work","quantity":1,"unit_price_cents":` + itoa(net) + `,"revenue_account_id":` + itoa(sales) + `,"gst_code":"` + code + `"}]}`
		id := int64(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", body, owner), 201)["id"].(float64))
		expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(id)+"/post?org_id="+o, "", owner), 200)
		if gst >= 0 {
			_ = gst
		}
		return id
	}
	srInvoice := invoice("SR", 1000000, 90000, "F5-SR")
	invoice("ZR", 200000, 0, "F5-ZR")
	invoice("ES", 100000, 0, "F5-ES")
	invoice("OS", 50000, 0, "F5-OS")
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(srInvoice)+"/credit-notes", `{"org_id":`+o+`,"credit_date":"2026-05-20","reason":"Return","amount_cents":109000}`, owner), 201)

	bill := func(code string, net int64, ref string) int64 {
		t.Helper()
		body := `{"org_id":` + o + `,"contact_id":` + itoa(supp) + `,"supplier_reference":"` + ref + `","bill_date":"2026-05-06","due_date":"2026-05-31","lines":[{"account_id":` + itoa(expAcct) + `,"description":"Parts","quantity":1,"unit_price_cents":` + itoa(net) + `,"gst_code":"` + code + `"}]}`
		id := int64(expectStatus(t, do(t, h, "POST", "/api/v1/bills", body, owner), 201)["id"].(float64))
		expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(id)+"/post?org_id="+o, "", owner), 200)
		return id
	}
	srBill := bill("SR", 400000, "F5-B1")
	bill("ZR", 100000, "F5-B2")
	expectStatus(t, do(t, h, "POST", "/api/v1/expenses", `{"org_id":`+o+`,"expense_date":"2026-05-07","payee":"Shop","description":"Paper","expense_account_id":`+itoa(expAcct)+`,"amount_cents":109000,"gst_code":"SR"}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(srBill)+"/credit-notes", `{"org_id":`+o+`,"credit_date":"2026-05-21","reason":"Returned","net_cents":50000,"gst_cents":4500}`, owner), 201)

	draft := expectStatus(t, do(t, h, "GET", "/api/v1/tax/gst-f5?org_id="+o+"&from=2026-05-01&to=2026-05-31", "", owner), 200)
	box := func(n string) int64 {
		for _, raw := range draft["boxes"].([]any) {
			row := raw.(map[string]any)
			if row["box"] == n {
				return int64(row["value_cents"].(float64))
			}
		}
		t.Fatalf("box %s missing: %v", n, draft)
		return 0
	}
	if box("6") != 81000 || box("7") != 40500 || box("8") != 40500 {
		t.Fatalf("boxes 6/7/8 = %d/%d/%d, want 81000/40500/40500", box("6"), box("7"), box("8"))
	}
	if box("1") != 900000 || box("2") != 200000 || box("3") != 100000 || box("5") != 550000 {
		t.Fatalf("boxes 1/2/3/5 = %d/%d/%d/%d, want 900000/200000/100000/550000", box("1"), box("2"), box("3"), box("5"))
	}
	if draft["draft"] != true || draft["filed"] != false || draft["net_payable_cents"] != float64(40500) {
		t.Fatalf("draft flags %v", draft)
	}
	// Both boxes tie to the ledger accounts, to the cent.
	if draft["reconciles_output"] != true || draft["reconciles_input"] != true {
		t.Fatalf("draft does not reconcile: %v", draft)
	}

	// The summary keeps output and input apart.
	sum := expectStatus(t, do(t, h, "GET", "/api/v1/reports/sales-tax?org_id="+o+"&from=2026-05-01&to=2026-05-31", "", owner), 200)
	if sum["output_tax_cents"] != float64(81000) || sum["input_tax_cents"] != float64(40500) || sum["net_payable_cents"] != float64(40500) {
		t.Fatalf("summary %v", sum)
	}

	// Conditional fields: accepted when supported, IGDS refused as unsupported.
	worksheet := `{"org_id":` + o + `,"period_start":"2026-05-01","period_end":"2026-05-31","revenue_cents":900000,"lines":[{"kind":"sale","code":"SR","description":"Work","net_cents":900000,"gst_cents":81000}],"tourist_refund_claim":true,"tourist_refund_amount_cents":1000}`
	worksheetResp := expectStatus(t, do(t, h, "POST", "/api/v1/tax/gst-f5", worksheet, owner), 200)
	if worksheetResp["boxes"] == nil {
		t.Fatal(worksheetResp)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/tax/gst-f5", `{"org_id":`+o+`,"period_start":"2026-05-01","period_end":"2026-05-31","revenue_cents":900000,"lines":[{"kind":"sale","code":"SR","description":"Work","net_cents":900000,"gst_cents":81000}],"igds_applicable":true}`, owner), 422)

	// A saved snapshot is approved once and can never change afterwards.
	snap := expectStatus(t, do(t, h, "POST", "/api/v1/tax/gst-f5/snapshots", `{"org_id":`+o+`,"period_start":"2026-05-01","period_end":"2026-05-31"}`, owner), 201)
	snapID := int64(snap["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/tax/gst-f5/snapshots/"+itoa(snapID)+"/approve", `{"org_id":`+o+`}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/tax/gst-f5/snapshots/"+itoa(snapID)+"/approve", `{"org_id":`+o+`}`, owner), 409)
	if _, err := a.db.Exec(t.Context(), `UPDATE gst_f5_snapshots SET payload='{}'::jsonb WHERE org_id=$1 AND id=$2`, org, snapID); err == nil {
		t.Fatal("approved snapshot payload was changed")
	}
}
