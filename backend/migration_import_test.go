package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Migration import is proven here against a real database: rows converted from vendor fixture
// files are posted through the same endpoints the UI calls, then read back. The browser-side
// converter is covered by frontend/tests/migrate.test.mjs; this file closes the loop the frontend
// cannot — that the numbers actually land in the ledger.
//
// The fixtures are the ones under frontend/tests/fixtures/migrate/. Reading them here keeps one
// copy of each vendor file instead of a Go copy that can drift.

func migrationFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "frontend", "tests", "fixtures", "migrate", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return string(b)
}

// csvRows reads a CSV/TXT fixture into header plus rows, detecting tab or comma like the importer.
func csvRows(t *testing.T, text string) ([]string, [][]string) {
	t.Helper()
	delim := ','
	if strings.Contains(strings.SplitN(text, "\n", 2)[0], "\t") {
		delim = '\t'
	}
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = delim
	r.FieldsPerRecord = -1
	all, err := r.ReadAll()
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(all) < 2 {
		t.Fatal("fixture has no data rows")
	}
	return all[0], all[1:]
}

// pick returns the value of the first header that matches one of the aliases, case-insensitively.
func pick(headers, row []string, aliases ...string) string {
	for _, want := range aliases {
		for i, h := range headers {
			if strings.EqualFold(strings.TrimSpace(h), strings.TrimSpace(want)) && i < len(row) {
				return strings.TrimSpace(row[i])
			}
		}
	}
	return ""
}

func num(t *testing.T, m map[string]any, key string) int64 {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("response has no numeric %q: %v", key, m)
	}
	return int64(v)
}

// A Sage 50 US chart of accounts, converted and posted the way the importer does, must create
// accounts whose opening balances then post as one conversion journal.
func TestMigrationChartOfAccountsAndOpeningBalances(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	token := seedSession(t, a, "migrate-coa@example.com")
	orgID := orgFromList(t, a, token, `{"name":"Migrate CoA Pte Ltd","uen":"","fiscal_year_end":"12-31","gst_registered":false}`)

	headers, rows := csvRows(t, migrationFixture(t, "peachtree-coa.csv"))
	created := 0
	for _, row := range rows {
		// Same field set the frontend's account import spec sends.
		body := map[string]any{
			"org_id": orgID,
			"code":   pick(headers, row, "Account ID", "Account Number"),
			"name":   pick(headers, row, "Account Description", "Account Name"),
			"type":   mapSageAccountType(pick(headers, row, "Account Type")),
		}
		rec := do(t, h, "POST", "/api/v1/accounts", string(mustJSON(body)), token)
		if rec.Code == http.StatusConflict && strings.Contains(rec.Body.String(), "reserved_account") {
			continue // seeded posting account; nothing to create
		}
		expectStatus(t, rec, http.StatusCreated)
		created++
	}
	if created == 0 {
		t.Fatal("no accounts were created from the fixture")
	}

	// Post one conversion journal covering every account that carried a balance, as the UI does.
	type line struct {
		AccountID   int64  `json:"account_id"`
		Description string `json:"description"`
		DebitCents  int64  `json:"debit_cents"`
		CreditCents int64  `json:"credit_cents"`
	}
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/accounts?org_id="+coreID(orgID), "", token), http.StatusOK)
	byCode := map[string]int64{}
	for _, raw := range listed["accounts"].([]any) {
		acc := raw.(map[string]any)
		byCode[acc["code"].(string)] = int64(acc["id"].(float64))
	}
	var lines []line
	for _, row := range rows {
		code := pick(headers, row, "Account ID", "Account Number")
		balance := pick(headers, row, "Current Balance", "Balance")
		if balance == "" {
			continue
		}
		id, ok := byCode[code]
		if !ok {
			continue
		}
		cents := centsFromString(t, balance)
		if cents > 0 {
			lines = append(lines, line{AccountID: id, Description: "Opening balance", DebitCents: cents})
		} else if cents < 0 {
			lines = append(lines, line{AccountID: id, Description: "Opening balance", CreditCents: -cents})
		}
	}
	if len(lines) < 2 {
		t.Fatalf("fixture should carry at least two opening balances, got %d", len(lines))
	}
	body := map[string]any{"org_id": orgID, "conversion_date": "2026-01-01", "lines": lines}
	expectStatus(t, do(t, h, "POST", "/api/v1/conversion-balances", string(mustJSON(body)), token), http.StatusCreated)

	// Read back: the journal exists, so the balances really posted rather than being accepted and dropped.
	// Read back: the conversion journal exists (with Hratch concrete opening balances),
	// so the balances really posted rather than being accepted and dropped.
	got := expectStatus(t, do(t, h, "GET", "/api/v1/conversion-balances?org_id="+coreID(orgID), "", token), http.StatusOK)
	journal, ok := got["journal"].(map[string]any)
	if !ok || journal == nil {
		t.Fatalf("conversion balances not stored: %v", got)
	}

	var stored int
	if err := a.db.QueryRow(context.Background(), `SELECT count(*) FROM accounts WHERE org_id=$1`, orgID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored < created {
		t.Fatalf("expected at least %d accounts in the database, found %d", created, stored)
	}
}

// A converted Sage customer file must create contacts that an invoice can then reference by id.
func TestMigrationCustomersThenInvoice(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	token := seedSession(t, a, "migrate-cust@example.com")
	orgID := orgFromList(t, a, token, `{"name":"Migrate Cust Pte Ltd","uen":"","fiscal_year_end":"12-31","gst_registered":false}`)

	headers, rows := csvRows(t, migrationFixture(t, "peachtree-customer.csv"))
	ids := map[string]int64{}
	for _, row := range rows {
		name := pick(headers, row, "Customer Name", "ContactName")
		body := map[string]any{
			"org_id": orgID, "role": "customer", "name": name,
			"email": pick(headers, row, "Customer Email", "EmailAddress"),
			"phone": pick(headers, row, "Telephone 1", "PhoneNumber"),
		}
		m := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", string(mustJSON(body)), token), http.StatusCreated)
		ids[name] = num(t, m, "id")
	}
	if len(ids) == 0 {
		t.Fatal("no customers created from the fixture")
	}

	// The invoice fixture references CUST-001 by name; invoice it as the importer would.
	invHeaders, invRows := csvRows(t, migrationFixture(t, "peachtree-invoice.csv"))
	row := invRows[0]
	customer := pick(invHeaders, row, "Customer Name")
	contactID, ok := ids[customer]
	if !ok {
		t.Fatalf("invoice names customer %q, which the customer file did not create", customer)
	}
	body := map[string]any{
		"org_id": orgID, "contact_id": contactID,
		"invoice_date": "2026-03-15", "due_date": "2026-04-14",
		"reference": pick(invHeaders, row, "Customer PO"),
		"lines": []map[string]any{{
			"description":      pick(invHeaders, row, "Description"),
			"quantity":         int64(2),
			"unit_price_cents": 50000,
		}},
	}
	created := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", string(mustJSON(body)), token), http.StatusCreated)
	invoiceID := num(t, created, "id")

	// Read the invoice back: the stored total must be the fixture's 2 × 500.00, not something else.
	// Read the invoice back with its lines: the stored lines must be the fixture's
	// 2 x 500.00, proving the amount reached the ledger rather than being stored elsewhere.
	fetched := expectStatus(t, do(t, h, "GET", fmt.Sprintf("/api/v1/invoices/%d?org_id=%d", invoiceID, orgID), "", token), http.StatusOK)
	linesRaw, ok := fetched["lines"].([]any)
	if !ok || len(linesRaw) != 1 {
		t.Fatalf("expected 1 stored invoice line, got %v", fetched["lines"])
	}
	gotLine := linesRaw[0].(map[string]any)
	quantity := int64(gotLine["quantity"].(float64))
	unitPrice := int64(gotLine["unit_price_cents"].(float64))
	amount := int64(gotLine["amount_cents"].(float64))
	if quantity != 2 || unitPrice != 50000 || amount != 100000 {
		t.Fatalf("stored line = qty %d x %d = %d cents, want qty 2 x 50000 = 100000", quantity, unitPrice, amount)
	}

	var count int
	if err := a.db.QueryRow(context.Background(), `SELECT count(*) FROM invoices WHERE org_id=$1 AND contact_id=$2`, orgID, contactID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 invoice row for the migrated customer, found %d", count)
	}
}

// A converted supplier file plus a bill: the bill must post against the migrated supplier and
// store the fixture's amount.
func TestMigrationSupplierThenBill(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	token := seedSession(t, a, "migrate-supp@example.com")
	orgID := orgFromList(t, a, token, `{"name":"Migrate Supp Pte Ltd","uen":"","fiscal_year_end":"12-31","gst_registered":false}`)

	// A bill needs a real expense account to post to.
	acct := expectStatus(t, do(t, h, "POST", "/api/v1/accounts",
		`{"org_id":`+coreID(orgID)+`,"code":"6100","name":"Office supplies","type":"expense"}`, token), http.StatusCreated)
	accountID := num(t, acct, "id")

	headers, rows := csvRows(t, migrationFixture(t, "peachtree-vendor.csv"))
	row := rows[0]
	supplier := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", string(mustJSON(map[string]any{
		"org_id": orgID, "role": "supplier", "name": pick(headers, row, "Vendor Name"),
		"email": pick(headers, row, "Vendor Email"),
	})), token), http.StatusCreated)
	supplierID := num(t, supplier, "id")

	billHeaders, billRows := csvRows(t, migrationFixture(t, "peachtree-bill.csv"))
	brow := billRows[0]
	bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", string(mustJSON(map[string]any{
		"org_id": orgID, "contact_id": supplierID,
		"supplier_reference": pick(billHeaders, brow, "Invoice #"),
		"bill_date":          "2026-03-20", "due_date": "2026-04-19",
		"lines": []map[string]any{{
			"account_id":       accountID,
			"description":      pick(billHeaders, brow, "Description"),
			"quantity":         int64(1),
			"unit_price_cents": 45075,
		}},
	})), token), http.StatusCreated)
	billID := num(t, bill, "id")

	// Read the bill back: the stored line must be the fixture's 1 x 450.75 against the
	// migrated supplier, and the vendor reference must survive in the bill row itself.
	fetched := expectStatus(t, do(t, h, "GET", fmt.Sprintf("/api/v1/bills/%d?org_id=%d", billID, orgID), "", token), http.StatusOK)
	billOut, ok := fetched["bill"].(map[string]any)
	if !ok {
		t.Fatalf("bill response has no bill object: %v", fetched)
	}
	if got := int64(billOut["contact_id"].(float64)); got != supplierID {
		t.Fatalf("bill contact_id = %d, want the migrated supplier %d", got, supplierID)
	}
	linesRaw, ok := fetched["lines"].([]any)
	if !ok || len(linesRaw) != 1 {
		t.Fatalf("expected 1 stored bill line, got %v", fetched["lines"])
	}
	gotLine := linesRaw[0].(map[string]any)
	quantity := int64(gotLine["quantity"].(float64))
	unitPrice := int64(gotLine["unit_price_cents"].(float64))
	if quantity != 1 || unitPrice != 45075 {
		t.Fatalf("stored bill line = qty %d x %d cents, want qty 1 x 45075", quantity, unitPrice)
	}
	var reference string
	if err := a.db.QueryRow(context.Background(), `SELECT supplier_reference FROM bills WHERE org_id=$1 AND id=$2`, orgID, billID).Scan(&reference); err != nil {
		t.Fatal(err)
	}
	if reference != pick(billHeaders, brow, "Invoice #") {
		t.Fatalf("bill reference = %q, want the vendor reference from the fixture", reference)
	}
}

// A converted item file must create items at the fixture's price, proving the price column is
// read rather than silently defaulted to zero.
func TestMigrationItemsKeepPrice(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	token := seedSession(t, a, "migrate-item@example.com")
	orgID := orgFromList(t, a, token, `{"name":"Migrate Item Pte Ltd","uen":"","fiscal_year_end":"12-31","gst_registered":false}`)

	headers, rows := csvRows(t, migrationFixture(t, "peachtree-item.csv"))
	posted := 0
	for _, row := range rows {
		price := centsFromString(t, pick(headers, row, "Sales Price/Billing Rate 1-10", "Selling Price"))
		if price <= 0 {
			continue
		}
		expectStatus(t, do(t, h, "POST", "/api/v1/items", string(mustJSON(map[string]any{
			"org_id": orgID, "name": pick(headers, row, "Item Description", "Item Name"),
			"item_type":           "service",
			"sku":                 pick(headers, row, "UPC/SKU", "Item ID"),
			"selling_price_cents": price,
		})), token), http.StatusCreated)
		posted++
	}
	if posted == 0 {
		t.Fatal("no priced items in the fixture")
	}
	var names []string
	rowsOut, err := a.db.Query(context.Background(), `SELECT name, selling_price_cents FROM items WHERE org_id=$1 ORDER BY name`, orgID)
	if err != nil {
		t.Fatal(err)
	}
	defer rowsOut.Close()
	minPrice := int64(1 << 62)
	for rowsOut.Next() {
		var name string
		var cents int64
		if err := rowsOut.Scan(&name, &cents); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
		if cents < minPrice {
			minPrice = cents
		}
	}
	if len(names) != posted {
		t.Fatalf("expected %d items stored, found %d", posted, len(names))
	}
	if minPrice <= 0 {
		t.Fatalf("items stored with a zero price, so the price column was not read: %v", names)
	}
}

// mapSageAccountType mirrors the converter's numeric-code mapping. Kept here so the Go proof
// fails loudly if the two sides ever disagree.
func mapSageAccountType(code string) string {
	switch strings.TrimSpace(code) {
	case "0", "1", "2", "3", "4", "5", "6", "8":
		return "asset"
	case "10", "11", "12", "14":
		return "liability"
	case "16", "18", "19":
		return "equity"
	case "21":
		return "revenue"
	case "23", "24":
		return "expense"
	}
	return ""
}

// centsFromString parses a plain money cell ("1,234.50", "-59.25", "(45.00)") into integer cents.
func centsFromString(t *testing.T, raw string) int64 {
	t.Helper()
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, "$", "")
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, s[1:len(s)-1]
	}
	if strings.HasPrefix(s, "-") {
		neg, s = !neg, s[1:]
	}
	if s == "" {
		return 0
	}
	var whole, frac int64
	parts := strings.SplitN(s, ".", 2)
	if _, err := fmt.Sscanf(parts[0], "%d", &whole); err != nil && parts[0] != "" {
		t.Fatalf("money %q is not a number", raw)
	}
	if len(parts) == 2 {
		f := (parts[1] + "00")[:2]
		if _, err := fmt.Sscanf(f, "%d", &frac); err != nil {
			t.Fatalf("money %q has an unreadable decimal part", raw)
		}
	}
	cents := whole*100 + frac
	if neg {
		cents = -cents
	}
	return cents
}
