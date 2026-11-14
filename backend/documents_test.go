package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDocumentsInboxFoldersTrash(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "docs-owner@example.com")
	viewer := seedSession(t, a, "docs-viewer@example.com")
	outsider := seedSession(t, a, "docs-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Docs co","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(t.Context(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='docs-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	o := "org_id=" + itoa(org)
	upload := func(token, query, name string, data []byte) *httptest.ResponseRecorder {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		part, _ := w.CreateFormFile("file", name)
		part.Write(data)
		w.Close()
		req := httptest.NewRequest("POST", "/api/v1/documents?"+o+query, &body)
		req.Header.Set("Content-Type", w.FormDataContentType())
		req.Header.Set("Origin", "http://app.test")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	count := func(q string) int {
		return len(expectStatus(t, do(t, h, "GET", "/api/v1/documents?"+o+q, "", owner), 200)["documents"].([]any))
	}
	expectStatus(t, upload(viewer, "", "a.txt", []byte("x")), 403)
	expectStatus(t, upload(outsider, "", "a.txt", []byte("x")), 403)
	expectStatus(t, upload(owner, "", "empty.txt", nil), 422)
	doc := expectStatus(t, upload(owner, "", `..\receipt.html`, []byte("<script>x</script>")), 201)
	id := itoa(int64(doc["id"].(float64)))
	if doc["filename"] != "receipt.html" || count("&view=inbox") != 1 || count("") != 1 {
		t.Fatal(doc)
	}
	// Viewer can read and download, never as inline HTML.
	rec := do(t, h, "GET", "/api/v1/documents/"+id+"?"+o, "", viewer)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatal(rec.Code, rec.Header())
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/documents/"+id+"?"+o, "", outsider), 403)

	folderBody := `{"org_id":` + itoa(org) + `,"name":"Receipts"}`
	expectStatus(t, do(t, h, "POST", "/api/v1/document-folders", folderBody, viewer), 403)
	folder := expectStatus(t, do(t, h, "POST", "/api/v1/document-folders", folderBody, owner), 201)
	expectStatus(t, do(t, h, "POST", "/api/v1/document-folders", `{"org_id":`+itoa(org)+`,"name":"receipts"}`, owner), 409)
	fid := itoa(int64(folder["id"].(float64)))
	act := func(action, folder string) int {
		return do(t, h, "POST", "/api/v1/documents/"+id+"/action", `{"org_id":`+itoa(org)+`,"action":"`+action+`","folder_id":`+folder+`}`, owner).Code
	}
	if act("move", "999999") != 404 || act("move", fid) != 200 || count("&view=inbox") != 0 || count("&view=folder&folder_id="+fid) != 1 {
		t.Fatal("move")
	}
	// Folder with a document cannot be deleted.
	expectStatus(t, do(t, h, "DELETE", "/api/v1/document-folders/"+fid+"?"+o, "", owner), 409)
	if act("delete", "0") != 404 { // live documents cannot be deleted for good
		t.Fatal("delete live")
	}
	if act("trash", "0") != 200 || count("") != 0 || count("&view=trash") != 1 || act("move", "0") != 404 {
		t.Fatal("trash")
	}
	if act("restore", "0") != 200 || count("") != 1 || act("trash", "0") != 200 || act("delete", "0") != 200 || count("&view=trash") != 0 {
		t.Fatal("restore/delete")
	}
	if do(t, h, "DELETE", "/api/v1/document-folders/"+fid+"?"+o, "", owner).Code != 204 {
		t.Fatal("empty folder delete")
	}
}
