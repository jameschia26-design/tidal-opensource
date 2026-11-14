package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// Needs TEST_DATABASE_URL (disposable database; the schema is wiped). Skips otherwise.
// Covers company isolation, role checks, post/payment immutability, idempotency and balance.

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return m
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) map[string]any {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d, body %s", rec.Code, want, rec.Body.String())
	}
	return decodeBody(t, rec)
}

func orgFromList(t *testing.T, a *app, token, body string) int64 {
	t.Helper()
	rec := do(t, a.routes(), "POST", "/api/v1/organizations", body, token)
	m := expectStatus(t, rec, http.StatusCreated)
	return int64(m["id"].(float64))
}

func seedContact(t *testing.T, a *app, orgID int64, token string) int64 {
	t.Helper()
	rec := do(t, a.routes(), "POST", "/api/v1/contacts",
		`{"org_id":`+coreID(orgID)+`,"role":"customer","name":"Buyer Pte"}`, token)
	m := expectStatus(t, rec, http.StatusCreated)
	return int64(m["id"].(float64))
}

func coreID(n int64) string { return strconv.FormatInt(n, 10) }

func TestCoreInvoiceFlowAndIsolation(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	tokA := seedSession(t, a, "owner-a@example.com")
	tokB := seedSession(t, a, "owner-b@example.com")
	orgA := orgFromList(t, a, tokA, `{"name":"Alpha Pte Ltd","uen":"","fiscal_year_end":"12-31","gst_registered":false}`)
	orgB := orgFromList(t, a, tokB, `{"name":"Beta Pte Ltd","uen":"","fiscal_year_end":"12-31","gst_registered":false}`)
	contactA := seedContact(t, a, orgA, tokA)

	// Draft create: server computes total; client may not send one.
	draftBody := `{"org_id":` + coreID(orgA) + `,"contact_id":` + coreID(contactA) +
		`,"invoice_date":"2026-10-01","due_date":"2026-10-31","lines":[{"description":"Work","quantity":2,"unit_price_cents":5000}]}`
	m := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", draftBody, tokA), http.StatusCreated)
	invID := int64(m["id"].(float64))
	if int64(m["total_cents"].(float64)) != 10000 {
		t.Fatalf("total = %v", m["total_cents"])
	}
	if rec := do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+coreID(orgA)+`,"contact_id":`+coreID(contactA)+
		`,"invoice_date":"2026-10-01","due_date":"2026-10-31","total_cents":1,"lines":[{"description":"x","quantity":1,"unit_price_cents":1}]}`, tokA); rec.Code != http.StatusBadRequest {
		t.Fatalf("client total accepted: %d", rec.Code)
	}

	// Company isolation: B cannot read A's invoice or list it.
	if rec := do(t, h, "GET", "/api/v1/invoices/"+coreID(invID)+"?org_id="+coreID(orgA), "", tokB); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-org read = %d", rec.Code)
	}
	// B cannot create an invoice pointing at A's contact.
	if rec := do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+coreID(orgB)+`,"contact_id":`+coreID(contactA)+
		`,"invoice_date":"2026-10-01","due_date":"2026-10-31","lines":[{"description":"x","quantity":1,"unit_price_cents":100}]}`, tokB); rec.Code == http.StatusCreated {
		t.Fatal("cross-company contact link accepted")
	}

	// Post twice: same number, one journal.
	p1 := expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+coreID(invID)+"/post?org_id="+coreID(orgA), "", tokA), http.StatusOK)
	p2 := expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+coreID(invID)+"/post?org_id="+coreID(orgA), "", tokA), http.StatusOK)
	if p1["number"] != p2["number"] || p1["journal_id"] != p2["journal_id"] || p1["number"].(float64) != 1 {
		t.Fatalf("repeat post changed result: %v vs %v", p1, p2)
	}

	// Payment: partial, then idempotent retry, then overpayment refused.
	pay := `{"org_id":` + coreID(orgA) + `,"amount_cents":4000,"payment_date":"2026-10-05","idempotency_key":"pay-1"}`
	first := expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+coreID(invID)+"/payments", pay, tokA), http.StatusCreated)
	retry := expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+coreID(invID)+"/payments", pay, tokA), http.StatusOK)
	if first["payment_id"] != retry["payment_id"] || first["balance_cents"].(float64) != 6000 {
		t.Fatalf("idempotent retry differs: %v vs %v", first, retry)
	}
	conflict := `{"org_id":` + coreID(orgA) + `,"amount_cents":1,"payment_date":"2026-10-05","idempotency_key":"pay-1"}`
	if rec := do(t, h, "POST", "/api/v1/invoices/"+coreID(invID)+"/payments", conflict, tokA); rec.Code != http.StatusConflict {
		t.Fatalf("key reuse with new body = %d", rec.Code)
	}
	over := `{"org_id":` + coreID(orgA) + `,"amount_cents":6001,"payment_date":"2026-10-06","idempotency_key":"pay-2"}`
	if rec := do(t, h, "POST", "/api/v1/invoices/"+coreID(invID)+"/payments", over, tokA); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("overpayment = %d", rec.Code)
	}

	// Ledger balances: trial balance must tie.
	tb := expectStatus(t, do(t, h, "GET", "/api/v1/reports/trial-balance?org_id="+coreID(orgA)+"&as_of=2026-12-31", "", tokA), http.StatusOK)
	if tb["total_debit_cents"] != tb["total_credit_cents"] || tb["balanced"] != true {
		t.Fatalf("trial balance does not tie: %v", tb)
	}
	bs := expectStatus(t, do(t, h, "GET", "/api/v1/reports/balance-sheet?org_id="+coreID(orgA)+"&as_of=2026-12-31", "", tokA), http.StatusOK)
	if bs["balanced"] != true {
		t.Fatalf("balance sheet does not balance: %v", bs)
	}
	pnl := expectStatus(t, do(t, h, "GET", "/api/v1/reports/pnl?org_id="+coreID(orgA)+"&from=2026-01-01&to=2026-12-31", "", tokA), http.StatusOK)
	if pnl["profit_cents"].(float64) != 10000 {
		t.Fatalf("pnl = %v", pnl)
	}

	// Posted invoice is immutable at the database: a direct update must fail.
	if _, err := a.db.Exec(t.Context(), `UPDATE invoices SET total_cents = 1 WHERE id = $1`, invID); err == nil {
		t.Fatal("posted invoice total changed")
	}
}

func TestCoreViewerCannotWriteAndGSTRefused(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "owner-gst@example.com")
	org := orgFromList(t, a, owner, `{"name":"Gamma Pte Ltd","uen":"","fiscal_year_end":"12-31","gst_registered":false}`)
	contact := seedContact(t, a, org, owner)

	// A real viewer member of the same company.
	viewer := seedSession(t, a, "viewer-gst@example.com")
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO memberships (org_id, user_id, role) SELECT $1, id, 'viewer' FROM users WHERE email=$2`,
		org, "viewer-gst@example.com"); err != nil {
		t.Fatal(err)
	}
	draft := `{"org_id":` + coreID(org) + `,"contact_id":` + coreID(contact) +
		`,"invoice_date":"2026-10-01","due_date":"2026-10-31","lines":[{"description":"x","quantity":1,"unit_price_cents":100}]}`
	if rec := do(t, h, "POST", "/api/v1/invoices", draft, viewer); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer invoice create = %d, want 403", rec.Code)
	}
	if rec := do(t, h, "POST", "/api/v1/leads", `{"org_id":`+coreID(org)+`,"name":"Lead"}`, viewer); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer lead create = %d, want 403", rec.Code)
	}
	if rec := do(t, h, "GET", "/api/v1/leads?org_id="+coreID(org), "", viewer); rec.Code != http.StatusOK {
		t.Fatalf("viewer read = %d, want 200", rec.Code)
	}

	// SR refused on a non-registered company.
	gst := `{"org_id":` + coreID(org) + `,"contact_id":` + coreID(contact) +
		`,"invoice_date":"2026-10-01","due_date":"2026-10-31","lines":[{"description":"x","quantity":1,"unit_price_cents":100,"gst_code":"SR"}]}`
	if rec := do(t, h, "POST", "/api/v1/invoices", gst, owner); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("SR invoice on non-registered company = %d, want 422", rec.Code)
	}
	// GST-registered company: SR invoice posts with GST Output leg.
	if _, err := pool.Exec(t.Context(), `UPDATE organizations SET gst_registered=true WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	rec := do(t, h, "POST", "/api/v1/invoices", gst, owner)
	if rec.Code != http.StatusCreated {
		t.Fatalf("SR invoice on registered company = %d, want 201", rec.Code)
	}
	sr := decodeBody(t, rec)
	invoiceID := coreID(int64(sr["id"].(float64)))
	if sr["gst_cents"].(float64) != 9 || sr["total_cents"].(float64) != 109 {
		t.Fatalf("SR gst=%v total=%v, want 9 and 109", sr["gst_cents"], sr["total_cents"])
	}
	if rec := do(t, h, "POST", "/api/v1/invoices/"+invoiceID+"/post?org_id="+coreID(org), "", owner); rec.Code != http.StatusOK {
		t.Fatalf("post SR invoice = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var gstOut int64
	if err := pool.QueryRow(t.Context(), `SELECT COALESCE(SUM(jl.credit_cents),0) FROM journal_lines jl JOIN accounts a ON a.id=jl.account_id AND a.org_id=jl.org_id WHERE jl.org_id=$1 AND a.code='2300'`, org).Scan(&gstOut); err != nil {
		t.Fatal(err)
	}
	if gstOut != 9 {
		t.Fatalf("GST Output credit = %d, want 9", gstOut)
	}
	if rec := do(t, h, "POST", "/api/v1/invoices", draft, owner); rec.Code != http.StatusCreated {
		t.Fatalf("NONE invoice on GST-registered company = %d, want 201", rec.Code)
	}
	// F5 needs a registered company; this one is now registered, so the check reaches the handler.
	f5 := `{"org_id":` + coreID(org) + `,"period_start":"2026-01-01","period_end":"2026-03-31","revenue_cents":0,"lines":[{"kind":"sale","code":"SR","net_cents":100,"gst_cents":9}]}`
	if rec := do(t, h, "POST", "/api/v1/tax/gst-f5", f5, owner); rec.Code != http.StatusOK {
		t.Fatalf("F5 for registered = %d, want 200", rec.Code)
	}
}

func TestCoreTrialBalanceNetsAndPaymentBeforeInvoiceRefused(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	tok := seedSession(t, a, "net-bal@example.com")
	org := orgFromList(t, a, tok, `{"name":"Delta Pte Ltd","uen":"","fiscal_year_end":"12-31","gst_registered":false}`)
	contact := seedContact(t, a, org, tok)
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+coreID(org)+`,"contact_id":`+coreID(contact)+
		`,"invoice_date":"2026-10-10","due_date":"2026-10-31","lines":[{"description":"x","quantity":1,"unit_price_cents":10000}]}`, tok), http.StatusCreated)
	id := coreID(int64(inv["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+id+"/post?org_id="+coreID(org), "", tok), http.StatusOK)

	early := `{"org_id":` + coreID(org) + `,"amount_cents":100,"payment_date":"2026-10-01","idempotency_key":"early"}`
	if rec := do(t, h, "POST", "/api/v1/invoices/"+id+"/payments", early, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("payment before invoice date = %d, want 422", rec.Code)
	}
	pay := `{"org_id":` + coreID(org) + `,"amount_cents":4000,"payment_date":"2026-10-20","idempotency_key":"net-1"}`
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+id+"/payments", pay, tok), http.StatusCreated)

	tb := expectStatus(t, do(t, h, "GET", "/api/v1/reports/trial-balance?org_id="+coreID(org)+"&as_of=2026-12-31", "", tok), http.StatusOK)
	want := map[string][2]float64{"1000": {4000, 0}, "1100": {6000, 0}, "4000": {0, 10000}}
	for _, r := range tb["rows"].([]any) {
		row := r.(map[string]any)
		code := row["code"].(string)
		if w, ok := want[code]; ok {
			if row["debit_cents"].(float64) != w[0] || row["credit_cents"].(float64) != w[1] {
				t.Fatalf("%s net = Dr %v Cr %v, want Dr %v Cr %v", code, row["debit_cents"], row["credit_cents"], w[0], w[1])
			}
		}
	}
	if tb["balanced"] != true {
		t.Fatalf("trial balance not balanced: %v", tb)
	}
}
