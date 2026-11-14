package main

import (
	"encoding/json"
	"testing"
)

func TestExpenseItemizationAccountsAndRetry(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "expense-details@example.com")
	other := seedSession(t, a, "expense-details-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Itemized expenses","fiscal_year_end":"12-31"}`)
	foreignOrg := orgFromList(t, a, other, `{"name":"Other expenses","fiscal_year_end":"12-31"}`)
	createAccount := func(code, typ string) int64 {
		return int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+itoa(org)+`,"name":"Account `+code+`","code":"`+code+`","type":"`+typ+`"}`, owner), 201)["id"].(float64))
	}
	bank := createAccount("1010", "asset")
	office := createAccount("6110", "expense")
	travel := createAccount("6120", "expense")
	vendor := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"name":"Vendor","role":"supplier"}`, owner), 201)["id"].(float64))
	customer := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"name":"Customer","role":"customer"}`, owner), 201)["id"].(float64))
	foreign := int64(expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(foreignOrg)+`,"name":"Foreign","role":"supplier"}`, other), 201)["id"].(float64))
	in := expenseInput{OrgID: org, ExpenseDate: "2026-10-29", Payee: "Vendor", Description: "Office visit", PaidThroughAccountID: bank, VendorID: vendor, CustomerID: customer, AmountCents: 3500, IdempotencyKey: "expense-one", Items: []expenseItem{{Description: "Paper", AccountID: office, AmountCents: 1000}, {Description: "Taxi", AccountID: travel, AmountCents: 2500}}}
	post := func(status int) map[string]any {
		t.Helper()
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		return expectStatus(t, do(t, h, "POST", "/api/v1/expenses", string(b), owner), status)
	}
	created := post(201)
	if post(200)["id"] != created["id"] {
		t.Fatal("retry created another expense")
	}
	in.Notes = "changed"
	post(409)
	in.Notes = ""
	in.IdempotencyKey = "expense-two"
	post(201)
	in.IdempotencyKey = "invalid"
	in.VendorID = foreign
	post(422)
	in.VendorID = customer
	post(422)
	in.VendorID = vendor
	in.CustomerID = vendor
	post(422)
	in.CustomerID = customer
	in.PaidThroughAccountID = office
	post(422)
	in.PaidThroughAccountID = bank
	in.AmountCents = 3501
	post(400)
	in.AmountCents = 3500
	in.Items[1].AccountID = bank
	post(422)
	listed := expectStatus(t, do(t, h, "GET", "/api/v1/expenses?org_id="+itoa(org), "", owner), 200)["expenses"].([]any)
	if len(listed) != 2 {
		t.Fatal(listed)
	}
	row := listed[0].(map[string]any)
	if row["paid_through_account_id"] != float64(bank) || row["vendor_id"] != float64(vendor) || row["customer_id"] != float64(customer) || len(row["items"].([]any)) != 2 {
		t.Fatal(row)
	}
	for account, want := range map[int64]int64{bank: -7000, office: 2000, travel: 5000} {
		var got int64
		if err := pool.QueryRow(t.Context(), `SELECT COALESCE(sum(debit_cents-credit_cents),0) FROM journal_lines WHERE org_id=$1 AND account_id=$2`, org, account).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("account %d got %d want %d", account, got, want)
		}
	}
}
