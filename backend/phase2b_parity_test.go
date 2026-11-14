package main

import (
	"net/http"
	"testing"
)

// Phase 2b server rules: PO-to-bill copy posts and marks the order billed, one batch payment covers
// several bills, an overpayment needs an explicit confirmation, repeating bills honour their mode,
// and a supplier credit has its own number and list.

type pbFixture struct {
	org      int64
	supplier int64
	account  int64
}

func pbSetup(t *testing.T, a *app, h http.Handler, owner, name string) pbFixture {
	t.Helper()
	org := orgFromList(t, a, owner, `{"name":"`+name+`","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	supplier := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Supply Co","payment_terms_days":30}`, owner), 201)["id"].(float64))
	account := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6100","name":"Supplies","type":"expense"}`, owner), 201)["id"].(float64))
	return pbFixture{org: org, supplier: supplier, account: account}
}

func pbBill(t *testing.T, h http.Handler, token string, f pbFixture, reference, date string, cents int64) int64 {
	t.Helper()
	body := `{"org_id":` + itoa(f.org) + `,"contact_id":` + itoa(f.supplier) + `,"supplier_reference":"` + reference + `","bill_date":"` + date + `","due_date":"` + date + `","lines":[{"description":"Supply","account_id":` + itoa(f.account) + `,"quantity":1,"unit_price_cents":` + itoa(cents) + `}]}`
	return int64(expectStatus(t, do(t, h, "POST", "/api/v1/bills", body, token), 201)["id"].(float64))
}

func pbPostedBill(t *testing.T, h http.Handler, token string, f pbFixture, reference string, cents int64) int64 {
	t.Helper()
	id := pbBill(t, h, token, f, reference, "2026-03-01", cents)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(id)+"/post?org_id="+itoa(f.org), "", token), 200)
	return id
}

func TestPurchaseOrderBillCopyPrefillsAndPosts(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "po-bill@example.com")
	f := pbSetup(t, a, h, owner, "PO copy co")
	o := itoa(f.org)
	lines := `[{"description":"Paper","account_id":` + itoa(f.account) + `,"quantity":10,"unit_price_cents":500,"gst_code":"NONE"},{"description":"Ink","account_id":` + itoa(f.account) + `,"quantity":2,"unit_price_cents":2500,"gst_code":"NONE"}]`
	po := int64(expectStatus(t, do(t, h, "POST", "/api/v1/purchase-orders", `{"org_id":`+o+`,"contact_id":`+itoa(f.supplier)+`,"po_date":"2026-03-01","delivery_date":"2026-03-08","reference":"REF-9","lines":`+lines+`}`, owner), 201)["id"].(float64))
	// A draft order cannot be billed; it must be submitted and approved first.
	expectStatus(t, do(t, h, "POST", "/api/v1/purchase-orders/"+itoa(po)+"/bill", `{"org_id":`+o+`}`, owner), 409)
	expectStatus(t, do(t, h, "POST", "/api/v1/purchase-orders/"+itoa(po)+"/stage", `{"org_id":`+o+`,"stage":"awaiting_approval"}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/purchase-orders/"+itoa(po)+"/stage", `{"org_id":`+o+`,"stage":"issued"}`, owner), 200)

	listed := expectStatus(t, do(t, h, "GET", "/api/v1/purchase-orders?org_id="+o+"&status=approved", "", owner), 200)["purchase_orders"].([]any)
	if len(listed) != 1 || listed[0].(map[string]any)["lifecycle_status"] != "approved" || listed[0].(map[string]any)["sent_at"] == "" {
		t.Fatalf("approved order missing: %v", listed)
	}
	copied := expectStatus(t, do(t, h, "POST", "/api/v1/purchase-orders/"+itoa(po)+"/bill", `{"org_id":`+o+`}`, owner), 201)
	billID := int64(copied["bill_id"].(float64))
	detail := expectStatus(t, do(t, h, "GET", "/api/v1/bills/"+itoa(billID)+"?org_id="+o, "", owner), 200)
	bill := detail["bill"].(map[string]any)
	if bill["supplier_reference"] != "PO-00001" || bill["bill_date"] != "2026-03-01" || bill["due_date"] != "2026-03-31" || bill["total_cents"] != float64(10000) {
		t.Fatalf("prefill: %v", bill)
	}
	linesOut := detail["lines"].([]any)
	if len(linesOut) != 2 || linesOut[0].(map[string]any)["account_id"] != float64(f.account) || linesOut[0].(map[string]any)["quantity"] != float64(10) {
		t.Fatalf("line prefill: %v", linesOut)
	}
	// The copy is approvable and posts without edits, which is the regression Xero has.
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+itoa(billID)+"/post?org_id="+o, "", owner), 200)
	var poStatus string
	var billed int64
	if err := pool.QueryRow(t.Context(), `SELECT status,billed_cents FROM purchase_orders WHERE id=$1`, po).Scan(&poStatus, &billed); err != nil {
		t.Fatal(err)
	}
	if poStatus != "billed" || billed != 10000 {
		t.Fatalf("po after billing: %s %d", poStatus, billed)
	}
	again := expectStatus(t, do(t, h, "GET", "/api/v1/purchase-orders?org_id="+o, "", owner), 200)["purchase_orders"].([]any)[0].(map[string]any)
	if again["billed_percent"] != float64(100) || again["lifecycle_status"] != "billed" {
		t.Fatalf("billed percentage: %v", again)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/purchase-orders/"+itoa(po)+"/bill", `{"org_id":`+o+`}`, owner), 409)
}

func TestBatchPaymentCoversSeveralBills(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "batch@example.com")
	f := pbSetup(t, a, h, owner, "Batch co")
	o := itoa(f.org)
	one := pbPostedBill(t, h, owner, f, "B-1", 10000)
	two := pbPostedBill(t, h, owner, f, "B-2", 20000)
	draft := pbBill(t, h, owner, f, "B-3", "2026-03-01", 5000)
	body := func(key string, allocations string) string {
		return `{"org_id":` + o + `,"payment_date":"2026-03-10","reference":"BANK-1","idempotency_key":"` + key + `","allocations":` + allocations + `}`
	}
	both := `[{"bill_id":` + itoa(one) + `,"amount_cents":10000},{"bill_id":` + itoa(two) + `,"amount_cents":20000}]`
	made := expectStatus(t, do(t, h, "POST", "/api/v1/bill-payments/batch", body("batch-1", both), owner), 201)
	if made["total_cents"] != float64(30000) {
		t.Fatalf("batch total: %v", made)
	}
	// A repeat of the same key returns the same payment instead of paying twice.
	again := expectStatus(t, do(t, h, "POST", "/api/v1/bill-payments/batch", body("batch-1", both), owner), 200)
	if len(again["payment_ids"].([]any)) != 2 || again["journal_id"] != made["journal_id"] {
		t.Fatalf("batch repeated: %v", again)
	}
	for _, id := range []int64{one, two} {
		detail := expectStatus(t, do(t, h, "GET", "/api/v1/bills/"+itoa(id)+"?org_id="+o, "", owner), 200)
		bill := detail["bill"].(map[string]any)
		if bill["balance_cents"] != float64(0) || bill["lifecycle_status"] != "paid" || bill["paid"] != true {
			t.Fatalf("bill after batch: %v", bill)
		}
	}
	// A draft bill cannot be in a batch, and a batch never overpays a bill.
	expectStatus(t, do(t, h, "POST", "/api/v1/bill-payments/batch", body("batch-2", `[{"bill_id":`+itoa(draft)+`,"amount_cents":5000}]`), owner), 422)
	expectStatus(t, do(t, h, "POST", "/api/v1/bill-payments/batch", body("batch-3", `[{"bill_id":`+itoa(one)+`,"amount_cents":100}]`), owner), 422)
}

func TestBillOverpaymentNeedsExplicitConfirmation(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "overpay@example.com")
	f := pbSetup(t, a, h, owner, "Overpay co")
	o := itoa(f.org)
	bill := pbPostedBill(t, h, owner, f, "OV-1", 10000)
	path := "/api/v1/bills/" + itoa(bill) + "/payments"
	base := `{"org_id":` + o + `,"payment_date":"2026-03-10"`
	// Paying more than is owed is blocked first.
	expectStatus(t, do(t, h, "POST", path, base+`,"amount_cents":15000,"idempotency_key":"over-1"}`, owner), 422)
	// The confirmation must say what the extra is for.
	expectStatus(t, do(t, h, "POST", path, base+`,"amount_cents":15000,"idempotency_key":"over-1","overpayment":true}`, owner), 400)
	expectStatus(t, do(t, h, "POST", path, base+`,"amount_cents":15000,"idempotency_key":"over-1","overpayment":true,"overpayment_description":"Paid with next month","tracking":{"999999":1}}`, owner), 422)
	made := expectStatus(t, do(t, h, "POST", path, base+`,"amount_cents":15000,"idempotency_key":"over-1","overpayment":true,"overpayment_description":"Paid with next month"}`, owner), 201)
	if made["balance_cents"] != float64(-5000) {
		t.Fatalf("overpaid balance: %v", made)
	}
	var flag bool
	var description string
	if err := pool.QueryRow(t.Context(), `SELECT overpayment,overpayment_description FROM bill_payments WHERE org_id=$1 AND bill_id=$2`, f.org, bill).Scan(&flag, &description); err != nil {
		t.Fatal(err)
	}
	if !flag || description != "Paid with next month" {
		t.Fatalf("overpayment not recorded: %v %q", flag, description)
	}
	detail := expectStatus(t, do(t, h, "GET", "/api/v1/bills/"+itoa(bill)+"?org_id="+o, "", owner), 200)
	if detail["bill"].(map[string]any)["balance_cents"] != float64(-5000) {
		t.Fatalf("overpaid bill: %v", detail["bill"])
	}
}

func TestRecurringBillModesAndAutoGeneration(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "recurring-mode@example.com")
	f := pbSetup(t, a, h, owner, "Repeat co")
	o := itoa(f.org)
	bill := `{"contact_id":` + itoa(f.supplier) + `,"supplier_reference":"RENT","payment_terms_days":0,"lines":[{"description":"Rent","account_id":` + itoa(f.account) + `,"quantity":1,"unit_price_cents":10000}]}`
	// Three monthly runs up to the end date; approve mode posts each one.
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring-bills", `{"org_id":`+o+`,"name":"Rent","repeat_every":1,"repeat_unit":"month","start_date":"2026-01-01","ends_on":"2026-03-01","mode":"approve","auto_generate":true,"bill":`+bill+`}`, owner), 201)
	profiles := expectStatus(t, do(t, h, "GET", "/api/v1/recurring-bills?org_id="+o, "", owner), 200)["profiles"].([]any)
	if len(profiles) != 1 || profiles[0].(map[string]any)["mode"] != "approve" || profiles[0].(map[string]any)["generated_count"] != float64(3) {
		t.Fatalf("recurring approve mode: %v", profiles)
	}
	var posted, drafts int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE status='posted'), count(*) FILTER (WHERE status='draft') FROM bills WHERE org_id=$1`, f.org).Scan(&posted, &drafts); err != nil {
		t.Fatal(err)
	}
	if posted != 3 || drafts != 0 {
		t.Fatalf("approve mode posted=%d drafts=%d", posted, drafts)
	}
	// Opening the list again generates nothing new.
	expectStatus(t, do(t, h, "GET", "/api/v1/recurring-bills?org_id="+o, "", owner), 200)
	var total int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM bills WHERE org_id=$1`, f.org).Scan(&total); err != nil || total != 3 {
		t.Fatalf("repeated run created duplicates: %d %v", total, err)
	}
	// Draft mode leaves the bill for review; the explicit run endpoint still works.
	draftBill := `{"contact_id":` + itoa(f.supplier) + `,"supplier_reference":"DRAFT-RENT","payment_terms_days":0,"lines":[{"description":"Rent","account_id":` + itoa(f.account) + `,"quantity":1,"unit_price_cents":10000}]}`
	draftProfile := expectStatus(t, do(t, h, "POST", "/api/v1/recurring-bills", `{"org_id":`+o+`,"name":"Draft rent","repeat_every":1,"repeat_unit":"month","start_date":"2026-02-01","mode":"draft","bill":`+draftBill+`}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/recurring-bills/run", `{"org_id":`+o+`,"as_of":"2026-02-01"}`, owner), 200)
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM bills WHERE org_id=$1 AND recurring_profile_id=$2 AND status='draft'`, f.org, int64(draftProfile["id"].(float64))).Scan(&drafts); err != nil || drafts != 1 {
		t.Fatalf("draft mode: %d %v", drafts, err)
	}
}

func TestStandaloneSupplierCreditNumberingAndList(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "sc@example.com")
	f := pbSetup(t, a, h, owner, "Credits co")
	o := itoa(f.org)
	body := func(ref string) string {
		return `{"org_id":` + o + `,"contact_id":` + itoa(f.supplier) + `,"account_id":` + itoa(f.account) + `,"credit_date":"2026-03-05","reference":"` + ref + `","net_cents":10000,"gst_cents":900}`
	}
	first := expectStatus(t, do(t, h, "POST", "/api/v1/supplier-credits", body("DAMAGED"), owner), 201)
	if first["credit_number"] != "SC-00001" || first["status"] != "draft" {
		t.Fatalf("standalone credit: %v", first)
	}
	second := expectStatus(t, do(t, h, "POST", "/api/v1/supplier-credits", body("RETURN"), owner), 201)
	if second["credit_number"] != "SC-00002" {
		t.Fatalf("own numbering: %v", second)
	}
	approved := expectStatus(t, do(t, h, "POST", "/api/v1/supplier-credits/"+itoa(int64(first["id"].(float64)))+"/approve", `{"org_id":`+o+`}`, owner), 200)
	var ap, expense, gst int64
	if err := pool.QueryRow(t.Context(), `SELECT
		COALESCE(sum(jl.debit_cents) FILTER (WHERE a.code='2100'),0),
		COALESCE(sum(jl.credit_cents) FILTER (WHERE a.code='6100'),0),
		COALESCE(sum(jl.credit_cents) FILTER (WHERE a.code='2300'),0)
		FROM journal_lines jl JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
		WHERE jl.org_id=$1 AND jl.journal_id=$2`, f.org, int64(approved["journal_id"].(float64))).Scan(&ap, &expense, &gst); err != nil {
		t.Fatal(err)
	}
	if ap != 10900 || expense != 10000 || gst != 900 {
		t.Fatalf("standalone credit journal: ap=%d expense=%d gst=%d", ap, expense, gst)
	}
	// A credit pointed at a posted bill reduces what that bill owes.
	bill := pbPostedBill(t, h, owner, f, "CREDITED-1", 20000)
	linked := expectStatus(t, do(t, h, "POST", "/api/v1/supplier-credits", `{"org_id":`+o+`,"contact_id":`+itoa(f.supplier)+`,"bill_id":`+itoa(bill)+`,"credit_date":"2026-03-06","reference":"BILL CREDIT","net_cents":5000,"gst_cents":0}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/supplier-credits/"+itoa(int64(linked["id"].(float64)))+"/approve", `{"org_id":`+o+`}`, owner), 200)
	detail := expectStatus(t, do(t, h, "GET", "/api/v1/bills/"+itoa(bill)+"?org_id="+o, "", owner), 200)
	if detail["bill"].(map[string]any)["balance_cents"] != float64(15000) {
		t.Fatalf("linked credit balance: %v", detail["bill"])
	}
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/supplier-credits?org_id="+o, "", owner), 200)["supplier_credits"].([]any)
	if len(listed) != 3 {
		t.Fatalf("credit list: %v", listed)
	}
	if listed[0].(map[string]any)["credit_number"] == "" || listed[0].(map[string]any)["status"] != "open" {
		t.Fatalf("credit list shape: %v", listed[0])
	}
	// Accounts payable: the posted bill credits 20,000 and the two credits debit 5,000 and 10,900.
	var balance int64
	if err := pool.QueryRow(t.Context(), `SELECT COALESCE(sum(jl.credit_cents)-sum(jl.debit_cents),0) FROM journal_lines jl JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id WHERE jl.org_id=$1 AND a.code='2100'`, f.org).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if balance != 4100 {
		t.Fatalf("ap after credits: %d", balance)
	}
}

func TestSupplierCreditCurrencyUsesDatedRate(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "supplier-credit-currency@example.com")
	org := orgFromList(t, a, owner, `{"name":"Supplier currency","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	supplier := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Foreign vendor"}`, owner), 201)["id"].(float64))
	expense := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6100","name":"Supplies","type":"expense"}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/currencies", `{"org_id":`+o+`,"code":"USD","name":"US Dollar"}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/currencies/rates", `{"org_id":`+o+`,"code":"USD","rate_date":"2026-03-01","rate_micro":1350000}`, owner), 200)
	base := `"org_id":` + o + `,"contact_id":` + itoa(supplier) + `,"account_id":` + itoa(expense) + `,"credit_date":"2026-03-10","reference":"USD return","net_cents":10000,"gst_cents":900`
	expectStatus(t, do(t, h, "POST", "/api/v1/supplier-credits", `{`+base+`,"currency":"EUR"}`, owner), 400)
	made := expectStatus(t, do(t, h, "POST", "/api/v1/supplier-credits", `{`+base+`,"currency":"USD"}`, owner), 201)
	creditID := int64(made["id"].(float64))
	list := expectStatus(t, do(t, h, "GET", "/api/v1/supplier-credits?org_id="+o, "", owner), 200)["supplier_credits"].([]any)
	credit := list[0].(map[string]any)
	if credit["currency"] != "USD" || credit["fx_rate_micro"] != float64(1350000) {
		t.Fatalf("currency/rate: %v", credit)
	}
	posted := expectStatus(t, do(t, h, "POST", "/api/v1/supplier-credits/"+itoa(creditID)+"/approve", `{"org_id":`+o+`}`, owner), 200)
	var debits, credits int64
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(sum(debit_cents),0),COALESCE(sum(credit_cents),0) FROM journal_lines WHERE org_id=$1 AND journal_id=$2`, org, int64(posted["journal_id"].(float64))).Scan(&debits, &credits); err != nil {
		t.Fatal(err)
	}
	if debits != 14715 || credits != 14715 {
		t.Fatalf("journal should post converted SGD cents and balance: %d/%d", debits, credits)
	}
	var ap, expenseCredit, lineCurrency string
	if err := a.db.QueryRow(t.Context(), `SELECT debit_cents::text, currency FROM journal_lines WHERE org_id=$1 AND journal_id=$2 AND line_no=1`, org, int64(posted["journal_id"].(float64))).Scan(&ap, &lineCurrency); err != nil {
		t.Fatal(err)
	}
	if ap != "14715" || lineCurrency != "USD" {
		t.Fatalf("AP FX leg: amount=%s currency=%s", ap, lineCurrency)
	}
	var apAccountID int64
	if err := a.db.QueryRow(t.Context(), `SELECT account_id FROM journal_lines WHERE org_id=$1 AND journal_id=$2 AND line_no=1`, org, int64(posted["journal_id"].(float64))).Scan(&apAccountID); err != nil {
		t.Fatal(err)
	}
	var apCode string
	if err := a.db.QueryRow(t.Context(), `SELECT code FROM accounts WHERE org_id=$1 AND id=$2`, org, apAccountID).Scan(&apCode); err != nil {
		t.Fatal(err)
	}
	if apCode != acctAP {
		t.Fatalf("expected AP control account %s, got %s", acctAP, apCode)
	}
	if err := a.db.QueryRow(t.Context(), `SELECT credit_cents::text FROM journal_lines WHERE org_id=$1 AND journal_id=$2 AND line_no=2`, org, int64(posted["journal_id"].(float64))).Scan(&expenseCredit); err != nil {
		t.Fatal(err)
	}
	if expenseCredit != "13500" {
		t.Fatalf("expense FX credit: %s", expenseCredit)
	}
	var gstCredit string
	if err := a.db.QueryRow(t.Context(), `SELECT credit_cents::text FROM journal_lines WHERE org_id=$1 AND journal_id=$2 AND line_no=3`, org, int64(posted["journal_id"].(float64))).Scan(&gstCredit); err != nil {
		t.Fatal(err)
	}
	if gstCredit != "1215" {
		t.Fatalf("GST FX credit: %s", gstCredit)
	}
}

func TestCreditNoteCurrency(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "cn-currency@example.com")
	org := orgFromList(t, a, owner, `{"name":"Currency credits","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	customer := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201)["id"].(float64))
	revenue := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4100","name":"Sales","type":"revenue"}`, owner), 201)["id"].(float64))
	bad := `{"org_id":` + o + `,"contact_id":` + itoa(customer) + `,"credit_date":"2026-03-10","currency":"USD","lines":[{"description":"Refund","account_id":` + itoa(revenue) + `,"quantity":1,"unit_price_cents":10000}]}`
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", bad, owner), 400)
	made := expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", `{"org_id":`+o+`,"contact_id":`+itoa(customer)+`,"credit_date":"2026-03-10","currency":"USD","fx_rate_micro":1350000,"lines":[{"description":"Refund","account_id":`+itoa(revenue)+`,"quantity":1,"unit_price_cents":10000}]}`, owner), 201)
	if made["currency"] != "USD" || made["fx_rate_micro"] != float64(1350000) {
		t.Fatalf("credit note currency: %v", made)
	}
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/credit-notes?org_id="+o, "", owner), 200)["credit_notes"].([]any)
	if listed[0].(map[string]any)["currency"] != "USD" {
		t.Fatalf("listed currency: %v", listed[0])
	}
	// A credit note stores the currency, not a converted amount: the books stay in SGD cents.
	if listed[0].(map[string]any)["total_cents"] != float64(10000) {
		t.Fatalf("credit amount: %v", listed[0])
	}
}
