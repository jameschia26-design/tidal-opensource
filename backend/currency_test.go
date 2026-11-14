package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDocumentCurrency covers the currency and rate a document is issued in: SGD is the default,
// a foreign document must carry its own rate, and both are stored and read back on invoices and bills.
func TestDocumentCurrency(t *testing.T) {
	for _, c := range []struct {
		currency string
		rate     int64
		ok       bool
	}{
		{"", 0, true}, // blank means SGD
		{"SGD", 1000000, true},
		{"sgd", 1000000, true}, // case is normalised
		{"USD", 1350000, true},
		{"USD", 0, false},        // a foreign document needs a rate
		{"USD", 1000000, false},  // a rate of 1 is not foreign
		{"SGD", 1350000, false},  // SGD cannot have a foreign rate
		{"US", 1350000, false},   // not a currency code
		{"US1", 1350000, false},  // digits are not a currency code
		{"USD", 999, false},      // absurdly small rate
		{"USD", 1e12 + 1, false}, // absurdly large rate
	} {
		currency, rate, ok := documentCurrency(c.currency, c.rate)
		if ok != c.ok {
			t.Fatalf("documentCurrency(%q, %d) ok=%v, want %v", c.currency, c.rate, ok, c.ok)
		}
		if ok && (currency == "" || rate <= 0) {
			t.Fatalf("documentCurrency(%q, %d) returned %q/%d", c.currency, c.rate, currency, rate)
		}
	}
}

func TestInvoiceAndBillCurrency(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "fx-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"FX co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := id(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"USD buyer"}`, owner))
	sup := id(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"USD seller"}`, owner))

	invoice := func(body string) *httptest.ResponseRecorder {
		return do(t, h, "POST", "/api/v1/invoices", body, owner)
	}
	base := `{"org_id":` + o + `,"contact_id":` + itoa(cust) + `,"invoice_date":"2026-04-01","due_date":"2026-04-30","lines":[{"description":"Design","quantity":1,"unit_price_cents":10000}]`
	// SGD by default, and a foreign invoice keeps its currency and rate.
	sgd := expectStatus(t, invoice(base+`}`), 201)
	if sgd["currency"] != "SGD" || sgd["fx_rate_micro"].(float64) != 1000000 {
		t.Fatalf("default currency: %v", sgd)
	}
	usd := expectStatus(t, invoice(base+`,"currency":"USD","fx_rate_micro":1350000}`), 201)
	if usd["currency"] != "USD" || usd["fx_rate_micro"].(float64) != 1350000 {
		t.Fatalf("foreign invoice: %v", usd)
	}
	// A foreign invoice without a rate, and a bad code, are refused.
	expectStatus(t, invoice(base+`,"currency":"USD"}`), 400)
	expectStatus(t, invoice(base+`,"currency":"US"}`), 400)

	invID := int64(usd["id"].(float64))
	got := expectStatus(t, do(t, h, "GET", "/api/v1/invoices/"+itoa(invID)+"?org_id="+o, "", owner), 200)["invoice"].(map[string]any)
	if got["currency"] != "USD" || got["fx_rate_micro"].(float64) != 1350000 {
		t.Fatalf("invoice read back: %v", got)
	}

	bill := func(body string) *httptest.ResponseRecorder {
		return do(t, h, "POST", "/api/v1/bills", body, owner)
	}
	billBase := `{"org_id":` + o + `,"contact_id":` + itoa(sup) + `,"supplier_reference":"FX-1","bill_date":"2026-04-01","due_date":"2026-04-30","lines":[{"account_id":` + itoa(id(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6100","name":"Supplies","type":"expense"}`, owner))) + `,"description":"Parts","quantity":1,"unit_price_cents":10000}]`
	foreign := expectStatus(t, bill(billBase+`,"currency":"USD","fx_rate_micro":1350000}`), 201)
	billID := int64(foreign["id"].(float64))
	read := expectStatus(t, do(t, h, "GET", "/api/v1/bills/"+itoa(billID)+"?org_id="+o, "", owner), 200)["bill"].(map[string]any)
	if read["currency"] != "USD" || read["fx_rate_micro"].(float64) != 1350000 {
		t.Fatalf("bill read back: %v", read)
	}
	expectStatus(t, bill(billBase+`,"currency":"EUR"}`), 400)

	// Posting both documents proves the books stay in SGD: the ledger carries the document total,
	// unchanged by the currency it was struck in.
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(invID)+"/post?org_id="+o, `{}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(billID)+"/post?org_id="+o, `{}`, owner), 200)
	var debit int64
	if err := pool.QueryRow(t.Context(), `SELECT COALESCE(sum(debit_cents),0) FROM journal_lines WHERE org_id=$1`, org).Scan(&debit); err != nil {
		t.Fatal(err)
	}
	if debit != 20000 {
		t.Fatalf("ledger debit %d, want 20000 (both documents at their SGD totals)", debit)
	}
}

// id returns the id from a created record, failing the test when the call did not succeed.
func id(t *testing.T, rec *httptest.ResponseRecorder) int64 {
	t.Helper()
	body := expectStatus(t, rec, http.StatusCreated)
	v, ok := body["id"].(float64)
	if !ok {
		t.Fatalf("no id in %v", body)
	}
	return int64(v)
}
