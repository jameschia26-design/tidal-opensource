package main

import "testing"

// TestDocumentLifecycle covers phase 1d: editable numbers fixed at draft save, paid derived from
// allocations, voiding with a dated mirrored journal, and list filters with counts.
func TestDocumentLifecycle(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "lifecycle@example.com")
	org := orgFromList(t, a, owner, `{"name":"Lifecycle","fiscal_year_end":"12-31"}`)
	o := coreID(org)
	customer := seedContact(t, a, org, owner)
	supplier := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"supplier","name":"Vendor"}`, owner), 201)["id"].(float64))
	expense := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6300","name":"Parts","type":"expense"}`, owner), 201)["id"].(float64))

	invoice := func(number string) map[string]any {
		t.Helper()
		body := `{"org_id":` + o + `,"contact_id":` + coreID(customer) + `,"invoice_date":"2026-10-01","due_date":"2026-10-31","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000}]`
		if number != "" {
			body += `,"document_number":"` + number + `"`
		}
		return expectStatus(t, do(t, h, "POST", "/api/v1/invoices", body+`}`, owner), 201)
	}
	readInvoice := func(id int64) map[string]any {
		t.Helper()
		return expectStatus(t, do(t, h, "GET", "/api/v1/invoices/"+coreID(id)+"?org_id="+o, "", owner), 200)["invoice"].(map[string]any)
	}

	// A typed number is fixed when the draft is saved and can be edited only before posting.
	first := invoice("CUSTOM-1")
	firstID := int64(first["id"].(float64))
	if got := readInvoice(firstID)["document_number"]; got != "CUSTOM-1" {
		t.Fatalf("draft number=%v", got)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+coreID(customer)+`,"invoice_date":"2026-10-01","due_date":"2026-10-31","document_number":"CUSTOM-1","lines":[{"description":"Work","quantity":1,"unit_price_cents":100}]}`, owner), 409)
	edit := `{"org_id":` + o + `,"contact_id":` + coreID(customer) + `,"invoice_date":"2026-10-01","due_date":"2026-10-31","document_number":"CUSTOM-2","lines":[{"description":"Work","quantity":1,"unit_price_cents":10000}]}`
	expectStatus(t, do(t, h, "PUT", "/api/v1/invoices/"+coreID(firstID), edit, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+coreID(firstID)+"/post?org_id="+o, "", owner), 200)
	posted := readInvoice(firstID)
	if posted["document_number"] != "CUSTOM-2" || posted["status"] != "posted" || posted["lifecycle_status"] != "awaiting_payment" {
		t.Fatalf("posted=%v", posted)
	}
	expectStatus(t, do(t, h, "PUT", "/api/v1/invoices/"+coreID(firstID), edit, owner), 409)

	// Numbering settings are used when a new document starts.
	settings := `{"invoice_prefix":"S-","invoice_next":7,"bill_prefix":"B-","bill_next":3,"credit_note_prefix":"C-","credit_note_next":2,"purchase_order_prefix":"P-","purchase_order_next":4,"quote_prefix":"Q-","quote_next":5}`
	expectStatus(t, do(t, h, "PUT", "/api/v1/document-numbering?org_id="+o, settings, owner), 200)
	auto := invoice("")
	autoID := int64(auto["id"].(float64))
	if got := readInvoice(autoID)["document_number"]; got != "S-00007" {
		t.Fatalf("auto number=%v", got)
	}

	// Void an unallocated invoice: totals stay, a balancing journal is written, status is voided.
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+coreID(autoID)+"/post?org_id="+o, "", owner), 200)
	totalBefore := readInvoice(autoID)["total_cents"]
	voided := expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+coreID(autoID)+"/void", `{"org_id":`+o+`,"reversal_date":"2026-10-20"}`, owner), 200)
	if voided["lifecycle_status"] != "voided" || voided["reversal_date"] != "2026-10-20" {
		t.Fatalf("void=%v", voided)
	}
	after := readInvoice(autoID)
	if after["lifecycle_status"] != "voided" || after["balance_cents"] != float64(0) || after["total_cents"] != totalBefore || after["document_number"] != "S-00007" {
		t.Fatalf("after void=%v", after)
	}
	var reversal, balanced bool
	var debits, credits int64
	if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM document_reversals WHERE org_id=$1 AND doc_kind='invoice' AND doc_id=$2)`, org, autoID).Scan(&reversal); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT COALESCE(sum(l.debit_cents),0), COALESCE(sum(l.credit_cents),0) FROM journal_lines l JOIN journals j ON j.org_id=l.org_id AND j.id=l.journal_id WHERE l.org_id=$1 AND (j.idempotency_key='invoice-post:'||$2::bigint::text OR j.idempotency_key='void-invoice:'||$2::bigint::text)`, org, autoID).Scan(&debits, &credits); err != nil {
		t.Fatal(err)
	}
	balanced = debits == credits && debits > 0
	if !reversal || !balanced {
		t.Fatalf("reversal=%v debits=%d credits=%d", reversal, debits, credits)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+coreID(autoID)+"/void", `{"org_id":`+o+`,"reversal_date":"2026-10-21"}`, owner), 409)

	// A paid invoice cannot be voided.
	paidID := int64(invoice("PAID-1")["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+coreID(paidID)+"/post?org_id="+o, "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+coreID(paidID)+"/payments", `{"org_id":`+o+`,"amount_cents":10000,"payment_date":"2026-10-05","idempotency_key":"life-paid"}`, owner), 201)
	if got := readInvoice(paidID)["lifecycle_status"]; got != "paid" {
		t.Fatalf("fully paid lifecycle=%v", got)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+coreID(paidID)+"/void", `{"org_id":`+o+`,"reversal_date":"2026-10-20"}`, owner), 409)

	// Partial payment stays awaiting payment.
	partID := int64(invoice("PART-1")["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+coreID(partID)+"/post?org_id="+o, "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+coreID(partID)+"/payments", `{"org_id":`+o+`,"amount_cents":4000,"payment_date":"2026-10-05","idempotency_key":"life-part"}`, owner), 201)
	if got := readInvoice(partID)["lifecycle_status"]; got != "awaiting_payment" {
		t.Fatalf("part-paid lifecycle=%v", got)
	}
	if got := lifecycleStatus("posted", -100); got != "paid" {
		t.Fatalf("overpaid lifecycle=%v", got)
	}

	// Bills get the same filters and a void status.
	bill := expectStatus(t, do(t, h, "POST", "/api/v1/bills", `{"org_id":`+o+`,"contact_id":`+coreID(supplier)+`,"supplier_reference":"LIFE-1","bill_date":"2026-10-01","due_date":"2026-10-31","lines":[{"account_id":`+coreID(expense)+`,"description":"Parts","quantity":1,"unit_price_cents":5000}]}`, owner), 201)
	if bill["id"] == nil {
		t.Fatal("bill not saved")
	}
	billID := int64(bill["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+coreID(billID)+"/post?org_id="+o, "", owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/bills/"+coreID(billID)+"/void", `{"org_id":`+o+`,"reversal_date":"2026-10-20"}`, owner), 200)

	_ = invoice("DRAFT-1")

	// Open customer credit notes and manual journals can be voided; both write a reversing journal.
	accounts := expectStatus(t, do(t, h, "GET", "/api/v1/accounts?org_id="+o, "", owner), 200)["accounts"].([]any)
	var sales, bank int64
	for _, raw := range accounts {
		a := raw.(map[string]any)
		switch a["code"] {
		case "4000":
			sales = int64(a["id"].(float64))
		case "1000":
			bank = int64(a["id"].(float64))
		}
	}
	if sales == 0 || bank == 0 {
		t.Fatalf("seeded accounts missing: %v", accounts)
	}
	credit := expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes", `{"org_id":`+o+`,"contact_id":`+coreID(customer)+`,"credit_date":"2026-10-02","lines":[{"description":"Return","account_id":`+coreID(sales)+`,"quantity":1,"unit_price_cents":2000,"gst_code":"NONE"}]}`, owner), 201)
	creditID := int64(credit["id"].(float64))
	if credit["credit_number"] != "C-00002" {
		t.Fatalf("credit number=%v", credit["credit_number"])
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes/"+coreID(creditID)+"/stage", `{"org_id":`+o+`,"stage":"open"}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/credit-notes/"+coreID(creditID)+"/void", `{"org_id":`+o+`,"reversal_date":"2026-10-21"}`, owner), 200)
	var creditStatus string
	if err := pool.QueryRow(t.Context(), `SELECT status FROM customer_credit_notes WHERE org_id=$1 AND id=$2`, org, creditID).Scan(&creditStatus); err != nil || creditStatus != "void" {
		t.Fatalf("credit status=%q err=%v", creditStatus, err)
	}
	journal := expectStatus(t, do(t, h, "POST", "/api/v1/journals", `{"org_id":`+o+`,"entry_date":"2026-10-02","memo":"Accrual","idempotency_key":"life-journal","lines":[{"account_id":`+coreID(sales)+`,"debit_cents":1000},{"account_id":`+coreID(bank)+`,"credit_cents":1000}]}`, owner), 201)
	journalID := int64(journal["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/journals/"+coreID(journalID)+"/void", `{"org_id":`+o+`,"reversal_date":"2026-10-22"}`, owner), 200)
	journals := expectStatus(t, do(t, h, "GET", "/api/v1/journals?org_id="+o+"&status=voided", "", owner), 200)["journals"].([]any)
	if len(journals) != 1 || journals[0].(map[string]any)["status"] != "voided" {
		t.Fatalf("journal void filter=%v", journals)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/journals/"+coreID(journalID)+"/void", `{"org_id":`+o+`,"reversal_date":"2026-10-23"}`, owner), 409)

	// List tabs carry the derived lifecycle and every count.
	draftCounts := expectStatus(t, do(t, h, "GET", "/api/v1/invoices?org_id="+o+"&status=draft", "", owner), 200)
	counts := draftCounts["counts"].(map[string]any)
	if counts["draft"] != float64(1) || counts["paid"] != float64(1) || counts["awaiting_payment"] != float64(2) || counts["voided"] != float64(1) || counts["all"] != float64(5) {
		t.Fatalf("invoice counts=%v", counts)
	}
	if got := len(draftCounts["invoices"].([]any)); got != 1 {
		t.Fatalf("draft filter rows=%d", got)
	}
	voidedFilter := expectStatus(t, do(t, h, "GET", "/api/v1/invoices?org_id="+o+"&status=voided", "", owner), 200)
	if len(voidedFilter["invoices"].([]any)) != 1 {
		t.Fatalf("voided filter=%v", voidedFilter)
	}
	bills := expectStatus(t, do(t, h, "GET", "/api/v1/bills?org_id="+o+"&status=voided", "", owner), 200)
	billCounts := bills["counts"].(map[string]any)
	if billCounts["voided"] != float64(1) || billCounts["all"] != float64(1) {
		t.Fatalf("bill counts=%v", billCounts)
	}
}
