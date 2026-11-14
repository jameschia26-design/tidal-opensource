package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReceiptInboxLinksOnce(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "receipt-owner@example.com")
	other := seedSession(t, a, "receipt-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Receipts Co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	account := expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6100","name":"Supplies","type":"expense"}`, owner), 201)
	expense := expectStatus(t, do(t, h, "POST", "/api/v1/expenses", `{"org_id":`+o+`,"expense_date":"2026-10-20","payee":"Shop","description":"Pens","expense_account_id":`+itoa(int64(account["id"].(float64)))+`,"amount_cents":900}`, owner), 201)
	expenseID := itoa(int64(expense["id"].(float64)))
	// Upload a receipt file to Documents (the Receipts Inbox reads from there).
	doc := uploadTestDocument(t, h, owner, o, "receipt.txt", []byte("receipt for pens"))
	docID := itoa(doc)

	list := expectStatus(t, do(t, h, "GET", "/api/v1/receipts/unlinked?org_id="+o, "", owner), 200)["receipts"].([]any)
	if len(list) != 1 {
		t.Fatal(list)
	}
	expectStatus(t, do(t, h, "POST", "/api/v1/documents/"+docID+"/receipt-link", `{"org_id":`+o+`,"expense_id":`+expenseID+`}`, other), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/documents/"+docID+"/receipt-link", `{"org_id":`+o+`,"expense_id":`+expenseID+`}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/documents/"+docID+"/receipt-link", `{"org_id":`+o+`,"expense_id":`+expenseID+`}`, owner), 409)
	if l := expectStatus(t, do(t, h, "GET", "/api/v1/receipts/unlinked?org_id="+o, "", owner), 200)["receipts"].([]any); len(l) != 0 {
		t.Fatal("linked receipt still in inbox", l)
	}
	// Refuse a document that does not exist in this company.
	expectStatus(t, do(t, h, "POST", "/api/v1/documents/999999/receipt-link", `{"org_id":`+o+`,"expense_id":`+expenseID+`}`, owner), 404)
}

// uploadTestDocument uploads a file to Documents and returns its id.
func uploadTestDocument(t *testing.T, h http.Handler, token, org string, name string, data []byte) int64 {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	w.Close()
	req := httptest.NewRequest("POST", "/api/v1/documents?org_id="+org, &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Origin", "http://app.test")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return int64(expectStatus(t, rec, 201)["id"].(float64))
}
