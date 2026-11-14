package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestBillPostingPaymentAndIsolation(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	ctx := t.Context()
	owner := seedSession(t, a, "bill-owner@example.com")
	viewer := seedSession(t, a, "bill-viewer@example.com")
	outsider := seedSession(t, a, "bill-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Buyer","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='bill-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	supplier := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+coreID(org)+`,"role":"supplier","name":"Original supplier"}`, owner), 201)
	account := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+coreID(org)+`,"code":"6100","name":"Services","type":"expense"}`, owner), 201)
	in := billInput{OrgID: org, ContactID: int64(supplier["id"].(float64)), SupplierReference: " SUP-1 ", BillDate: "2026-10-01", DueDate: "2026-10-31", Notes: "Notes", Lines: []billLineIn{{AccountID: int64(account["id"].(float64)), Description: "Service", Quantity: 2, UnitPriceCents: 100}}}
	body := func() string {
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/bills", body(), viewer), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills", body(), outsider), 403)
	created := expectStatus(t, do(t, h, "POST", "/api/v1/bills", body(), owner), 201)
	id := int64(created["id"].(float64))
	pdf := do(t, h, "GET", "/api/v1/documents/bills/"+coreID(id)+"/pdf?org_id="+coreID(org), "", owner)
	if pdf.Code != 200 || strings.Contains(pdf.Body.String(), "(Notes)") {
		t.Fatal("bill export must not contain internal notes", pdf.Code)
	}
	url := "/api/v1/bills/" + coreID(id)
	query := "?org_id=" + coreID(org)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills", body(), owner), 409)
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/bills"+query, "", viewer), 200)
	if len(listed["bills"].([]any)) != 1 {
		t.Fatalf("bill list=%v", listed)
	}
	in.Lines[0].Quantity = 3
	in.OrderNumber, in.Subject, in.PaymentTermsDays = "PO-9", "October services", 30
	expectStatus(t, do(t, h, "PUT", url, body(), viewer), 403)
	in.Subject = strings.Repeat("x", 201)
	expectStatus(t, do(t, h, "PUT", url, body(), owner), 400)
	in.Subject = "October services"
	expectStatus(t, do(t, h, "PUT", url, body(), owner), 200)
	expectStatus(t, do(t, h, "GET", url+query, "", outsider), 403)
	detail := expectStatus(t, do(t, h, "GET", url+query, "", viewer), 200)
	if got := detail["bill"].(map[string]any); got["order_number"] != "PO-9" || got["subject"] != "October services" || got["payment_terms_days"] != float64(30) {
		t.Fatalf("bill fields=%v", got)
	}
	if detail["bill"].(map[string]any)["total_cents"] != float64(300) || len(detail["lines"].([]any)) != 1 {
		t.Fatalf("draft=%v", detail)
	}
	for _, sql := range []string{`UPDATE bills SET total_cents=1 WHERE id=$1`, `DELETE FROM bill_lines WHERE bill_id=$1`} {
		if _, err := pool.Exec(ctx, sql, id); err == nil {
			t.Fatalf("bad draft accepted: %s", sql)
		}
	}
	expectStatus(t, do(t, h, "POST", url+"/payments", `{"org_id":`+coreID(org)+`,"amount_cents":100,"payment_date":"2026-10-02","idempotency_key":"before"}`, owner), 422)
	expectStatus(t, do(t, h, "POST", url+"/post"+query, "", viewer), 403)
	var wg sync.WaitGroup
	codes := make(chan int, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- do(t, h, "POST", url+"/post"+query, "", owner).Code }()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 200 {
			t.Fatalf("post=%d", code)
		}
	}
	expectStatus(t, do(t, h, "PUT", url, body(), owner), 409)
	if _, err := pool.Exec(ctx, `UPDATE contacts SET name='Changed supplier' WHERE id=$1`, in.ContactID); err != nil {
		t.Fatal(err)
	}
	detail = expectStatus(t, do(t, h, "GET", url+query, "", owner), 200)
	bill := detail["bill"].(map[string]any)
	if bill["supplier_name"] != "Original supplier" || bill["supplier_snapshot"].(map[string]any)["name"] != "Original supplier" {
		t.Fatalf("snapshot=%v", bill)
	}
	for _, sql := range []string{`UPDATE bills SET notes='changed' WHERE id=$1`, `DELETE FROM bill_lines WHERE bill_id=$1`, `INSERT INTO bill_lines(org_id,bill_id,line_no,account_id,description,quantity,unit_price_cents,amount_cents) SELECT org_id,bill_id,2,account_id,'extra',1,0,0 FROM bill_lines WHERE bill_id=$1`} {
		if _, err := pool.Exec(ctx, sql, id); err == nil {
			t.Fatalf("posted mutation accepted: %s", sql)
		}
	}
	payment := `{"org_id":` + coreID(org) + `,"amount_cents":100,"payment_date":"2026-10-02","reference":"Transfer-1","idempotency_key":"bill-pay-1"}`
	expectStatus(t, do(t, h, "POST", url+"/payments", payment, viewer), 403)
	expectStatus(t, do(t, h, "POST", url+"/payments", strings.Replace(payment, "2026-10-02", "2026-09-01", 1), owner), 422)
	codes = make(chan int, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- do(t, h, "POST", url+"/payments", payment, owner).Code }()
	}
	wg.Wait()
	close(codes)
	saves := 0
	for code := range codes {
		if code == 201 {
			saves++
		} else if code != 200 {
			t.Fatalf("pay=%d", code)
		}
	}
	if saves != 1 {
		t.Fatalf("payments saved %d times", saves)
	}
	expectStatus(t, do(t, h, "POST", url+"/payments", strings.Replace(payment, "Transfer-1", "Changed", 1), owner), 409)
	expectStatus(t, do(t, h, "POST", url+"/payments", strings.Replace(strings.Replace(payment, ":100", ":201", 1), "bill-pay-1", "bill-pay-2", 1), owner), 422)
	final := strings.Replace(strings.Replace(payment, ":100", ":200", 1), "bill-pay-1", "bill-pay-2", 1)
	expectStatus(t, do(t, h, "POST", url+"/payments", final, owner), 201)
	detail = expectStatus(t, do(t, h, "GET", url+query, "", owner), 200)
	if detail["bill"].(map[string]any)["balance_cents"] != float64(0) || len(detail["payments"].([]any)) != 2 {
		t.Fatalf("payments=%v", detail)
	}
	report := expectStatus(t, do(t, h, "GET", "/api/v1/reports/pnl"+query+"&from=2026-01-01&to=2026-12-31", "", owner), 200)
	if report["expense_cents"] != float64(300) || report["profit_cents"] != float64(-300) {
		t.Fatalf("pnl=%v", report)
	}
	tb := expectStatus(t, do(t, h, "GET", "/api/v1/reports/trial-balance"+query+"&as_of=2026-12-31", "", owner), 200)
	if tb["balanced"] != true {
		t.Fatalf("unbalanced=%v", tb)
	}
	var posts, pays int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM journals WHERE org_id=$1 AND idempotency_key LIKE 'bill-post:%'`, org).Scan(&posts); err != nil || posts != 1 {
		t.Fatalf("posts=%d err=%v", posts, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bill_payments WHERE org_id=$1`, org).Scan(&pays); err != nil || pays != 2 {
		t.Fatalf("payments=%d err=%v", pays, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE bill_payments SET amount_cents=1 WHERE org_id=$1`, org); err == nil {
		t.Fatal("payment changed")
	}
	in.SupplierReference = "SUP-2"
	second := expectStatus(t, do(t, h, "POST", "/api/v1/bills", body(), owner), 201)
	secondURL := "/api/v1/bills/" + coreID(int64(second["id"].(float64)))
	expectStatus(t, do(t, h, "POST", secondURL+"/post"+query, "", owner), 200)
	codes = make(chan int, 2)
	for _, key := range []string{"race-a", "race-b"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			codes <- do(t, h, "POST", secondURL+"/payments", `{"org_id":`+coreID(org)+`,"amount_cents":200,"payment_date":"2026-10-02","idempotency_key":"`+key+`"}`, owner).Code
		}(key)
	}
	wg.Wait()
	close(codes)
	saves, refused := 0, 0
	for code := range codes {
		if code == 201 {
			saves++
		} else if code == 422 {
			refused++
		} else {
			t.Fatalf("concurrent payments=%d", code)
		}
	}
	if saves != 1 || refused != 1 {
		t.Fatalf("overpayment race: saves=%d refused=%d", saves, refused)
	}
}

func TestBillRejectsForeignSupplierAndAccounts(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "bill-validation@example.com")
	other := seedSession(t, a, "bill-foreign@example.com")
	org := orgFromList(t, a, owner, `{"name":"Local","fiscal_year_end":"12-31"}`)
	foreign := orgFromList(t, a, other, `{"name":"Foreign","fiscal_year_end":"12-31"}`)
	create := func(path, body, token string) int64 {
		m := expectStatus(t, do(t, h, "POST", path, body, token), 201)
		return int64(m["id"].(float64))
	}
	customer := seedContact(t, a, org, owner)
	supplier := create("/api/v1/contacts", `{"org_id":`+coreID(org)+`,"role":"supplier","name":"Supplier"}`, owner)
	foreignSupplier := create("/api/v1/contacts", `{"org_id":`+coreID(foreign)+`,"role":"supplier","name":"Foreign supplier"}`, other)
	expense := create("/api/v1/accounts", `{"org_id":`+coreID(org)+`,"code":"6200","name":"Expense","type":"expense"}`, owner)
	asset := create("/api/v1/accounts", `{"org_id":`+coreID(org)+`,"code":"1510","name":"Asset","type":"asset"}`, owner)
	foreignExpense := create("/api/v1/accounts", `{"org_id":`+coreID(foreign)+`,"code":"6200","name":"Expense","type":"expense"}`, other)
	in := billInput{OrgID: org, ContactID: supplier, SupplierReference: "A", BillDate: "2026-10-01", DueDate: "2026-10-31", Lines: []billLineIn{{AccountID: expense, Description: "x", Quantity: 1, UnitPriceCents: 100}}}
	submit := func(want int) {
		b, _ := json.Marshal(in)
		expectStatus(t, do(t, h, "POST", "/api/v1/bills", string(b), owner), want)
	}
	in.ContactID = customer
	submit(400)
	in.ContactID = foreignSupplier
	submit(400)
	in.ContactID = supplier
	// An asset account capitalises into the register instead of being refused (R10).
	in.Lines[0].AccountID = asset
	in.SupplierReference = "ASSET-1"
	submit(201)
	in.SupplierReference = "A"
	in.Lines[0].AccountID = foreignExpense
	submit(400)
	in.Lines[0].AccountID = expense
	assetPurchaseAccount := create("/api/v1/accounts", `{"org_id":`+coreID(org)+`,"code":"1501","name":"Asset purchase account","type":"asset"}`, owner)
	// Item creation refuses a non-expense purchase account, so the asset-item line cannot exist.
	expectStatus(t, do(t, h, "POST", "/api/v1/items", `{"org_id":`+coreID(org)+`,"name":"Asset","item_type":"goods","selling_price_cents":1000,"cost_price_cents":500,"purchase_account_id":`+coreID(assetPurchaseAccount)+`}`, owner), 422)
	in.Lines[0].ItemID = 0
	in.Lines[0].AccountID = expense
	in.SupplierReference = "A"
	goods := create("/api/v1/items", `{"org_id":`+coreID(org)+`,"name":"Untracked goods","item_type":"goods","selling_price_cents":1000,"cost_price_cents":500,"purchase_account_id":`+coreID(expense)+`}`, owner)
	in.Lines[0].ItemID = goods
	in.Lines[0].AccountID = 0
	b, _ := json.Marshal(in)
	// Item lines resolve purchase accounts server-side; caller cannot smuggle another account.
	spoofed := strings.Replace(string(b), `"account_id":0`, `"account_id":`+coreID(expense), 1)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills", spoofed, owner), 400)
	goodsBill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", string(b), owner), 201)
	goodsBillPath := "/api/v1/bills/" + coreID(int64(goodsBill["id"].(float64)))
	expectStatus(t, do(t, h, "POST", goodsBillPath+"/post?org_id="+coreID(org), "", owner), 200)
	var goodsDebit int64
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(sum(l.debit_cents),0) FROM journal_lines l JOIN bills b ON b.org_id=l.org_id AND b.journal_id=l.journal_id WHERE b.org_id=$1 AND b.id=$2 AND l.account_id=$3`, org, int64(goodsBill["id"].(float64)), expense).Scan(&goodsDebit); err != nil || goodsDebit != 100 {
		t.Fatalf("goods purchase account debit=%d err=%v", goodsDebit, err)
	}
	trackedGoods := create("/api/v1/items", `{"org_id":`+coreID(org)+`,"name":"Tracked goods","item_type":"goods","selling_price_cents":1000,"cost_price_cents":500,"purchase_account_id":`+coreID(expense)+`,"track_inventory":true}`, owner)
	in.SupplierReference = "TRACKED"
	in.Lines[0].ItemID = trackedGoods
	b, _ = json.Marshal(in)
	// Tracked stock posts to the inventory control account, not the expense account (R07).
	trackedBill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", string(b), owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+coreID(int64(trackedBill["id"].(float64)))+"/post?org_id="+coreID(org), "", owner), 200)
	var inventoryDebit int64
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(sum(l.debit_cents),0) FROM journal_lines l JOIN accounts a ON a.org_id=l.org_id AND a.id=l.account_id WHERE l.org_id=$1 AND l.journal_id=(SELECT journal_id FROM bills WHERE org_id=$1 AND id=$2) AND a.code='1300'`, org, int64(trackedBill["id"].(float64))).Scan(&inventoryDebit); err != nil || inventoryDebit != 100 {
		t.Fatalf("tracked stock inventory debit=%d err=%v", inventoryDebit, err)
	}
	in.SupplierReference = "LEGACY"
	in.Lines[0].ItemID = 0
	in.Lines[0].AccountID = 0
	if _, err := a.db.Exec(t.Context(), `INSERT INTO accounts(org_id,code,name,type,class) VALUES($1,'2190','Legacy wrong type','revenue','sales')`, org); err != nil {
		t.Fatal(err)
	}
	var wrongType int64
	if err := a.db.QueryRow(t.Context(), `SELECT id FROM accounts WHERE org_id=$1 AND code='2190'`, org).Scan(&wrongType); err != nil {
		t.Fatal(err)
	}
	in.Lines[0].AccountID = wrongType
	var before, after int
	if err := a.db.QueryRow(t.Context(), `SELECT count(*) FROM journals WHERE org_id=$1`, org).Scan(&before); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(in)
	// A bill line must use an expense account: refused at save, so no journal is written.
	expectStatus(t, do(t, h, "POST", "/api/v1/bills", string(b), owner), 400)
	if err := a.db.QueryRow(t.Context(), `SELECT count(*) FROM journals WHERE org_id=$1`, org).Scan(&after); err != nil || after != before {
		t.Fatalf("refused bill changed journals %d -> %d err=%v", before, after, err)
	}
	in.SupplierReference = ""
	submit(400)
	in.SupplierReference = "A"
	in.Notes = "bad\x00text"
	submit(400)
	in.Notes = ""
	if _, err := a.db.Exec(t.Context(), `UPDATE organizations SET gst_registered=true WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	// GST-registered orgs may now post non-GST bill lines.
	in.SupplierReference = "GST-NONE"
	in.Lines[0].AccountID = expense
	submit(201)
}

func TestBillGstCodes(t *testing.T) {
	for _, code := range []string{"ZR", "ES", "OS", "NONE"} {
		in := billInput{ContactID: 1, SupplierReference: "T", BillDate: "2026-01-01", DueDate: "2026-01-01", Lines: []billLineIn{{AccountID: 1, Description: "purchase", Quantity: 1, UnitPriceCents: 10000, GSTCode: code}}}
		lines, net, gst, errCode := validBill(&in, true, 900)
		if errCode != "" || len(lines) != 1 || net != 10000 || gst != 0 || lines[0].GSTCode != code {
			t.Fatalf("%s: lines=%+v net=%d gst=%d err=%s", code, lines, net, gst, errCode)
		}
	}
	for _, tc := range []struct {
		date           string
		rate, expected int64
	}{{"2004-01-01", 500, 500}, {"2007-06-30", 500, 500}, {"2007-07-01", 700, 700}, {"2022-12-31", 700, 700}, {"2023-01-01", 800, 800}, {"2024-01-01", 900, 900}} {
		in := billInput{ContactID: 1, SupplierReference: "T", BillDate: tc.date, DueDate: tc.date, Lines: []billLineIn{{AccountID: 1, Description: "taxable", Quantity: 1, UnitPriceCents: 10000, GSTCode: "SR"}}}
		lines, _, gst, errCode := validBill(&in, true, tc.rate)
		if errCode != "" || gst != tc.expected || lines[0].GSTRateBP != tc.rate {
			t.Fatalf("%s: %+v gst=%d err=%s", tc.date, lines, gst, errCode)
		}
	}
	in := billInput{ContactID: 1, SupplierReference: "T", BillDate: "2026-01-01", DueDate: "2026-01-01", Lines: []billLineIn{{AccountID: 1, Description: "purchase", Quantity: 1, UnitPriceCents: 10000, GSTCode: "ES"}}}
	if _, _, _, code := validBill(&in, false, 900); code != "unsupported_gst" {
		t.Fatalf("unregistered company code=%q", code)
	}
}

func TestBillGstPosting(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	token := seedSession(t, a, "bill-gst@example.com")
	org := orgFromList(t, a, token, `{"name":"GST","fiscal_year_end":"12-31"}`)
	if _, err := a.db.Exec(t.Context(), `UPDATE organizations SET gst_registered=true,gst_registration_history_missing=false WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	create := func(path, body string) int64 {
		m := expectStatus(t, do(t, h, "POST", path, body, token), 201)
		return int64(m["id"].(float64))
	}
	supplier := create("/api/v1/contacts", `{"org_id":`+coreID(org)+`,"role":"supplier","name":"Supplier"}`)
	expense := create("/api/v1/accounts", `{"org_id":`+coreID(org)+`,"code":"6201","name":"Expense","type":"expense"}`)
	in := billInput{OrgID: org, ContactID: supplier, SupplierReference: "GST-1", BillDate: "2026-03-01", DueDate: "2026-03-31",
		Lines: []billLineIn{{AccountID: expense, Description: "x", Quantity: 2, UnitPriceCents: 5000, GSTCode: "SR"}}}
	b, _ := json.Marshal(in)
	billID := int64(expectStatus(t, do(t, h, "POST", "/api/v1/bills", string(b), token), 201)["id"].(float64))
	var total, gst int64
	if err := a.db.QueryRow(t.Context(), `SELECT total_cents,gst_cents FROM bills WHERE org_id=$1 AND id=$2`, org, billID).Scan(&total, &gst); err != nil {
		t.Fatal(err)
	}
	if total != 10900 || gst != 900 {
		t.Fatalf("bill total=%d gst=%d, want 10900 and 900", total, gst)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+coreID(billID)+"/post?org_id="+coreID(org), "", token), 200)
	var debitExpense, debitGST, creditAP int64
	if err := a.db.QueryRow(t.Context(), `SELECT
		COALESCE(sum(l.debit_cents) FILTER (WHERE a.code='6201'),0),
		COALESCE(sum(l.debit_cents) FILTER (WHERE a.code='2300' AND l.tax_direction='input'),0),
		COALESCE(sum(l.credit_cents) FILTER (WHERE a.code='2100'),0)
		FROM bills b JOIN journals j ON j.org_id=b.org_id AND j.id=b.journal_id
		JOIN journal_lines l ON l.org_id=j.org_id AND l.journal_id=j.id
		JOIN accounts a ON a.org_id=l.org_id AND a.id=l.account_id
		WHERE b.org_id=$1 AND b.id=$2`, org, billID).Scan(&debitExpense, &debitGST, &creditAP); err != nil {
		t.Fatal(err)
	}
	if debitExpense != 10000 || debitGST != 900 || creditAP != 10900 {
		t.Fatalf("journal expense=%d gst=%d ap=%d, want 10000/900/10900", debitExpense, debitGST, creditAP)
	}
	// Payment route records completed amount only; verify resulting balance and ledger record.
	payPath := "/api/v1/bills/" + coreID(billID) + "/payments"
	paid := expectStatus(t, do(t, h, "POST", payPath, `{"org_id":`+coreID(org)+`,"amount_cents":4000,"payment_date":"2026-03-20","reference":"Bank transfer already sent","idempotency_key":"bill-pay-test"}`, token), 201)
	if paid["balance_cents"] != float64(6900) {
		t.Fatal("payment balance", paid)
	}
	var balance, paymentCount, journalCount int64
	if err := a.db.QueryRow(t.Context(), `SELECT total_cents-COALESCE((SELECT sum(amount_cents) FROM bill_payments WHERE org_id=$1 AND bill_id=$2),0),(SELECT count(*) FROM bill_payments WHERE org_id=$1 AND bill_id=$2),(SELECT count(*) FROM journals WHERE org_id=$1 AND idempotency_key='bill-pay:bill-pay-test') FROM bills WHERE org_id=$1 AND id=$2`, org, billID).Scan(&balance, &paymentCount, &journalCount); err != nil {
		t.Fatal(err)
	}
	if balance != 6900 || paymentCount != 1 || journalCount != 1 {
		t.Fatalf("payment state balance=%d payments=%d journals=%d", balance, paymentCount, journalCount)
	}
	// A 2023 bill uses the historical 8% rate.
	historyToken := seedSession(t, a, "bill-gst-history@example.com")
	legacyOrg := orgFromList(t, a, historyToken, `{"name":"Historical GST","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	legacySupplier := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+coreID(legacyOrg)+`,"role":"supplier","name":"Historical supplier"}`, historyToken), 201)["id"].(float64))
	legacyExpense := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+coreID(legacyOrg)+`,"code":"6202","name":"Historical expense","type":"expense"}`, historyToken), 201)["id"].(float64))
	oldBill := `{"org_id":` + coreID(legacyOrg) + `,"contact_id":` + coreID(legacySupplier) + `,"supplier_reference":"H-2023","bill_date":"2023-03-01","due_date":"2023-03-31","lines":[{"account_id":` + coreID(legacyExpense) + `,"description":"old","quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}`
	old := expectStatus(t, do(t, h, "POST", "/api/v1/bills", oldBill, historyToken), 201)
	if old["total_cents"] != float64(10800) {
		t.Fatal("2023 GST rate", old)
	}
	// Registration date gates SR: bill after deregistration cannot claim input GST.
	deregToken := seedSession(t, a, "bill-gst-dereg@example.com")
	deregistered := orgFromList(t, a, deregToken, `{"name":"Deregistered","fiscal_year_end":"12-31","gst_registered":true,"gst_effective_from":"2022-01-01"}`)
	if _, err := a.db.Exec(t.Context(), `UPDATE organizations SET gst_registered=false WHERE id=$1`, deregistered); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(t.Context(), `UPDATE gst_registrations SET effective_to='2025-12-31' WHERE org_id=$1`, deregistered); err != nil {
		t.Fatal(err)
	}
	deregisterSupplier := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+coreID(deregistered)+`,"role":"supplier","name":"Deregistered supplier"}`, deregToken), 201)["id"].(float64))
	deregisterExpense := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+coreID(deregistered)+`,"code":"6203","name":"Expense","type":"expense"}`, deregToken), 201)["id"].(float64))
	outside := `{"org_id":` + coreID(deregistered) + `,"contact_id":` + coreID(deregisterSupplier) + `,"supplier_reference":"H-after","bill_date":"2026-03-01","due_date":"2026-03-31","lines":[{"account_id":` + coreID(deregisterExpense) + `,"description":"outside","quantity":1,"unit_price_cents":10000,"gst_code":"SR"}]}`
	expectStatus(t, do(t, h, "POST", "/api/v1/bills", outside, deregToken), 422)
	// A company without GST registration cannot claim input tax.
	other := seedSession(t, a, "bill-gst-plain@example.com")
	plain := orgFromList(t, a, other, `{"name":"Plain","fiscal_year_end":"12-31"}`)
	supplier2 := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+coreID(plain)+`,"role":"supplier","name":"Supplier 2"}`, other), 201)["id"].(float64))
	expense2 := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+coreID(plain)+`,"code":"6201","name":"Expense","type":"expense"}`, other), 201)["id"].(float64))
	in = billInput{OrgID: plain, ContactID: supplier2, SupplierReference: "GST-2", BillDate: "2026-03-01", DueDate: "2026-03-31",
		Lines: []billLineIn{{AccountID: expense2, Description: "x", Quantity: 1, UnitPriceCents: 1000, GSTCode: "SR"}}}
	b, _ = json.Marshal(in)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills", string(b), other), 422)
}
