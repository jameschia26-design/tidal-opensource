package main

import (
	"testing"
)

// Phase 3a banking parity: bank account records, per-account statement import, ranked matches,
// creating a transaction from a line, saved reconciliations and rule any/all actions.

func TestBankAccountsParity(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "bank-accounts@example.com")
	org := orgFromList(t, a, owner, `{"name":"Bank accounts","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	// Add a bank account without a code: the server picks the next free one.
	created := expectStatus(t, do(t, h, "POST", "/api/v1/bank/accounts", `{"org_id":`+o+`,"name":"DBS Current","account_number":"123-456-789","account_type":"bank","opening_balance_cents":250000,"opening_balance_date":"2026-01-01","currency":"SGD"}`, owner), 201)
	bankID := int64(created["id"].(float64))
	if created["code"] != "1001" {
		t.Fatalf("auto code = %v", created["code"])
	}
	list := expectStatus(t, do(t, h, "GET", "/api/v1/bank/accounts?org_id="+o, "", owner), 200)["accounts"].([]any)
	if len(list) != 1 {
		t.Fatal(list)
	}
	first := list[0].(map[string]any)
	if first["name"] != "DBS Current" || first["account_number"] != "123-456-789" ||
		first["opening_balance_cents"] != float64(250000) || first["ledger_balance_cents"] != float64(0) ||
		first["imported_balance_cents"] != float64(250000) {
		t.Fatalf("bank account %v", first)
	}
	// Editing statement details keeps the ledger account; archiving hides it from new statements but
	// keeps its history readable.
	expectStatus(t, do(t, h, "PUT", "/api/v1/bank/accounts/"+itoa(bankID), `{"org_id":`+o+`,"name":"DBS Current 2","account_number":"999","account_type":"credit_card","opening_balance_cents":0,"statement_balance_cents":12345,"currency":"SGD","archived":true}`, owner), 200)
	first = expectStatus(t, do(t, h, "GET", "/api/v1/bank/accounts?org_id="+o, "", owner), 200)["accounts"].([]any)[0].(map[string]any)
	if first["name"] != "DBS Current 2" || first["account_type"] != "credit_card" || first["archived"] != true ||
		first["statement_balance_cents"] != float64(12345) {
		t.Fatalf("edited bank account %v", first)
	}
	// A non-bank account cannot be edited as one, and a viewer cannot add one.
	acct := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6200","name":"Parts","type":"expense"}`, owner), 201)
	expectStatus(t, do(t, h, "PUT", "/api/v1/bank/accounts/"+itoa(int64(acct["id"].(float64))), `{"org_id":`+o+`,"name":"Parts","account_type":"bank","currency":"SGD"}`, owner), 422)
	viewer := seedSession(t, a, "bank-accounts-viewer@example.com")
	if _, err := a.db.Exec(t.Context(), `INSERT INTO memberships(org_id,user_id,role) VALUES($1,(SELECT id FROM users WHERE email='bank-accounts-viewer@example.com'),'viewer')`, org); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/accounts", `{"org_id":`+o+`,"name":"Sneaky","account_type":"bank"}`, viewer), 403)
}

func TestBankStatementMappingAndDedupe(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "bank-map@example.com")
	org := orgFromList(t, a, owner, `{"name":"Bank map","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	bank1 := int64(expectStatus(t, do(t, h, "POST", "/api/v1/bank/accounts", `{"org_id":`+o+`,"code":"1050","name":"Bank A","account_type":"bank"}`, owner), 201)["id"].(float64))
	bank2 := int64(expectStatus(t, do(t, h, "POST", "/api/v1/bank/accounts", `{"org_id":`+o+`,"code":"1060","name":"Bank B","account_type":"bank"}`, owner), 201)["id"].(float64))
	// Columns arrive as amount, date, reference, description — mapped by position.
	mapping := "?org_id=" + o + "&bank_account_id=" + itoa(bank1) + "&date_col=2&description_col=4&reference_col=3&amount_col=1"
	csv := "Amount,Date,Reference,Description\n100.00,2026-07-01,DEP-1,Customer deposit\n-40.00,2026-07-02,CHQ-1,Supplier cheque\n"
	got := expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements"+mapping, csv, owner), 200)
	if got["imported"] != float64(2) {
		t.Fatalf("mapped import %v", got)
	}
	// Same file again, and the same movement with different bank wording: both are duplicates.
	got = expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements"+mapping, csv, owner), 200)
	if got["imported"] != float64(0) || got["duplicates_skipped"] != float64(2) {
		t.Fatalf("reimport %v", got)
	}
	same := "Amount,Date,Reference,Description\n100.00,2026-07-01,DEP-1,CUSTOMER DEPOSIT REV"
	got = expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements"+mapping, same, owner), 200)
	if got["imported"] != float64(0) {
		t.Fatalf("same movement with new wording imported again: %v", got)
	}
	// The same movement on another account is its own statement line, not a duplicate.
	other := "?org_id=" + o + "&bank_account_id=" + itoa(bank2)
	got = expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements"+other, "Date,Description,Reference,Amount\n2026-07-01,Customer deposit,DEP-1,100.00\n", owner), 200)
	if got["imported"] != float64(1) {
		t.Fatalf("same line on another account %v", got)
	}
	for _, bank := range []int64{bank1, bank2} {
		lines := expectStatus(t, do(t, h, "GET", "/api/v1/bank/lines?org_id="+o+"&bank_account_id="+itoa(bank), "", owner), 200)["lines"].([]any)
		if len(lines) != 2 && bank == bank1 || len(lines) != 1 && bank == bank2 {
			t.Fatalf("lines on account %d: %d", bank, len(lines))
		}
	}
	// Bad mappings are refused, not guessed.
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements?org_id="+o+"&date_col=1&amount_col=1", "x", owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements?org_id="+o+"&date_col=9", "2026-07-01,x\r\n", owner), 400)
}

func TestBankSuggestionsRankSameDayFirst(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "bank-suggest@example.com")
	org := orgFromList(t, a, owner, `{"name":"Bank suggest","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	cust := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Buyer"}`, owner), 201)["id"].(float64))
	pay := func(date, key string) {
		inv := int64(expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+o+`,"contact_id":`+itoa(cust)+`,"invoice_date":"2026-07-01","due_date":"2026-07-31","lines":[{"description":"Work","quantity":1,"unit_price_cents":30000}]}`, owner), 201)["id"].(float64))
		expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(inv)+"/post?org_id="+o, "", owner), 200)
		expectStatus(t, do(t, h, "POST", "/api/v1/invoices/"+itoa(inv)+"/payments", `{"org_id":`+o+`,"amount_cents":30000,"payment_date":"`+date+`","idempotency_key":"`+key+`"}`, owner), 201)
	}
	pay("2026-07-10", "sug-1")
	pay("2026-07-09", "sug-2")
	// Payments land on the seeded bank account (1000); the statement must use the same account.
	var bank int64
	if err := a.db.QueryRow(t.Context(), `SELECT id FROM accounts WHERE org_id=$1 AND code='1000'`, org).Scan(&bank); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, do(t, h, "PUT", "/api/v1/organizations/"+o, `{"name":"Bank suggest","fiscal_year_end":"12-31","bank_match_tolerance_days":3}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements?org_id="+o+"&bank_account_id="+itoa(bank), "Date,Description,Reference,Amount\n2026-07-10,Customer receipt,SUG,300.00\n", owner), 200)
	line := expectStatus(t, do(t, h, "GET", "/api/v1/bank/lines?org_id="+o+"&bank_account_id="+itoa(bank), "", owner), 200)["lines"].([]any)[0].(map[string]any)
	lineID := int64(line["id"].(float64))
	got := expectStatus(t, do(t, h, "GET", "/api/v1/bank/lines/"+itoa(lineID)+"/suggestions?org_id="+o, "", owner), 200)
	list := got["suggestions"].([]any)
	if len(list) != 2 {
		t.Fatalf("suggestions %v", list)
	}
	first := list[0].(map[string]any)
	second := list[1].(map[string]any)
	if first["days_apart"] != float64(0) || first["date"] != "2026-07-10" {
		t.Fatalf("same-day payment not first: %v", list)
	}
	if second["days_apart"] != float64(1) || second["date"] != "2026-07-09" {
		t.Fatalf("closest payment second: %v", list)
	}
	// Once matched, a document drops out of suggestions.
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/lines/"+itoa(lineID)+"/match", `{"org_id":`+o+`,"kind":"invoice","payment_id":`+itoa(int64(first["id"].(float64)))+`}`, owner), 200)
	got = expectStatus(t, do(t, h, "GET", "/api/v1/bank/lines/"+itoa(lineID)+"/suggestions?org_id="+o, "", owner), 200)
	if len(got["suggestions"].([]any)) != 1 {
		t.Fatalf("matched payment still suggested: %v", got)
	}
}

func TestCreateTransactionFromLinePostsBalancedTransfer(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "bank-create@example.com")
	org := orgFromList(t, a, owner, `{"name":"Bank create","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	bank1 := int64(expectStatus(t, do(t, h, "POST", "/api/v1/bank/accounts", `{"org_id":`+o+`,"code":"1050","name":"Bank A","account_type":"bank"}`, owner), 201)["id"].(float64))
	bank2 := int64(expectStatus(t, do(t, h, "POST", "/api/v1/bank/accounts", `{"org_id":`+o+`,"code":"1060","name":"Bank B","account_type":"bank"}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements?org_id="+o+"&bank_account_id="+itoa(bank1), "Date,Description,Reference,Amount\n2026-07-05,To savings,TRF-1,-500.00\n", owner), 200)
	line := expectStatus(t, do(t, h, "GET", "/api/v1/bank/lines?org_id="+o+"&bank_account_id="+itoa(bank1), "", owner), 200)["lines"].([]any)[0].(map[string]any)
	lineID := int64(line["id"].(float64))
	created := expectStatus(t, do(t, h, "POST", "/api/v1/bank/lines/"+itoa(lineID)+"/create", `{"org_id":`+o+`,"kind":"transfer","account_id":`+itoa(bank2)+`,"memo":"To savings"}`, owner), 201)
	journalID := int64(created["journal_id"].(float64))
	// The transfer is one journal with two balanced legs, moving money out of A and into B.
	var debit, credit, legs int64
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(SUM(debit_cents),0),COALESCE(SUM(credit_cents),0),count(*) FROM journal_lines WHERE org_id=$1 AND journal_id=$2`, org, journalID).Scan(&debit, &credit, &legs); err != nil {
		t.Fatal(err)
	}
	if legs != 2 || debit != 50000 || credit != 50000 {
		t.Fatalf("transfer journal legs=%d debit=%d credit=%d", legs, debit, credit)
	}
	var intoB, outOfA int64
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(SUM(debit_cents-credit_cents),0) FROM journal_lines WHERE org_id=$1 AND journal_id=$2 AND account_id=$3`, org, journalID, bank2).Scan(&intoB); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow(t.Context(), `SELECT COALESCE(SUM(debit_cents-credit_cents),0) FROM journal_lines WHERE org_id=$1 AND journal_id=$2 AND account_id=$3`, org, journalID, bank1).Scan(&outOfA); err != nil {
		t.Fatal(err)
	}
	if intoB != 50000 || outOfA != -50000 {
		t.Fatalf("transfer legs A=%d B=%d", outOfA, intoB)
	}
	line = expectStatus(t, do(t, h, "GET", "/api/v1/bank/lines?org_id="+o+"&bank_account_id="+itoa(bank1), "", owner), 200)["lines"].([]any)[0].(map[string]any)
	if line["matched_kind"] != "transfer" || int64(line["matched_id"].(float64)) != journalID {
		t.Fatalf("line not matched to the transfer: %v", line)
	}
	// Creating again on the matched line must not post a second journal.
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/lines/"+itoa(lineID)+"/create", `{"org_id":`+o+`,"kind":"transfer","account_id":`+itoa(bank2)+`}`, owner), 409)
}

func TestReconciliationSavedOnlyAtZeroDifference(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "bank-reconcile-save@example.com")
	org := orgFromList(t, a, owner, `{"name":"Bank reconcile","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	bank := int64(expectStatus(t, do(t, h, "POST", "/api/v1/bank/accounts", `{"org_id":`+o+`,"code":"1050","name":"Bank A","account_type":"bank"}`, owner), 201)["id"].(float64))
	revenue := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"4100","name":"Other income","type":"revenue"}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements?org_id="+o+"&bank_account_id="+itoa(bank), "Date,Description,Reference,Amount\n2026-07-03,Deposit one,D1,100.00\n2026-07-04,Deposit two,D2,50.00\n", owner), 200)
	lines := expectStatus(t, do(t, h, "GET", "/api/v1/bank/lines?org_id="+o+"&bank_account_id="+itoa(bank), "", owner), 200)["lines"].([]any)
	ids := map[string]int64{}
	for _, raw := range lines {
		l := raw.(map[string]any)
		ids[l["reference"].(string)] = int64(l["id"].(float64))
	}
	create := func(ref string) {
		expectStatus(t, do(t, h, "POST", "/api/v1/bank/lines/"+itoa(ids[ref])+"/create", `{"org_id":`+o+`,"kind":"receive","account_id":`+itoa(revenue)+`}`, owner), 201)
	}
	period := `{"org_id":` + o + `,"bank_account_id":` + itoa(bank) + `,"from":"2026-07-01","to":"2026-07-31"}`
	// An unmatched line blocks the save even though the running figures are not the problem yet.
	create("D1")
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/reconciliations", period, owner), 422)
	// Opening balance is part of the statement and, until it is posted, not part of the ledger.
	expectStatus(t, do(t, h, "PUT", "/api/v1/bank/accounts/"+itoa(bank), `{"org_id":`+o+`,"name":"Bank A","account_type":"bank","opening_balance_cents":10000,"currency":"SGD"}`, owner), 200)
	create("D2")
	got := expectStatus(t, do(t, h, "POST", "/api/v1/bank/reconciliations", period, owner), 422)
	if got["error"] != "difference_not_zero" {
		t.Fatalf("opening balance mismatch %v", got)
	}
	// Clear the opening balance: statement and ledger now agree.
	expectStatus(t, do(t, h, "PUT", "/api/v1/bank/accounts/"+itoa(bank), `{"org_id":`+o+`,"name":"Bank A","account_type":"bank","opening_balance_cents":0,"currency":"SGD"}`, owner), 200)
	figures := expectStatus(t, do(t, h, "GET", "/api/v1/bank/reconcile?org_id="+o+"&bank_account_id="+itoa(bank)+"&from=2026-07-01&to=2026-07-31", "", owner), 200)
	if figures["difference_cents"] != float64(0) || figures["statement_cents"] != float64(15000) || figures["unmatched_lines"] != float64(0) {
		t.Fatalf("figures %v", figures)
	}
	saved := expectStatus(t, do(t, h, "POST", "/api/v1/bank/reconciliations", period, owner), 201)
	if saved["difference_cents"] != float64(0) {
		t.Fatalf("saved %v", saved)
	}
	list := expectStatus(t, do(t, h, "GET", "/api/v1/bank/reconciliations?org_id="+o+"&bank_account_id="+itoa(bank), "", owner), 200)["reconciliations"].([]any)
	if len(list) != 1 {
		t.Fatal(list)
	}
	record := list[0].(map[string]any)
	if record["statement_cents"] != float64(15000) || record["ledger_cents"] != float64(15000) || record["created_by"] != "bank-reconcile-save@example.com" {
		t.Fatalf("saved reconciliation %v", record)
	}
	accounts := expectStatus(t, do(t, h, "GET", "/api/v1/bank/accounts?org_id="+o, "", owner), 200)["accounts"].([]any)
	var carried any
	for _, raw := range accounts {
		acc := raw.(map[string]any)
		if int64(acc["id"].(float64)) == bank {
			carried = acc["statement_balance_cents"]
		}
	}
	if carried != float64(15000) {
		t.Fatalf("statement balance not carried: %v", accounts)
	}
	var audited int
	if err := a.db.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE org_id=$1 AND action='bank.reconciled'`, org).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 1 {
		t.Fatalf("audits=%d", audited)
	}
}

func TestBankRuleAnyAllAndActions(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "bank-rules-actions@example.com")
	org := orgFromList(t, a, owner, `{"name":"Bank rules","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	bank1 := int64(expectStatus(t, do(t, h, "POST", "/api/v1/bank/accounts", `{"org_id":`+o+`,"code":"1050","name":"Bank A","account_type":"bank"}`, owner), 201)["id"].(float64))
	bank2 := int64(expectStatus(t, do(t, h, "POST", "/api/v1/bank/accounts", `{"org_id":`+o+`,"code":"1060","name":"Bank B","account_type":"bank"}`, owner), 201)["id"].(float64))
	fees := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6200","name":"Bank fees","type":"expense"}`, owner), 201)["id"].(float64))
	cust := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Acme Pte Ltd"}`, owner), 201)["id"].(float64))
	expectStatus(t, do(t, h, "POST", "/api/v1/tracking/categories", `{"org_id":`+o+`,"name":"Region","options":["North","South"]}`, owner), 201)
	cats := expectStatus(t, do(t, h, "GET", "/api/v1/tracking/categories?org_id="+o, "", owner), 200)["categories"].([]any)
	catID := int64(cats[0].(map[string]any)["id"].(float64))
	optionID := int64(cats[0].(map[string]any)["options"].([]any)[0].(map[string]any)["id"].(float64))
	// Priority 1 is the all rule: it must not match a line that only has one of its two texts.
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/rules", `{"org_id":`+o+`,"name":"All rule","direction":"receive","contains_text":"ACME","account_id":`+itoa(fees)+`,"priority":1,"condition_match":"all","conditions":[{"field":"description","value":"INVOICE"}],"contact_id":`+itoa(cust)+`,"tax_rate_code":"SR","tracking":{"`+itoa(catID)+`":`+itoa(optionID)+`}}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/rules", `{"org_id":`+o+`,"name":"Any rule","direction":"receive","contains_text":"ACME","account_id":`+itoa(fees)+`,"priority":2,"condition_match":"any","conditions":[{"field":"description","value":"INVOICE"}],"contact_id":`+itoa(cust)+`,"tax_rate_code":"SR","tracking":{"`+itoa(catID)+`":`+itoa(optionID)+`}}`, owner), 201)
	// A transfer rule suggests the other bank account; spend rules must not offer on money in.
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/rules", `{"org_id":`+o+`,"name":"Transfer rule","direction":"transfer","contains_text":"TO SAVINGS","account_id":`+itoa(bank2)+`,"priority":1}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/rules", `{"org_id":`+o+`,"name":"Spend rule","direction":"spend","contains_text":"ACME","account_id":`+itoa(fees)+`,"priority":1}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/bank/statements?org_id="+o+"&bank_account_id="+itoa(bank1),
		"Date,Description,Reference,Amount\n2026-07-02,ACME payment,P1,100.00\n2026-07-03,To savings,TRF, -75.00\n", owner), 200)
	lines := expectStatus(t, do(t, h, "GET", "/api/v1/bank/lines?org_id="+o+"&bank_account_id="+itoa(bank1), "", owner), 200)["lines"].([]any)
	var paymentLine, transferLine map[string]any
	for _, raw := range lines {
		l := raw.(map[string]any)
		if l["reference"] == "P1" {
			paymentLine = l
		}
		if l["reference"] == "TRF" {
			transferLine = l
		}
	}
	sug, _ := paymentLine["suggestion"].(map[string]any)
	if sug == nil || sug["name"] != "Any rule" {
		t.Fatalf("any-mode rule did not win over the all-mode rule: %v", paymentLine)
	}
	if int64(sug["contact_id"].(float64)) != cust || sug["tax_rate_code"] != "SR" {
		t.Fatalf("rule actions missing: %v", sug)
	}
	tracking := sug["tracking"].(map[string]any)
	if int64(tracking[itoa(catID)].(float64)) != optionID {
		t.Fatalf("tracking action missing: %v", sug)
	}
	sug, _ = transferLine["suggestion"].(map[string]any)
	if sug == nil || sug["direction"] != "transfer" || int64(sug["account_id"].(float64)) != bank2 {
		t.Fatalf("transfer suggestion %v", transferLine)
	}
	// Suggestions never post: no journal exists for either line.
	var journals int
	if err := a.db.QueryRow(t.Context(), `SELECT count(*) FROM journals WHERE org_id=$1`, org).Scan(&journals); err != nil {
		t.Fatal(err)
	}
	if journals != 0 {
		t.Fatalf("rules posted %d journals", journals)
	}
}
