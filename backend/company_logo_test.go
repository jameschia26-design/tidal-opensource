package main

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// logoPNG builds a small solid-colour PNG, so the test needs no fixture file.
func logoPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 20, G: 120, B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// hugeLogoPNG builds a PNG whose header claims huge dimensions, so it is a few bytes on disk.
func hugeLogoPNG(t *testing.T, w, h uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	chunk := func(typ string, data []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(data)))
		payload := append([]byte(typ), data...)
		buf.Write(n[:])
		buf.Write(payload)
		binary.BigEndian.PutUint32(n[:], crc32.ChecksumIEEE(payload))
		buf.Write(n[:])
	}
	buf.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 2
	chunk("IHDR", ihdr)
	chunk("IDAT", []byte{})
	chunk("IEND", nil)
	return buf.Bytes()
}

func uploadLogo(t *testing.T, h http.Handler, orgID int64, token string, contents []byte, name string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/organizations/"+itoa(orgID)+"/logo", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", "http://app.test")
	req.AddCookie(&http.Cookie{Name: "tidal_session", Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestCompanyLogo(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "logo-owner@example.com")
	viewer := seedSession(t, a, "logo-viewer@example.com")
	org := orgFromList(t, a, owner, `{"name":"Logo co","fiscal_year_end":"12-31"}`)
	if _, err := a.db.Exec(t.Context(), `INSERT INTO memberships(org_id,user_id,role) SELECT $1,id,'viewer' FROM users WHERE email='logo-viewer@example.com'`, org); err != nil {
		t.Fatal(err)
	}
	png := logoPNG(t)
	// Non-images are refused rather than stored and broken at print time.
	if w := uploadLogo(t, h, org, owner, []byte("not an image"), "notes.txt"); w.Code != 422 {
		t.Fatalf("text upload status = %d, want 422", w.Code)
	}
	if w := uploadLogo(t, h, org, viewer, png, "logo.png"); w.Code != 403 {
		t.Fatalf("viewer upload status = %d, want 403", w.Code)
	}
	if w := uploadLogo(t, h, org, owner, png, "logo.png"); w.Code != 200 {
		t.Fatalf("upload status = %d body %s", w.Code, w.Body.String())
	}
	get := do(t, h, "GET", "/api/v1/organizations/"+itoa(org)+"/logo", "", owner)
	if get.Code != 200 || get.Header().Get("Content-Type") != "image/png" || !bytes.Equal(get.Body.Bytes(), png) {
		t.Fatalf("get logo status %d type %q", get.Code, get.Header().Get("Content-Type"))
	}
	// A posted invoice PDF carries the image object and draws it once.
	contact := seedContact(t, a, org, owner)
	inv := expectStatus(t, do(t, h, "POST", "/api/v1/invoices", `{"org_id":`+itoa(org)+`,"contact_id":`+itoa(contact)+`,"invoice_date":"2026-04-01","due_date":"2026-04-30","lines":[{"description":"Design","quantity":1,"unit_price_cents":12000}]}`, owner), 201)
	id := itoa(int64(inv["id"].(float64)))
	do(t, h, "POST", "/api/v1/invoices/"+id+"/post?org_id="+itoa(org), "", owner)
	pdf := do(t, h, "GET", "/api/v1/documents/invoices/"+id+"/pdf?org_id="+itoa(org), "", owner)
	if pdf.Code != 200 || !strings.Contains(pdf.Body.String(), "/Subtype /Image") || !strings.Contains(pdf.Body.String(), "/Im1 Do") {
		t.Fatalf("pdf missing logo, status %d", pdf.Code)
	}
	// Deleting the logo leaves documents printable without it.
	if del := do(t, h, "DELETE", "/api/v1/organizations/"+itoa(org)+"/logo", "", viewer); del.Code != 403 {
		t.Fatalf("viewer delete status = %d, want 403", del.Code)
	}
	if del := do(t, h, "DELETE", "/api/v1/organizations/"+itoa(org)+"/logo", "", owner); del.Code != 204 {
		t.Fatalf("delete status = %d", del.Code)
	}
	if get := do(t, h, "GET", "/api/v1/organizations/"+itoa(org)+"/logo", "", owner); get.Code != 404 {
		t.Fatalf("get after delete status = %d, want 404", get.Code)
	}
	pdf = do(t, h, "GET", "/api/v1/documents/invoices/"+id+"/pdf?org_id="+itoa(org), "", owner)
	if pdf.Code != 200 || strings.Contains(pdf.Body.String(), "/Im1 Do") {
		t.Fatalf("pdf still draws a deleted logo, status %d", pdf.Code)
	}
}

// TestLogoDimensionsCapped covers R19: a tiny file describing a huge image is refused before decoding.
func TestLogoDimensionsCapped(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "logo-huge@example.com")
	org := orgFromList(t, a, owner, `{"name":"Huge logo co","fiscal_year_end":"12-31"}`)
	huge := hugeLogoPNG(t, 20000, 20000)
	if w := uploadLogo(t, h, org, owner, huge, "huge.png"); w.Code != 422 {
		t.Fatalf("oversized upload status = %d, want 422 (body %s)", w.Code, w.Body.String())
	}
	// A stored oversized logo is refused at print time from the header alone.
	if _, err := decodeLogo(huge); err == nil {
		t.Fatal("decodeLogo accepted a 20000x20000 image")
	}
	// A normal logo still works.
	if w := uploadLogo(t, h, org, owner, logoPNG(t), "logo.png"); w.Code != 200 {
		t.Fatalf("normal upload status = %d, body %s", w.Code, w.Body.String())
	}
}
