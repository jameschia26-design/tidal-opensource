//go:build samplesim

// Run only on purpose against an EMPTY disposable database:
//
//	TEST_DATABASE_URL=.../tidal_sim go test -tags samplesim -run TestSimulateSampleYear -count=1 -v .
//
// Drives FY2025 month by month through the real routes and logs each status or refusal.
// The run creates a throwaway schema inside that database and drops only that schema; it never
// drops or rewrites an existing company. The database name must still look disposable
// (test, sim, scratch or demo), so pointing this at a production database fails before work starts.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func isolatedSimApp(t *testing.T) (*app, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var dbName string
	if err := admin.QueryRow(ctx, `SELECT current_database()`).Scan(&dbName); err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(dbName)
	if !strings.Contains(lower, "test") && !strings.Contains(lower, "sim") &&
		!strings.Contains(lower, "scratch") && !strings.Contains(lower, "demo") {
		t.Fatalf("refusing to simulate in database %q: name must identify a disposable database (test/sim/scratch/demo)", dbName)
	}

	schema := fmt.Sprintf("sim_%d_%d", time.Now().UnixNano(), os.Getpid())
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		admin.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
	})
	if err := migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	cfgApp := config{PublicOrigin: "http://app.test"}
	return &app{cfg: cfgApp, db: pool, mail: newMailer(cfgApp)}, pool
}

func TestSimulateSampleYear(t *testing.T) {
	a, _ := isolatedSimApp(t)
	h := a.routes()
	tok := seedSession(t, a, "sample-owner@example.com")
	org := orgFromList(t, a, tok, `{"name":"SAMPLE - Demo Trading Pte Ltd (not a real company)","uen":"","fiscal_year_end":"12-31","gst_registered":true}`)
	contact := seedContact(t, a, org, tok)
	supplier := expectStatus(t, do(t, a.routes(), "POST", "/api/v1/contacts", `{"org_id":`+coreID(org)+`,"role":"supplier","name":"Sample Supplier Pte"}`, tok), http.StatusCreated)
	o := coreID(org)
	c := coreID(contact)
	sup := coreID(int64(supplier["id"].(float64)))
	// Create a supplies expense account through the real route.
	acct := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6300","name":"Supplies and services","type":"expense"}`, tok), 201)
	expenseAccount := int64(acct["id"].(float64))

	try := func(label, method, path, body string) int {
		rec := do(t, h, method, path, body, tok)
		t.Logf("%-28s %-4s %-28s -> %d %s", label, method, path, rec.Code, rec.Body.String())
		return rec.Code
	}

	var invoiceIDs []int64
	for m := 1; m <= 12; m++ {
		date := fmt.Sprintf("2025-%02d-15", m)
		due := fmt.Sprintf("2025-%02d-28", m)
		// Create and post standard-rated and no-tax sales invoices.
		for _, item := range []struct {
			label, desc, code string
			cents             int64
		}{{"SR", "Consulting", "SR", 200000}, {"NONE", "Export", "NONE", 100000}} {
			body := fmt.Sprintf(`{"org_id":%s,"contact_id":%s,"invoice_date":"%s","due_date":"%s","lines":[{"description":%q,"quantity":1,"unit_price_cents":%d,"gst_code":%q}]}`, o, c, date, due, item.desc, item.cents, item.code)
			rec := do(t, h, "POST", "/api/v1/invoices", body, tok)
			if rec.Code != http.StatusCreated {
				t.Fatalf("M%02d invoice %s: %d %s", m, item.label, rec.Code, rec.Body.String())
			}
			invoice := decodeBody(t, rec)
			id := int64(invoice["id"].(float64))
			invoiceIDs = append(invoiceIDs, id)
			post := do(t, h, "POST", "/api/v1/invoices/"+coreID(id)+"/post?org_id="+o, "", tok)
			if post.Code != http.StatusOK {
				t.Fatalf("M%02d post %s: %d %s", m, item.label, post.Code, post.Body.String())
			}
		}
		// Supplier bill and expense, both untaxed (GST input is not yet supported on these paths).
		if code := try(fmt.Sprintf("M%02d expense", m), "POST", "/api/v1/expenses",
			`{"org_id":`+o+`,"expense_date":"`+date+`","payee":"Office Mart","description":"Supplies","expense_account_id":`+coreID(expenseAccount)+`,"amount_cents":30000,"idempotency_key":"exp-`+o+`-`+fmt.Sprintf("%02d", m)+`"}`); code != http.StatusCreated {
			t.Fatalf("M%02d expense: %d", m, code)
		}
		// Receive payment for the posted standard-rated invoice.
		srID := invoiceIDs[(m-1)*2]
		payBody := fmt.Sprintf(`{"org_id":%s,"amount_cents":218000,"payment_date":"2025-%02d-20","idempotency_key":"sample-invoice-payment-%02d"}`, o, m, m)
		pay := do(t, h, "POST", "/api/v1/invoices/"+coreID(srID)+"/payments", payBody, tok)
		if pay.Code != http.StatusCreated {
			t.Fatalf("M%02d invoice payment: %d %s", m, pay.Code, pay.Body.String())
		}
	}

	for m := 1; m <= 12; m++ {
		date := fmt.Sprintf("2025-%02d-22", m)
		// Supplier bill (untaxed), post, then pay in full.
		billRec := do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+sup+`,"supplier_reference":"SUP-`+fmt.Sprintf("%02d", m)+`","bill_date":"`+date+`","due_date":"2025-`+fmt.Sprintf("%02d", m)+`-28","lines":[{"account_id":`+coreID(expenseAccount)+`,"description":"Stock","quantity":1,"unit_price_cents":500000}]}`, tok)
		if billRec.Code != http.StatusCreated {
			t.Fatalf("M%02d bill create: %d %s", m, billRec.Code, billRec.Body.String())
		}
		bill := decodeBody(t, billRec)
		billID := coreID(int64(bill["id"].(float64)))
		post := do(t, h, "POST", "/api/v1/bills/"+billID+"/post?org_id="+o, "", tok)
		if post.Code != http.StatusOK {
			t.Fatalf("M%02d bill post: %d %s", m, post.Code, post.Body.String())
		}
		pay := do(t, h, "POST", "/api/v1/bills/"+billID+"/payments", `{"org_id":`+o+`,"amount_cents":500000,"payment_date":"2025-`+fmt.Sprintf("%02d", m)+`-25","idempotency_key":"sample-bill-payment-`+fmt.Sprintf("%02d", m)+`"}`, tok)
		if pay.Code != http.StatusCreated {
			t.Fatalf("M%02d bill payment: %d %s", m, pay.Code, pay.Body.String())
		}
	}

	// Credit note against a real posted invoice (the untaxed one, so no GST reversal is expected).
	m := expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+coreID(invoiceIDs[1])+"/credit-notes",
		`{"org_id":`+o+`,"credit_date":"2025-07-01","reason":"Return","amount_cents":10000}`, tok), http.StatusCreated)
	_ = m

	var bankID, salesID int64
	if err := a.db.QueryRow(t.Context(), `SELECT id FROM accounts WHERE org_id=$1 AND code='1000'`, org).Scan(&bankID); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(t.Context(), `SELECT id FROM accounts WHERE org_id=$1 AND code='4000'`, org).Scan(&salesID); err != nil {
		t.Fatal(err)
	}

	// Standalone customer credit note: save, then post. Standard-rated line reverses GST output.
	cn := expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes",
		`{"org_id":`+o+`,"contact_id":`+c+`,"credit_number":"CN-SAMPLE-001","credit_date":"2025-06-30","lines":[{"description":"Goodwill","account_id":`+coreID(salesID)+`,"quantity":1,"unit_price_cents":50000,"gst_code":"SR"}]}`, tok), http.StatusCreated)
	cnID := coreID(int64(cn["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes/"+cnID+"/stage", `{"org_id":`+o+`,"stage":"open"}`, tok), http.StatusOK)

	for m := 1; m <= 12; m++ {
		date := fmt.Sprintf("2025-%02d-10", m)
		if code := try(fmt.Sprintf("M%02d quote SR", m), "POST", "/api/v1/quotes",
			`{"org_id":`+o+`,"contact_id":`+c+`,"quote_date":"`+date+`","expiry_date":"2025-`+fmt.Sprintf("%02d", m)+`-28","lines":[{"description":"Quote work","quantity":1,"unit_price_cents":100000,"gst_code":"SR"}]}`); code != http.StatusCreated {
			t.Fatalf("M%02d quote: %d", m, code)
		}
		if code := try(fmt.Sprintf("M%02d sales receipt SR", m), "POST", "/api/v1/sales-receipts",
			`{"org_id":`+o+`,"contact_id":`+c+`,"receipt_number":"SR-`+fmt.Sprintf("%02d", m)+`","receipt_date":"`+date+`","payment_mode":"bank_transfer","deposit_account_id":`+coreID(bankID)+`,"lines":[{"description":"Cash sale","account_id":`+coreID(salesID)+`,"quantity":1,"unit_price_cents":20000,"gst_code":"SR"}],"idempotency_key":"sr-`+o+`-`+fmt.Sprintf("%02d", m)+`-x"}`); code != http.StatusCreated {
			t.Fatalf("M%02d sales receipt: %d", m, code)
		}
	}

	// Supplier bill with GST: saves with SR line, posts claiming GST input as a tagged debit on the one GST control account.
	billGST := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+sup+`,"supplier_reference":"GST-BILL","bill_date":"2025-03-01","due_date":"2025-03-31","lines":[{"account_id":`+coreID(expenseAccount)+`,"description":"Taxed stock","quantity":1,"unit_price_cents":100000,"gst_code":"SR"}]}`, tok), http.StatusCreated)
	billGSTID := coreID(int64(billGST["id"].(float64)))
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+billGSTID+"/post?org_id="+o, "", tok), http.StatusOK)

	// Purchase order with GST: saves with SR line (receive path separate).
	po := expectStatus(t, do(t, h, "POST", "/api/v1/purchase-orders", `{"org_id":`+o+`,"contact_id":`+sup+`,"po_date":"2025-03-01","delivery_date":"2025-03-15","lines":[{"description":"Taxed order","account_id":`+coreID(expenseAccount)+`,"quantity":1,"unit_price_cents":100000,"gst_code":"SR"}]}`, tok), http.StatusCreated)
	_ = po

	// Derive SR F5 totals from posted invoices, not hand-entered figures.
	var net, gst int64
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(sum(l.amount_cents),0),COALESCE(sum(l.gst_cents),0) FROM invoice_lines l JOIN invoices i ON i.org_id=l.org_id AND i.id=l.invoice_id WHERE i.org_id=$1 AND i.status='posted' AND i.invoice_date BETWEEN '2025-01-01' AND '2025-12-31' AND l.gst_code='SR'`, org).Scan(&net, &gst); err != nil {
		t.Fatal(err)
	}
	if net != 2400000 || gst != 216000 {
		t.Fatalf("posted SR totals net=%d gst=%d, want 2400000 and 216000", net, gst)
	}
	if code := try("gst f5 FY2025", "POST", "/api/v1/tax/gst-f5",
		fmt.Sprintf(`{"org_id":%s,"period_start":"2025-01-01","period_end":"2025-12-31","revenue_cents":0,"lines":[{"kind":"sale","code":"SR","net_cents":%d,"gst_cents":%d}]}`, o, net, gst)); code != http.StatusOK {
		t.Fatalf("GST F5: %d", code)
	}
	tb := do(t, h, "GET", "/api/v1/reports/trial-balance?org_id="+o+"&as_of=2025-12-31", "", tok)
	if tb.Code != 200 || !decodeBody(t, tb)["balanced"].(bool) {
		t.Fatalf("trial balance: %d %s", tb.Code, tb.Body.String())
	}
	var output int64
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(sum(jl.credit_cents-jl.debit_cents),0) FROM journal_lines jl JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id WHERE jl.org_id=$1 AND a.code='2300' AND COALESCE(jl.tax_direction,'output')='output' AND j.entry_date BETWEEN '2025-01-01' AND '2025-12-31'`, org).Scan(&output); err != nil {
		t.Fatal(err)
	}
	// Ledger GST output = invoice GST + 12 receipts at 1,800 each - the 4,500 credit-note reversal.
	if want := gst + 12*1800 - 4500; output != want {
		t.Fatalf("GST Output net=%d, want %d (invoice GST plus receipts less credit note)", output, want)
	}

	// Another company must not see this demonstration company's records.
	otherTok := seedSession(t, a, "other-owner@example.com")
	orgFromList(t, a, otherTok, `{"name":"Other Company Pte Ltd","uen":"","fiscal_year_end":"12-31","gst_registered":false}`)
	otherList := do(t, h, "GET", "/api/v1/invoices?org_id="+o, "", otherTok)
	if otherList.Code != http.StatusNotFound && otherList.Code != http.StatusForbidden {
		t.Fatalf("other company read demo invoices: %d %s", otherList.Code, otherList.Body.String())
	}
	t.Logf("cross-company invoice read -> %d (isolated)", otherList.Code)
}
