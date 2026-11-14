package main

import (
	"bytes"
	"context"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAttachmentsPrivacyLimitsAndDownloads(t *testing.T) {
	a, pool := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "files-owner@example.com")
	viewer := seedSession(t, a, "files-viewer@example.com")
	outsider := seedSession(t, a, "files-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Private files","fiscal_year_end":"12-31"}`)
	if _, err := pool.Exec(context.Background(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='files-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	contact := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"name":"Buyer","role":"customer"}`, owner), 201)
	path := "/api/v1/entities/contacts/" + itoa(int64(contact["id"].(float64))) + "/attachments?org_id=" + itoa(org)
	upload := func(token, target, name string, data []byte) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", target, &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Origin", "http://app.test")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	contents := []byte("<script>not rendered inline</script>")
	expectStatus(t, upload(viewer, path, "denied.html", contents), 403)
	expectStatus(t, upload(outsider, path, "denied.html", contents), 403)
	expectStatus(t, upload(owner, strings.Replace(path, "/contacts/", "/unknown/", 1), "denied.html", contents), 400)
	expectStatus(t, upload(owner, strings.Replace(path, "/contacts/1/", "/contacts/999999/", 1), "denied.html", contents), 404)
	expectStatus(t, upload(owner, path, "empty.txt", nil), 422)
	expectStatus(t, upload(owner, path, "large.txt", make([]byte, (10<<20)+1)), 422)
	created := expectStatus(t, upload(owner, path, `..\private\proof.html`, contents), 201)
	if created["filename"] != "proof.html" {
		t.Fatalf("unsafe filename: %v", created)
	}
	download := "/api/v1/attachments/" + itoa(int64(created["id"].(float64))) + "?org_id=" + itoa(org)
	for _, token := range []string{owner, viewer} {
		rec := do(t, h, "GET", download, "", token)
		if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), contents) || rec.Header().Get("Content-Type") != "application/octet-stream" || rec.Header().Get("Content-Security-Policy") != "sandbox" {
			t.Fatalf("unsafe or incorrect download: %d %v", rec.Code, rec.Header())
		}
		disposition, params, err := mime.ParseMediaType(rec.Header().Get("Content-Disposition"))
		if err != nil || disposition != "attachment" || params["filename"] != "proof.html" {
			t.Fatalf("unsafe disposition: %v %v", params, err)
		}
	}
	expectStatus(t, do(t, h, "GET", download+"&preview=1", "", owner), 415)
	png := []byte("\x89PNG\r\n\x1a\n00000000")
	image := expectStatus(t, upload(owner, path, "photo.png", png), 201)
	preview := "/api/v1/attachments/" + itoa(int64(image["id"].(float64))) + "?org_id=" + itoa(org) + "&preview=1"
	rec := do(t, h, "GET", preview, "", viewer)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("X-Content-Type-Options") != "nosniff" || !bytes.Equal(rec.Body.Bytes(), png) {
		t.Fatalf("incorrect preview: %d %v", rec.Code, rec.Header())
	}
	expectStatus(t, do(t, h, "GET", preview, "", outsider), 403)
	expectStatus(t, do(t, h, "GET", download, "", outsider), 403)
	expectStatus(t, do(t, h, "GET", path, "", outsider), 403)
	otherOrg := orgFromList(t, a, outsider, `{"name":"Other files","fiscal_year_end":"12-31"}`)
	expectStatus(t, do(t, h, "GET", strings.Replace(download, "org_id="+itoa(org), "org_id="+itoa(otherOrg), 1), "", outsider), 404)
	expectStatus(t, upload(outsider, strings.Replace(path, "org_id="+itoa(org), "org_id="+itoa(otherOrg), 1), "foreign.txt", contents), 404)
	for i := 2; i < 10; i++ {
		expectStatus(t, upload(owner, path, "proof.txt", contents), 201)
	}
	expectStatus(t, upload(owner, path, "excess.txt", contents), 422)
	listed := expectStatus(t, do(t, h, "GET", path, "", viewer), 200)["attachments"].([]any)
	if len(listed) != 10 {
		t.Fatalf("wrong attachment count: %d", len(listed))
	}
}
