package main

import (
	"context"
	"testing"
)

// Tax model (Phase 1c): dated catalogue lookup, per-account default rate, inclusive and exclusive
// totals matching Xero's worked example, organisation tax settings, and the single GST control account
// with a dated reclassification that keeps the trial balance intact.

func TestTaxCatalogueRateByDate(t *testing.T) {
	a, _ := testApp(t)
	ctx := context.Background()
	for _, tc := range []struct {
		on    string
		want  int64
		label string
	}{
		{"2022-06-30", 700, "7% before 2023"},
		{"2023-01-01", 800, "8% during 2023"},
		{"2023-12-31", 800, "8% last day of 2023"},
		{"2024-01-01", 900, "9% from 2024"},
		{"2026-11-03", 900, "9% today"},
	} {
		rate, kind, found, err := taxRateOn(ctx, a.db, "sr", "sale", tc.on)
		if err != nil {
			t.Fatal(err)
		}
		if !found || rate != tc.want || kind != "standard" {
			t.Errorf("%s (%s): rate=%d kind=%q found=%v, want %d", tc.label, tc.on, rate, kind, found, tc.want)
		}
	}
	// Zero-rated, exempt and out-of-scope codes resolve to zero tax on both sides.
	for _, code := range []string{"ZR", "ES", "OS", "NONE"} {
		rate, _, found, err := taxRateOn(ctx, a.db, code, "sale", "2026-01-01")
		if err != nil {
			t.Fatal(err)
		}
		if !found || rate != 0 {
			t.Errorf("%s rate=%d found=%v, want 0", code, rate, found)
		}
	}
	// A code the catalogue does not hold is reported as missing, not as zero tax.
	if _, _, found, err := taxRateOn(ctx, a.db, "NOPE", "sale", "2026-01-01"); err != nil || found {
		t.Errorf("unknown code found=%v err=%v", found, err)
	}
}

func TestAccountTaxDefaultAndOverride(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "tax-default@example.com")
	org := orgFromList(t, a, owner, `{"name":"Tax co","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	o, cust := itoa(org), seedContact(t, a, org, owner)

	// An account with a standard-rate default.
	sales := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4100","name":"Consulting","type":"revenue","tax_rate_code":"SR"}`, owner), 201)["id"].(float64))

	newInvoice := func(line string) map[string]any {
		t.Helper()
		body := `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"invoice_date":"2026-04-01","due_date":"2026-04-30","lines":[` + line + `]}`
		// The response carries the computed lines, so the test reads what the server priced.
		return expectStatus(t, do(t, h, "POST", "/api/v1/invoices", body, owner), 201)
	}

	// A line that names the account but no tax code takes the account's default: 9% from 2024.
	got := newInvoice(`{"description":"Work","quantity":1,"unit_price_cents":10000,"revenue_account_id":` + itoa(sales) + `}`)
	lines := got["lines"].([]any)
	first := lines[0].(map[string]any)
	if first["gst_code"] != "SR" || first["gst_rate_bp"] != float64(900) || first["gst_cents"] != float64(900) {
		t.Fatalf("account default not applied: %v", first)
	}

	// An explicit code still wins over the account default.
	got = newInvoice(`{"description":"Work","quantity":1,"unit_price_cents":10000,"revenue_account_id":` + itoa(sales) + `,"gst_code":"NONE"}`)
	first = got["lines"].([]any)[0].(map[string]any)
	if first["gst_code"] != "NONE" || first["gst_cents"] != float64(0) {
		t.Fatalf("line override not honoured: %v", first)
	}
}

func TestInclusiveAndExclusiveTotalsMatchXero(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "tax-totals@example.com")
	org := orgFromList(t, a, owner, `{"name":"Totals co","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	o, cust := itoa(org), seedContact(t, a, org, owner)

	total := func(body string) (int64, int64, int64) {
		t.Helper()
		created := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", body, owner), 201)
		line := created["lines"].([]any)[0].(map[string]any)
		return int64(line["amount_cents"].(float64)), int64(line["gst_cents"].(float64)), int64(created["total_cents"].(float64))
	}
	exclusive := `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"invoice_date":"2026-01-15","due_date":"2026-01-31","amounts_are":"exclusive","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}`
	if net, gst, want := total(exclusive); net != 10000 || gst != 900 || want != 10900 {
		t.Fatalf("exclusive 100.00 at 9%% = net %d gst %d total %d, want 10000/900/10900", net, gst, want)
	}
	inclusive := `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"invoice_date":"2026-01-15","due_date":"2026-01-31","amounts_are":"inclusive","lines":[{"description":"Work","quantity":1,"unit_price_cents":10900,"gst_code":"SR"}]}`
	if net, gst, want := total(inclusive); net != 10000 || gst != 900 || want != 10900 {
		t.Fatalf("inclusive 109.00 at 9%% = net %d gst %d total %d, want 10000/900/10900", net, gst, want)
	}
	// Half-up line rounding: 5 cents of GST on 55.56 at 9% (5.0004 -> 5), and 100.05 inclusive backs out
	// to 91.79 + 8.26 = 100.05 with no lost cent.
	rounded := `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"invoice_date":"2026-01-15","due_date":"2026-01-31","amounts_are":"exclusive","lines":[{"description":"Work","quantity":1,"unit_price_cents":5556,"gst_code":"SR"}]}`
	if net, gst, want := total(rounded); net != 5556 || gst != 500 || want != 6056 {
		t.Fatalf("5556 at 9%% = net %d gst %d total %d, want 5556/500/6056", net, gst, want)
	}
}

func TestTaxSettingsRoundTrip(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "tax-settings@example.com")
	org := orgFromList(t, a, owner, `{"name":"Settings co","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	o := itoa(org)

	got := expectStatus(t, do(t, h, "GET", "/api/v1/tax/settings?org_id="+o, "", owner), 200)
	if got["tax_basis"] != "accrual" || got["tax_display_name"] != "GST" || got["tax_period"] != "quarterly" ||
		got["default_sales_treatment"] != "exclusive" || got["default_purchase_treatment"] != "exclusive" {
		t.Fatalf("defaults = %v", got)
	}
	body := `{"org_id":` + o + `,"tax_basis":"payment","tax_id":"M90371234X","tax_display_name":"GST (IRAS)","tax_period":"monthly","default_sales_treatment":"inclusive","default_purchase_treatment":"inclusive"}`
	expectStatus(t, do(t, h, "PUT", "/api/v1/tax/settings", body, owner), 200)
	got = expectStatus(t, do(t, h, "GET", "/api/v1/tax/settings?org_id="+o, "", owner), 200)
	if got["tax_basis"] != "payment" || got["tax_id"] != "M90371234X" || got["tax_period"] != "monthly" ||
		got["default_sales_treatment"] != "inclusive" {
		t.Fatalf("saved settings = %v", got)
	}
	// The tax ID is the company's GST number, so the two can never disagree.
	var stored string
	if err := a.db.QueryRow(t.Context(), `SELECT gst_number FROM organizations WHERE id=$1`, org).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "M90371234X" {
		t.Fatalf("gst_number = %q", stored)
	}
	// Bad period and an empty display name are refused rather than stored.
	expectStatus(t, do(t, h, "PUT", "/api/v1/tax/settings", `{"org_id":`+o+`,"tax_basis":"payment","tax_id":"","tax_display_name":"GST","tax_period":"weekly","default_sales_treatment":"exclusive","default_purchase_treatment":"exclusive"}`, owner), 400)
	expectStatus(t, do(t, h, "PUT", "/api/v1/tax/settings", `{"org_id":`+o+`,"tax_basis":"payment","tax_id":"","tax_display_name":"  ","tax_period":"monthly","default_sales_treatment":"exclusive","default_purchase_treatment":"exclusive"}`, owner), 400)
}

func TestAccountTaxRateUpdateAndCatalogueListing(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "tax-account@example.com")
	org := orgFromList(t, a, owner, `{"name":"Chart co","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	o := itoa(org)

	rates := expectStatus(t, do(t, h, "GET", "/api/v1/tax/rates?org_id="+o+"&on=2023-06-01", "", owner), 200)
	found := false
	for _, raw := range rates["rates"].([]any) {
		row := raw.(map[string]any)
		if row["code"] == "SR" {
			found = true
			if row["rate_bp"] != float64(800) {
				t.Fatalf("SR on 2023-06-01 = %v, want 800", row["rate_bp"])
			}
		}
		if row["code"] == "IM" || row["code"] == "RC" || row["kind"] == "reverse_charge" || row["kind"] == "import" {
			// Catalogue holds the SG extras even though documents cannot post them yet.
			continue
		}
	}
	if !found {
		t.Fatalf("SR missing from catalogue: %v", rates)
	}

	id := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6200","name":"Parts","type":"expense"}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "PUT", "/api/v1/accounts/"+itoa(id), `{"org_id":`+o+`,"tax_rate_code":"SR"}`, owner), 200)
	accounts := expectStatus(t, do(t, h, "GET", "/api/v1/accounts?org_id="+o, "", owner), 200)
	seen := ""
	for _, raw := range accounts["accounts"].([]any) {
		row := raw.(map[string]any)
		if int64(row["id"].(float64)) == id {
			seen = row["tax_rate_code"].(string)
		}
	}
	if seen != "SR" {
		t.Fatalf("chart row tax rate = %q, want SR", seen)
	}
	// An unknown code is refused with a specific error.
	expectStatus(t, do(t, h, "PUT", "/api/v1/accounts/"+itoa(id), `{"org_id":`+o+`,"tax_rate_code":"NOPE"}`, owner), 422)
	// Imports and reverse charge are self-assessed on purchase bills, so an account may default to them.
	expectStatus(t, do(t, h, "PUT", "/api/v1/accounts/"+itoa(id), `{"org_id":`+o+`,"tax_rate_code":"RC"}`, owner), 200)
}

func TestGSTControlReclassificationKeepsTrialBalance(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "gst-control@example.com")
	org := orgFromList(t, a, owner, `{"name":"Control co","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	o := itoa(org)
	ctx := t.Context()

	// A company created after the migration has no GST Input account. Put the old asset back the way
	// an upgraded company holds it, with a balance, to exercise the reclassification.
	// The chart is seeded on the first write, so this company has no accounts yet; create both rows the
	// reclassification needs directly.
	if _, err := a.db.Exec(ctx, `INSERT INTO accounts(org_id,code,name,type,class,is_bank_account) VALUES
		($1,'2300','GST','liability','current_liability',false),
		($1,'2310','GST Input','asset','current_asset',false),
		($1,'1000','Bank','asset','bank',true)`, org); err != nil {
		t.Fatal(err)
	}
	input := accountIDByCode(t, a, org, "2310")
	bank := accountIDByCode(t, a, org, "1000")
	expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"2026-02-01","memo":"Old input GST","idempotency_key":"control-seed","lines":[{"account_id":`+itoa(input)+`,"debit_cents":5000},{"account_id":`+itoa(bank)+`,"credit_cents":5000}]}`, owner), 201)

	balance := func() (debit, credit int64) {
		t.Helper()
		if err := a.db.QueryRow(ctx, `SELECT COALESCE(SUM(debit_cents),0), COALESCE(SUM(credit_cents),0) FROM journal_lines WHERE org_id=$1`, org).Scan(&debit, &credit); err != nil {
			t.Fatal(err)
		}
		return debit, credit
	}
	beforeDebit, beforeCredit := balance()
	if beforeDebit != beforeCredit {
		t.Fatalf("seed journal unbalanced: %d/%d", beforeDebit, beforeCredit)
	}
	if _, err := a.db.Exec(ctx, `SELECT reclassify_gst_control($1)`, org); err != nil {
		t.Fatal(err)
	}
	afterDebit, afterCredit := balance()
	// The reclassification adds one balanced journal (5000 debit + 5000 credit), so gross totals grow by
	// that amount and nothing else: the trial balance still ties and the net of every account is zero.
	if afterDebit != afterCredit || afterDebit-beforeDebit != 5000 || afterCredit-beforeCredit != 5000 {
		t.Fatalf("reclassification changed the trial balance: before %d/%d after %d/%d", beforeDebit, beforeCredit, afterDebit, afterCredit)
	}
	var netAll int64
	if err := a.db.QueryRow(ctx, `SELECT COALESCE(SUM(debit_cents-credit_cents),0) FROM journal_lines WHERE org_id=$1`, org).Scan(&netAll); err != nil {
		t.Fatal(err)
	}
	if netAll != 0 {
		t.Fatalf("net of all accounts = %d, want 0", netAll)
	}
	control := accountIDByCode(t, a, org, "2300")
	var inputBalance, controlBalance int64
	if err := a.db.QueryRow(ctx, `SELECT COALESCE(SUM(debit_cents-credit_cents),0) FROM journal_lines WHERE org_id=$1 AND account_id=$2`, org, input).Scan(&inputBalance); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(ctx, `SELECT COALESCE(SUM(debit_cents-credit_cents),0) FROM journal_lines WHERE org_id=$1 AND account_id=$2`, org, control).Scan(&controlBalance); err != nil {
		t.Fatal(err)
	}
	if inputBalance != 0 || controlBalance != 5000 {
		t.Fatalf("after reclassification input=%d control=%d, want 0 and 5000", inputBalance, controlBalance)
	}
	// The move is excluded from GST returns, so history is not double counted.
	var tagged, journals int64
	if err := a.db.QueryRow(ctx, `SELECT COALESCE(SUM(CASE WHEN tax_direction='none' THEN 1 ELSE 0 END),0), count(*) FROM journal_lines WHERE org_id=$1 AND account_id IN ($2,$3)`, org, input, control).Scan(&tagged, &journals); err != nil {
		t.Fatal(err)
	}
	if tagged != 2 {
		t.Fatalf("reclassification lines tagged none = %d, want 2 (of %d)", tagged, journals)
	}
	// Running it again posts nothing.
	if _, err := a.db.Exec(ctx, `SELECT reclassify_gst_control($1)`, org); err != nil {
		t.Fatal(err)
	}
	var reclassJournals int64
	if err := a.db.QueryRow(ctx, `SELECT count(*) FROM journals WHERE org_id=$1 AND idempotency_key='gst-control-reclass:2310'`, org).Scan(&reclassJournals); err != nil {
		t.Fatal(err)
	}
	if reclassJournals != 1 {
		t.Fatalf("reclassification journals = %d, want 1", reclassJournals)
	}
}

// Input tax now debits the single GST control as a tagged input line, and the F5 still keeps boxes 6
// and 7 apart from that one account.
func TestInputGSTPostsToSingleControlAccount(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "gst-single@example.com")
	org := orgFromList(t, a, owner, `{"name":"Single co","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	o := itoa(org)
	supp := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201)["id"].(float64))
	expAcct := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6200","name":"Parts","type":"expense","tax_rate_code":"SR"}`, owner), 201)["id"].(float64))
	bill := int64(expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+itoa(supp)+`,"supplier_reference":"S1","bill_date":"2026-03-01","due_date":"2026-03-31","lines":[{"account_id":`+itoa(expAcct)+`,"description":"Parts","quantity":1,"unit_price_cents":10000}]}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(bill)+"/post?org_id="+o, "", owner), 200)
	var inputTax int64
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(SUM(l.debit_cents),0) FROM journal_lines l JOIN accounts a ON a.org_id=l.org_id AND a.id=l.account_id WHERE l.org_id=$1 AND a.code='2300' AND l.tax_direction='input'`, org).Scan(&inputTax); err != nil {
		t.Fatal(err)
	}
	if inputTax != 900 {
		t.Fatalf("input GST on the control account = %d, want 900 (the bill line defaulted to SR from its account)", inputTax)
	}
	// No GST Input asset is seeded for a company created after this change.
	var legacy int64
	if err := a.db.QueryRow(t.Context(), `SELECT count(*) FROM accounts WHERE org_id=$1 AND code='2310'`, org).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if legacy != 0 {
		t.Fatalf("legacy GST input account was seeded: %d rows", legacy)
	}
}
