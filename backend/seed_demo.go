package main

// seed-demo-2025: an explicit operator command that creates one 2025 demonstration company through the
// real HTTP routes, then stops. It never runs at startup, never starts the server, never deletes or
// rewrites data, and is create-only: a company with the same name short-circuits the run.
//
// ponytail: the data is fixed and small (one company, one calendar year); missing features are reported
// as "skipped: <feature> <status>" instead of being worked around. Add a feature when its route exists.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	demoOrgName   = "Demo Trading 2025 Pte Ltd"
	demoSeedLock  = 736427193 // distinct from the shared-sample seed lock
	demoSeedLabel = "demo-2025"
)

// seedDemo2025 needs cfg.AdminEmail to name an existing user; it uses a short-lived session for that
// user so every write goes through the normal permission checks.
func seedDemo2025(ctx context.Context, a *app, out io.Writer) error {
	email, ok := normalizeEmail(a.cfg.AdminEmail)
	if !ok {
		return errors.New("TIDAL_ADMIN_EMAIL must name the platform admin account that owns the demo company")
	}
	var adminID int64
	err := a.db.QueryRow(ctx, `SELECT id FROM users WHERE email=$1 AND NOT disabled`, email).Scan(&adminID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("admin %s does not exist; set TIDAL_ADMIN_EMAIL/TIDAL_ADMIN_PASSWORD and start once", email)
	}
	if err != nil {
		return err
	}

	// A dedicated connection holds the advisory lock, so pooled connections stay free for the routes.
	lock, err := pgx.Connect(ctx, a.db.Config().ConnConfig.ConnString())
	if err != nil {
		return err
	}
	defer lock.Close(context.Background())
	if _, err := lock.Exec(ctx, `SELECT pg_advisory_lock($1)`, demoSeedLock); err != nil {
		return err
	}
	defer lock.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, demoSeedLock)

	var existing int64
	err = a.db.QueryRow(ctx, `SELECT id FROM organizations WHERE name=$1`, demoOrgName).Scan(&existing)
	switch {
	case err == nil:
		var done bool
		if qerr := a.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM demo_seed_runs WHERE org_id=$1 AND label=$2)`, existing, demoSeedLabel).Scan(&done); qerr != nil {
			return qerr
		}
		if !done {
			return fmt.Errorf("%s exists (org %d) without a completed seed marker; fix the cause or remove the company by hand, then rerun", demoOrgName, existing)
		}
		fmt.Fprintf(out, "%s already exists, nothing changed (org %d)\n", demoOrgName, existing)
		return nil
	case !errors.Is(err, pgx.ErrNoRows):
		return err
	}

	token, err := randomString(32)
	if err != nil {
		return err
	}
	if _, err := a.db.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1,$2,now() + interval '15 minutes')`, tokenHash(token), adminID); err != nil {
		return err
	}
	// The temporary session exists only for this run, even when the run fails.
	defer a.db.Exec(context.Background(), `DELETE FROM sessions WHERE token_hash=$1`, tokenHash(token))

	run := &demoRun{a: a, h: a.routes(), token: token, out: out, counts: map[string]int{}}
	if err := run.seed(ctx); err != nil {
		fmt.Fprintf(out, "seed failed: %v\norg id: %d\nno data was deleted; fix the cause or remove the company by hand, then rerun\n", err, run.org)
		return err
	}
	if _, err := a.db.Exec(ctx, `INSERT INTO demo_seed_runs (org_id, label, counts) VALUES ($1,$2,$3) ON CONFLICT (org_id) DO UPDATE SET label=EXCLUDED.label, counts=EXCLUDED.counts, completed_at=now()`, run.org, demoSeedLabel, countsJSON(run.counts)); err != nil {
		return err
	}
	run.report(out)
	return nil
}

type demoRun struct {
	a       *app
	h       http.Handler
	token   string
	out     io.Writer
	org     int64
	counts  map[string]int
	skipped []string
}

func (d *demoRun) req(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", d.a.cfg.PublicOrigin)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: d.token})
	rec := httptest.NewRecorder()
	d.h.ServeHTTP(rec, req)
	return rec
}

// must drives a route the demo depends on; any unexpected status stops the run.
func (d *demoRun) must(label, method, path, body string, want int) (map[string]any, error) {
	rec := d.req(method, path, body)
	if rec.Code != want {
		return nil, fmt.Errorf("%s: %s %s -> %d %s", label, method, path, rec.Code, shortBody(rec.Body.String()))
	}
	return jsonObject(rec.Body.Bytes())
}

// try drives an optional feature: a 4xx is reported once and the run continues; 5xx is a real failure.
func (d *demoRun) try(label, method, path, body string, want int) (map[string]any, error) {
	rec := d.req(method, path, body)
	if rec.Code == want {
		return jsonObject(rec.Body.Bytes())
	}
	if rec.Code >= 400 && rec.Code < 500 {
		line := fmt.Sprintf("%s %d", label, rec.Code)
		d.skipped = append(d.skipped, line)
		fmt.Fprintf(d.out, "skipped: %s\n", line)
		return nil, nil
	}
	return nil, fmt.Errorf("%s: %s %s -> %d %s", label, method, path, rec.Code, shortBody(rec.Body.String()))
}

func (d *demoRun) seed(ctx context.Context) error {
	org, err := d.must("create company", "POST", "/api/v1/organizations",
		`{"name":"`+demoOrgName+`","uen":"","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`, http.StatusCreated)
	if err != nil {
		return err
	}
	d.org = demoID(org)
	o := strconv.FormatInt(d.org, 10)

	customers := []struct{ name, email string }{
		{"Harbourfront Retail Pte Ltd", "ap@harbourfront.example"},
		{"Orchard Digital Studio", "billing@orcharddigital.example"},
		{"Jurong Precision Works", "accounts@jurongprecision.example"},
		{"Sentosa Hospitality Group", "finance@sentosa.example"},
		{"Tampines Learning Centre", "pay@tampineslearning.example"},
		{"Bukit Timah Design Co", "ap@bukittimahdesign.example"},
	}
	customerIDs := make([]int64, 0, len(customers))
	for i, c := range customers {
		m, err := d.must("create customer", "POST", "/api/v1/contacts",
			fmt.Sprintf(`{"org_id":%s,"role":"customer","name":%q,"email":%q,"payment_terms_days":%d}`, o, c.name, c.email, 14+i*3), http.StatusCreated)
		if err != nil {
			return err
		}
		customerIDs = append(customerIDs, demoID(m))
		d.counts["customers"]++
	}

	suppliers := []struct{ name, email string }{
		{"Kallang Wholesale Pte Ltd", "orders@kallangwholesale.example"},
		{"Woodlands Logistics", "billing@woodlandslogistics.example"},
		{"Geylang Printworks", "ap@geylangprint.example"},
		{"Changi Facilities Services", "invoices@changifacilities.example"},
		{"Queenstown Components", "sales@queenstowncomponents.example"},
	}
	supplierIDs := make([]int64, 0, len(suppliers))
	for i, s := range suppliers {
		m, err := d.must("create supplier", "POST", "/api/v1/contacts",
			fmt.Sprintf(`{"org_id":%s,"role":"supplier","name":%q,"email":%q,"payment_terms_days":%d}`, o, s.name, s.email, 10+i*5), http.StatusCreated)
		if err != nil {
			return err
		}
		supplierIDs = append(supplierIDs, demoID(m))
		d.counts["suppliers"]++
	}

	account := func(code, name, typ string) (int64, error) {
		m, err := d.must("create account "+code, "POST", "/api/v1/accounts",
			fmt.Sprintf(`{"org_id":%s,"code":%q,"name":%q,"type":%q}`, o, code, name, typ), http.StatusCreated)
		if err != nil {
			return 0, err
		}
		d.counts["accounts"]++
		return demoID(m), nil
	}
	supplies, err := account("6300", "Supplies and services", "expense")
	if err != nil {
		return err
	}
	rent, err := account("6400", "Rent", "expense")
	if err != nil {
		return err
	}
	savings, err := account("1010", "Savings", "asset")
	if err != nil {
		return err
	}

	// Saving a bill is the first write that makes the app seed its standard accounts (bank, sales).
	gstBill, err := d.must("GST bill", "POST", "/api/v1/bills",
		fmt.Sprintf(`{"org_id":%s,"contact_id":%d,"supplier_reference":"GST-2025-001","bill_date":"2025-03-01","due_date":"2025-03-31","lines":[{"account_id":%d,"description":"Taxed stock","quantity":1,"unit_price_cents":100000,"gst_code":"SR"}]}`, o, supplierIDs[0], supplies), http.StatusCreated)
	if err != nil {
		return err
	}
	if _, err := d.must("GST bill post", "POST", "/api/v1/bills/"+strconv.FormatInt(demoID(gstBill), 10)+"/post?org_id="+o, "", http.StatusOK); err != nil {
		return err
	}
	d.counts["GST bills posted"]++

	// Standard accounts are seeded by the app; read their ids back through the real route.
	accounts, err := d.must("list accounts", "GET", "/api/v1/accounts?org_id="+o, "", http.StatusOK)
	if err != nil {
		return err
	}
	byCode := map[string]int64{}
	for _, row := range accounts["accounts"].([]any) {
		r := row.(map[string]any)
		byCode[r["code"].(string)] = int64(r["id"].(float64))
	}
	bank, sales := byCode["1000"], byCode["4000"]
	if bank == 0 || sales == 0 {
		return errors.New("seeded bank/sales accounts are missing")
	}

	var creditTarget int64
	for m := 1; m <= 12; m++ {
		date := fmt.Sprintf("2025-%02d-15", m)
		due := fmt.Sprintf("2025-%02d-28", m)
		cust := customerIDs[(m-1)%len(customerIDs)]

		// Standard-rated sale, posted every month.
		srInv, err := d.must(fmt.Sprintf("M%02d SR invoice", m), "POST", "/api/v1/invoices",
			fmt.Sprintf(`{"org_id":%s,"contact_id":%d,"invoice_date":%q,"due_date":%q,"lines":[{"description":"Consulting","quantity":1,"unit_price_cents":%d,"gst_code":"SR"}]}`, o, cust, date, due, int64(200000+m*12000)), http.StatusCreated)
		if err != nil {
			return err
		}
		srID := demoID(srInv)
		if _, err := d.must(fmt.Sprintf("M%02d SR post", m), "POST", "/api/v1/invoices/"+strconv.FormatInt(srID, 10)+"/post?org_id="+o, "", http.StatusOK); err != nil {
			return err
		}
		d.counts["sales invoices posted"]++

		// Zero-rated export invoice, posted every month.
		noneInv, err := d.must(fmt.Sprintf("M%02d NONE invoice", m), "POST", "/api/v1/invoices",
			fmt.Sprintf(`{"org_id":%s,"contact_id":%d,"invoice_date":%q,"due_date":%q,"lines":[{"description":"Export services","quantity":1,"unit_price_cents":%d,"gst_code":"NONE"}]}`, o, cust, date, due, int64(100000+m*7000)), http.StatusCreated)
		if err != nil {
			return err
		}
		noneID := demoID(noneInv)
		if _, err := d.must(fmt.Sprintf("M%02d NONE post", m), "POST", "/api/v1/invoices/"+strconv.FormatInt(noneID, 10)+"/post?org_id="+o, "", http.StatusOK); err != nil {
			return err
		}
		d.counts["zero-rated invoices posted"]++

		// Leave a few invoices unpaid so aging shows real overdue balances.
		if m%4 != 0 {
			if _, err := d.must(fmt.Sprintf("M%02d SR payment", m), "POST", "/api/v1/invoices/"+strconv.FormatInt(srID, 10)+"/payments",
				fmt.Sprintf(`{"org_id":%s,"amount_cents":%d,"payment_date":"2025-%02d-20","idempotency_key":"demo-sr-pay-%s-%02d"}`, o, int64(srInv["total_cents"].(float64)), m, o, m), http.StatusCreated); err != nil {
				return err
			}
			d.counts["customer payments"]++
		}
		if m%3 != 0 {
			if _, err := d.must(fmt.Sprintf("M%02d export payment", m), "POST", "/api/v1/invoices/"+strconv.FormatInt(noneID, 10)+"/payments",
				fmt.Sprintf(`{"org_id":%s,"amount_cents":%d,"payment_date":"2025-%02d-22","idempotency_key":"demo-export-pay-%s-%02d"}`, o, int64(noneInv["total_cents"].(float64)), m, o, m), http.StatusCreated); err != nil {
				return err
			}
			d.counts["customer payments"]++
		}

		// Paid expense against the bank account.
		payee := "Office Mart"
		if m%2 == 0 {
			payee = "City Supplies"
		}
		if _, err := d.must(fmt.Sprintf("M%02d expense", m), "POST", "/api/v1/expenses",
			fmt.Sprintf(`{"org_id":%s,"expense_date":%q,"payee":%q,"description":"Monthly supplies","expense_account_id":%d,"amount_cents":%d,"idempotency_key":"demo-exp-%s-%02d"}`, o, date, payee, supplies, int64(30000+m*2500), o, m), http.StatusCreated); err != nil {
			return err
		}
		d.counts["expenses"]++

		// Supplier bill, posted and usually paid; two are left open.
		billNet := int64(500000 + m*20000)
		sup := supplierIDs[(m-1)%len(supplierIDs)]
		bill, err := d.must(fmt.Sprintf("M%02d bill", m), "POST", "/api/v1/bills",
			fmt.Sprintf(`{"org_id":%s,"contact_id":%d,"supplier_reference":"SUP-2025-%02d","bill_date":%q,"due_date":%q,"lines":[{"account_id":%d,"description":"Stock","quantity":1,"unit_price_cents":%d}]}`, o, sup, m, date, due, supplies, billNet), http.StatusCreated)
		if err != nil {
			return err
		}
		billID := demoID(bill)
		if _, err := d.must(fmt.Sprintf("M%02d bill post", m), "POST", "/api/v1/bills/"+strconv.FormatInt(billID, 10)+"/post?org_id="+o, "", http.StatusOK); err != nil {
			return err
		}
		d.counts["supplier bills posted"]++
		if m%5 != 0 {
			if _, err := d.must(fmt.Sprintf("M%02d bill payment", m), "POST", "/api/v1/bills/"+strconv.FormatInt(billID, 10)+"/payments",
				fmt.Sprintf(`{"org_id":%s,"amount_cents":%d,"payment_date":"2025-%02d-25","idempotency_key":"demo-bill-pay-%s-%02d"}`, o, billNet, m, o, m), http.StatusCreated); err != nil {
				return err
			}
			d.counts["supplier bill payments"]++
		}

		// Quote, some sent to the customer.
		quote, err := d.must(fmt.Sprintf("M%02d quote", m), "POST", "/api/v1/quotes",
			fmt.Sprintf(`{"org_id":%s,"contact_id":%d,"quote_date":"2025-%02d-05","expiry_date":%q,"reference":"Q-2025-%02d","lines":[{"description":"Proposal work","quantity":1,"unit_price_cents":%d,"gst_code":"SR"}]}`, o, cust, m, due, m, int64(100000+m*10000)), http.StatusCreated)
		if err != nil {
			return err
		}
		d.counts["quotes"]++
		if m%3 == 0 {
			if _, err := d.must(fmt.Sprintf("M%02d quote sent", m), "POST", "/api/v1/quotes/"+strconv.FormatInt(demoID(quote), 10)+"/stage", fmt.Sprintf(`{"org_id":%s,"stage":"sent"}`, o), http.StatusOK); err != nil {
				return err
			}
		}

		// Cash sale paid straight into the bank.
		if _, err := d.must(fmt.Sprintf("M%02d sales receipt", m), "POST", "/api/v1/sales-receipts",
			fmt.Sprintf(`{"org_id":%s,"contact_id":%d,"receipt_number":"SR-2025-%03d","receipt_date":"2025-%02d-10","payment_mode":"bank_transfer","deposit_account_id":%d,"lines":[{"description":"Walk-in sale","account_id":%d,"quantity":1,"unit_price_cents":%d,"gst_code":"SR"}],"idempotency_key":"demo-receipt-%s-%02d"}`, o, cust, m, m, bank, sales, int64(20000+m*1000), o, m), http.StatusCreated); err != nil {
			return err
		}
		d.counts["sales receipts"]++

		if m == 8 {
			// August's SR invoice is left unpaid, so a later credit note still fits inside it.
			creditTarget = srID
		}
	}

	// Credit note against a posted, unpaid invoice, plus a standalone customer credit note.
	if cn, err := d.try("invoice credit note", "POST", "/api/v1/invoices/"+strconv.FormatInt(creditTarget, 10)+"/credit-notes",
		fmt.Sprintf(`{"org_id":%s,"credit_date":"2025-08-20","reason":"Returned goods","amount_cents":10000}`, o), http.StatusCreated); err != nil {
		return err
	} else if cn != nil {
		d.counts["credit notes"]++
	}
	cn, err := d.try("customer credit note", "POST", "/api/v1/credit-notes",
		fmt.Sprintf(`{"org_id":%s,"contact_id":%d,"credit_number":"CN-2025-001","credit_date":"2025-06-30","lines":[{"description":"Goodwill","account_id":%d,"quantity":1,"unit_price_cents":50000,"gst_code":"SR"}]}`, o, customerIDs[0], sales), http.StatusCreated)
	if err != nil {
		return err
	}
	if cn != nil {
		if _, err := d.try("customer credit note post", "POST", "/api/v1/credit-notes/"+strconv.FormatInt(demoID(cn), 10)+"/stage",
			fmt.Sprintf(`{"org_id":%s,"stage":"open"}`, o), http.StatusOK); err != nil {
			return err
		}
		d.counts["credit notes"]++
	}

	// Two purchase orders, issued to the supplier.
	for i, poDate := range []string{"2025-03-01", "2025-09-01"} {
		po, err := d.try("purchase order", "POST", "/api/v1/purchase-orders",
			fmt.Sprintf(`{"org_id":%s,"contact_id":%d,"po_date":%q,"delivery_date":%q,"lines":[{"description":"Taxed order","account_id":%d,"quantity":1,"unit_price_cents":%d,"gst_code":"SR"}]}`, o, supplierIDs[i], poDate, datePlus(poDate, 14), supplies, int64(100000+i*25000)), http.StatusCreated)
		if err != nil {
			return err
		}
		if po != nil {
			if _, err := d.try("purchase order issue", "POST", "/api/v1/purchase-orders/"+strconv.FormatInt(demoID(po), 10)+"/stage",
				fmt.Sprintf(`{"org_id":%s,"stage":"issued"}`, o), http.StatusOK); err != nil {
				return err
			}
			d.counts["purchase orders"]++
		}
	}

	// Manual accrual journal with an automatic reversal the next day.
	if j, err := d.try("manual journal", "POST", "/api/v1/journals",
		fmt.Sprintf(`{"org_id":%s,"entry_date":"2025-06-30","reversal_date":"2025-07-01","memo":"Accrued rent for June","idempotency_key":"demo-accrual-2025-06","lines":[{"description":"Rent accrual","account_id":%d,"debit_cents":40000},{"description":"Rent accrual","account_id":%d,"credit_cents":40000}]}`, o, rent, byCode[acctAP]), http.StatusCreated); err != nil {
		return err
	} else if j != nil {
		d.counts["manual journals"]++
	}

	// Ledger transfer between two asset accounts (no bank feed in Tidal).
	if tr, err := d.try("bank transfer", "POST", "/api/v1/transfers",
		fmt.Sprintf(`{"org_id":%s,"from_account_id":%d,"to_account_id":%d,"entry_date":"2025-01-31","amount_cents":50000,"reference":"Monthly savings","idempotency_key":"demo-transfer-2025-01"}`, o, bank, savings), http.StatusCreated); err != nil {
		return err
	} else if tr != nil {
		d.counts["transfers"]++
	}

	// Recurring invoice template; drafts only, nothing is posted by saving it.
	if r, err := d.try("recurring invoice template", "POST", "/api/v1/recurring",
		fmt.Sprintf(`{"org_id":%s,"contact_id":%d,"name":"Retainer 2025","frequency":"monthly","start_date":"2025-01-01","term_days":14,"lines":[{"description":"Monthly retainer","quantity":1,"unit_price_cents":80000,"gst_code":"SR"}]}`, o, customerIDs[1]), http.StatusCreated); err != nil {
		return err
	} else if r != nil {
		d.counts["recurring invoice templates"]++
	}

	return d.verify()
}

// verify asserts the demo is coherent after the run: balanced books, every month of 2025 represented,
// no date outside 2025, and the company visible to its owner.
func (d *demoRun) verify() error {
	o := strconv.FormatInt(d.org, 10)
	tb, err := d.must("trial balance", "GET", "/api/v1/reports/trial-balance?org_id="+o+"&as_of=2025-12-31", "", http.StatusOK)
	if err != nil {
		return err
	}
	if balanced, _ := tb["balanced"].(bool); !balanced {
		return errors.New("trial balance at 2025-12-31 is not balanced")
	}

	list, err := d.must("list invoices", "GET", "/api/v1/invoices?org_id="+o+"&status=posted&limit=200", "", http.StatusOK)
	if err != nil {
		return err
	}
	months := map[string]bool{}
	for _, row := range list["invoices"].([]any) {
		inv := row.(map[string]any)
		date, _ := inv["invoice_date"].(string)
		if !strings.HasPrefix(date, "2025-") {
			return fmt.Errorf("posted invoice %v is dated %s, outside 2025", inv["id"], date)
		}
		months[date[:7]] = true
	}
	if len(months) != 12 {
		return fmt.Errorf("posted invoices cover %d months of 2025, want 12", len(months))
	}

	orgs, err := d.must("list organizations", "GET", "/api/v1/organizations", "", http.StatusOK)
	if err != nil {
		return err
	}
	found := false
	for _, row := range orgs["organizations"].([]any) {
		if int64(row.(map[string]any)["id"].(float64)) == d.org {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("company %d is missing from the admin's organization list", d.org)
	}
	return nil
}

var demoCountOrder = []string{
	"customers", "suppliers", "accounts",
	"sales invoices posted", "zero-rated invoices posted", "customer payments",
	"expenses", "supplier bills posted", "supplier bill payments", "GST bills posted",
	"quotes", "sales receipts", "credit notes", "purchase orders",
	"manual journals", "transfers", "recurring invoice templates",
}

func (d *demoRun) report(out io.Writer) {
	fmt.Fprintf(out, "org: %d (%s)\n", d.org, demoOrgName)
	for _, key := range demoCountOrder {
		if n := d.counts[key]; n > 0 {
			fmt.Fprintf(out, "%s: %d\n", key, n)
		}
	}
	if len(d.skipped) > 0 {
		fmt.Fprintf(out, "skipped: %s\n", strings.Join(d.skipped, ", "))
	} else {
		fmt.Fprintln(out, "skipped: none")
	}
}

func demoID(m map[string]any) int64 {
	if v, ok := m["id"].(float64); ok {
		return int64(v)
	}
	return 0
}

func jsonObject(raw []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return m, nil
}

// countsJSON encodes the run summary for the durable marker; counts never contain user data.
func countsJSON(counts map[string]int) []byte {
	b, err := json.Marshal(counts)
	if err != nil {
		return []byte("{}")
	}
	return b
}

func shortBody(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// datePlus adds days to a YYYY-MM-DD date; the demo only uses dates that exist in 2025.
func datePlus(day string, days int) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	return t.AddDate(0, 0, days).Format("2006-01-02")
}
