package main

import "testing"

// TestAccountantPackTiesToTrialBalance covers R29: the pack carries the same trial balance totals as the
// trial-balance report, plus mappings, the fixed-asset schedule and manual GST adjustments.
func TestAccountantPackTiesToTrialBalance(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "pack@example.com")
	org := orgFromList(t, a, owner, `{"name":"Pack co","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	o := itoa(org)
	cust := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201)["id"].(float64))
	inv := int64(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+itoa(cust)+`,"invoice_date":"2026-07-01","due_date":"2026-07-31","lines":[{"description":"Work","quantity":1,"unit_price_cents":100000,"gst_code":"SR"}]}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(inv)+"/post?org_id="+o, "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/assets", `{"org_id":`+o+`,"name":"Laptop","acquired_on":"2026-07-01","cost_cents":120000,"useful_life_months":36}`, owner), 201)
	// A manual adjustment touching output GST.
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"2026-07-20","memo":"GST adjustment","idempotency_key":"manual:pack-1","lines":[{"account_id":`+itoa(accountIDByCode(t, a, org, "2300"))+`,"debit_cents":500,"description":"fix"},{"account_id":`+itoa(accountIDByCode(t, a, org, "3900"))+`,"credit_cents":500,"description":"fix"}]}`, owner), 201)

	pack := expectStatus(t, do(t, h, "GET", "/api/v1/reports/accountant-pack?org_id="+o+"&from=2026-07-01&to=2026-07-31", "", owner), 200)
	tb := expectStatus(t, do(t, h, "GET", "/api/v1/reports/trial-balance?org_id="+o+"&as_of=2026-07-31", "", owner), 200)
	ptb := pack["trial_balance"].(map[string]any)
	if ptb["total_debit_cents"] != tb["total_debit_cents"] || ptb["total_credit_cents"] != tb["total_credit_cents"] {
		t.Fatalf("pack totals %v/%v, trial balance %v/%v", ptb["total_debit_cents"], ptb["total_credit_cents"], tb["total_debit_cents"], tb["total_credit_cents"])
	}
	if ptb["balanced"] != true || pack["worksheet_only"] != true || pack["filed"] != false {
		t.Fatalf("pack flags %v", pack)
	}
	if len(pack["account_mappings"].([]any)) == 0 || len(pack["fixed_assets"].([]any)) != 1 || len(pack["tax_adjustments"].([]any)) != 1 {
		t.Fatalf("pack sections %v", pack)
	}
	asset := pack["fixed_assets"].([]any)[0].(map[string]any)
	if asset["cost_cents"] != float64(120000) || asset["book_value_cents"] != float64(120000) {
		t.Fatalf("asset row %v", asset)
	}
}

func accountIDByCode(t *testing.T, a *app, org int64, code string) int64 {
	t.Helper()
	var id int64
	if err := a.db.QueryRow(t.Context(), `SELECT id FROM accounts WHERE org_id=$1 AND code=$2`, org, code).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
