package main

import (
	"strconv"
	"testing"
)

// TestCreditNoteReversesRevenueAndGST covers B3: a credit against an invoice reverses its revenue
// account and output GST, so the ledger, GST report and aging all agree. A partial credit reverses pro rata.
func TestCreditNoteReversesRevenueAndGST(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "credit-reversal@example.com")
	org := orgFromList(t, a, owner, `{"name":"Credit reversal","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	o := itoa(org)
	cust := seedContact(t, a, org, owner)
	rev := itoa(int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4200","name":"Consulting","type":"revenue"}`, owner), 201)["id"].(float64)))

	post := func(date string) int64 {
		t.Helper()
		m := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+itoa(cust)+`,"invoice_date":"`+date+`","due_date":"`+date+`","lines":[{"description":"Work","revenue_account_id":`+rev+`,"quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}`, owner), 201)
		id := int64(m["id"].(float64))
		expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(id)+"/post?org_id="+o, "", owner), 200)
		return id
	}
	credit := func(id int64, date string, cents int64, reason string) int64 {
		t.Helper()
		m := expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(id)+"/credit-notes",
			`{"org_id":`+o+`,"credit_date":"`+date+`","reason":"`+reason+`","amount_cents":`+itoa(cents)+`}`, owner), 201)
		return int64(m["journal_id"].(float64))
	}
	var debitRev, debitGST, creditAR int64
	lines := func(jid, account int64) (debit, credit int64) {
		t.Helper()
		if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(sum(debit_cents),0),COALESCE(sum(credit_cents),0) FROM journal_lines WHERE org_id=$1 AND journal_id=$2 AND account_id=$3`, org, jid, account).Scan(&debit, &credit); err != nil {
			t.Fatal(err)
		}
		return
	}
	acct := func(code string) int64 {
		t.Helper()
		var id int64
		if err := a.db.QueryRow(t.Context(), `SELECT id FROM accounts WHERE org_id=$1 AND code=$2`, org, code).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// Full credit of 10,000 net + 900 GST. Seeded accounts exist once the first write lands.
	inv := post("2024-05-01")
	gstOut, ar := acct("2300"), acct("1100")
	sales, _ := strconv.ParseInt(rev, 10, 64)
	cj := credit(inv, "2024-05-10", 10900, "Full refund")
	debitRev, _ = lines(cj, sales)
	debitGST, _ = lines(cj, gstOut)
	_, creditAR = lines(cj, ar)
	if debitRev != 10000 || debitGST != 900 || creditAR != 10900 {
		t.Fatalf("full credit legs revenue=%d gst=%d ar=%d, want 10000/900/10900", debitRev, debitGST, creditAR)
	}
	var gstBalance, salesBalance int64
	if err := a.db.QueryRow(t.Context(), `SELECT
		COALESCE(sum(credit_cents-debit_cents) FILTER (WHERE account_id=$2),0),
		COALESCE(sum(credit_cents-debit_cents) FILTER (WHERE account_id=$3),0)
		FROM journal_lines WHERE org_id=$1`, org, gstOut, sales).Scan(&gstBalance, &salesBalance); err != nil {
		t.Fatal(err)
	}
	if gstBalance != 0 || salesBalance != 0 {
		t.Fatalf("after full credit output GST=%d revenue=%d, want 0/0", gstBalance, salesBalance)
	}

	// The GST report nets the credit against the invoice for the same period.
	audit := expectStatus(t, do(t, h, "GET", "/api/v1/reports/sales-tax?org_id="+o+"&from=2024-05-01&to=2024-05-31", "", owner), 200)
	var taxable, tax int64
	for _, raw := range audit["rows"].([]any) {
		r := raw.(map[string]any)
		taxable += int64(r["taxable_cents"].(float64))
		tax += int64(r["tax_cents"].(float64))
	}
	if taxable != 0 || tax != 0 {
		t.Fatalf("GST report after full credit taxable=%d tax=%d, want 0/0", taxable, tax)
	}
	aging := expectStatus(t, do(t, h, "GET", "/api/v1/reports/receivables-aging?org_id="+o+"&as_of=2024-05-31", "", owner), 200)
	var outstanding int64
	for _, raw := range aging["rows"].([]any) {
		outstanding += int64(raw.(map[string]any)["balance_cents"].(float64))
	}
	if outstanding != 0 {
		t.Fatalf("aging after full credit = %d, want 0", outstanding)
	}

	// A half credit reverses half the revenue and half the GST, leaving the rest on the ledger.
	part := post("2024-06-01")
	pj := credit(part, "2024-06-10", 5450, "Half refund")
	debitRev, _ = lines(pj, sales)
	debitGST, _ = lines(pj, gstOut)
	if debitRev != 5000 || debitGST != 450 {
		t.Fatalf("partial credit legs revenue=%d gst=%d, want 5000/450", debitRev, debitGST)
	}
	if err := a.db.QueryRow(t.Context(), `SELECT
		COALESCE(sum(credit_cents-debit_cents) FILTER (WHERE account_id=$2),0),
		COALESCE(sum(credit_cents-debit_cents) FILTER (WHERE account_id=$3),0)
		FROM journal_lines WHERE org_id=$1`, org, gstOut, sales).Scan(&gstBalance, &salesBalance); err != nil {
		t.Fatal(err)
	}
	if gstBalance != 450 || salesBalance != 5000 {
		t.Fatalf("after partial credit output GST=%d revenue=%d, want 450/5000", gstBalance, salesBalance)
	}
}
