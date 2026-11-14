package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDocumentRetentionGuard covers R25/D10: a document linked to a posted record inside the retention
// period, or under legal hold, cannot be permanently deleted; the trash still works, and older links can go.
func TestDocumentRetentionGuard(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "retention@example.com")
	org := orgFromList(t, a, owner, `{"name":"Retention co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	expAcct := int64(expectStatus(t, do(t, h, "POST", "/api/v1/accounts", `{"org_id":`+o+`,"code":"6200","name":"Parts","type":"expense"}`, owner), 201)["id"].(float64))
	upload := func() int64 {
		t.Helper()
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		part, _ := w.CreateFormFile("file", "receipt.txt")
		part.Write([]byte("receipt"))
		w.Close()
		req := httptest.NewRequest("POST", "/api/v1/documents?org_id="+o, &body)
		req.Header.Set("Content-Type", w.FormDataContentType())
		req.Header.Set("Origin", "http://app.test")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: owner})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 201 {
			t.Fatalf("upload %d %s", rec.Code, rec.Body)
		}
		return int64(decodeBody(t, rec)["id"].(float64))
	}
	action := func(id int64, act string) *httptest.ResponseRecorder {
		return do(t, h, "POST", "/api/v1/documents/"+itoa(id)+"/action", `{"org_id":`+o+`,"action":"`+act+`"}`, owner)
	}

	recentExpense := int64(expectStatus(t, do(t, h, "POST", "/api/v1/expenses", `{"org_id":`+o+`,"expense_date":"2026-10-01","payee":"Shop","description":"Recent","expense_account_id":`+itoa(expAcct)+`,"amount_cents":100}`, owner), 201)["id"].(float64))
	recentDoc := upload()
	expectStatus(t, do(t, h, "POST", "/api/v1/documents/"+itoa(recentDoc)+"/receipt-link", `{"org_id":`+o+`,"expense_id":`+itoa(recentExpense)+`}`, owner), 201)
	if rec := action(recentDoc, "trash"); rec.Code != 200 {
		t.Fatalf("trash refused: %d %s", rec.Code, rec.Body)
	}
	if rec := action(recentDoc, "delete"); rec.Code != 409 || !strings.Contains(rec.Body.String(), "document_retained") {
		t.Fatalf("recent linked document delete = %d %s, want 409 document_retained", rec.Code, rec.Body)
	}

	// Legal hold blocks even an unlinked document.
	held := upload()
	if _, err := a.db.Exec(t.Context(), `UPDATE documents SET legal_hold=true WHERE org_id=$1 AND id=$2`, org, held); err != nil {
		t.Fatal(err)
	}
	if rec := action(held, "trash"); rec.Code != 200 {
		t.Fatalf("trash held: %d", rec.Code)
	}
	if rec := action(held, "delete"); rec.Code != 409 {
		t.Fatalf("held document delete = %d, want 409", rec.Code)
	}

	// Outside the retention window the link no longer blocks deletion.
	oldExpense := int64(expectStatus(t, do(t, h, "POST", "/api/v1/expenses", `{"org_id":`+o+`,"expense_date":"2019-01-01","payee":"Shop","description":"Old","expense_account_id":`+itoa(expAcct)+`,"amount_cents":100}`, owner), 201)["id"].(float64))
	oldDoc := upload()
	expectStatus(t, do(t, h, "POST", "/api/v1/documents/"+itoa(oldDoc)+"/receipt-link", `{"org_id":`+o+`,"expense_id":`+itoa(oldExpense)+`}`, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/documents/"+itoa(oldDoc)+"/action", `{"org_id":`+o+`,"action":"trash"}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/documents/"+itoa(oldDoc)+"/action", `{"org_id":`+o+`,"action":"delete"}`, owner), 200)

	// The guard is in the database too, not only in the handler.
	if _, err := a.db.Exec(t.Context(), `DELETE FROM documents WHERE org_id=$1 AND id=$2`, org, recentDoc); err == nil {
		t.Fatal("database accepted deleting a retained document")
	}
}
