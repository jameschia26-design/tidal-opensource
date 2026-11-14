package main

import (
	"strings"
	"testing"
)

func TestContactImport(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "imp-owner@example.com")
	other := seedSession(t, a, "imp-other@example.com")
	org := orgFromList(t, a, owner, `{"name":"Import co","fiscal_year_end":"12-31"}`)
	post := func(body string, cookie string) int {
		return do(t, h, "POST", "/api/v1/contacts/import?org_id="+itoa(org), body, cookie).Code
	}
	for name, bad := range map[string]string{
		"no header col": "Name\nA\n",
		"empty name":    "ContactName\n \n",
		"bad email":     "ContactName,EmailAddress\nA,nope\n",
		"bad role":      "ContactName,Role\nA,boss\n",
		"bad site":      "ContactName,Website\nA,javascript:alert(1)\n",
		"unknown field": "ContactName,TaxID\nA,123\n",
		"short row":     "ContactName,EmailAddress\nA\n",
		"header only":   "ContactName\n",
	} {
		if c := post(bad, owner); c != 400 {
			t.Fatal(name, c)
		}
	}
	if c := post("ContactName\nA\n", other); c != 403 && c != 404 {
		t.Fatal("outsider imported", c)
	}
	// Preview reports the same counts as a real import but writes nothing.
	pv := expectStatus(t, do(t, h, "POST", "/api/v1/contacts/import?preview=1&org_id="+itoa(org), "ContactName,EmailAddress,Role\nAcme,a@example.com,supplier\nBeta,,\n", owner), 200)
	if pv["created"].(float64) != 2 || pv["updated"].(float64) != 0 || pv["rows"].(float64) != 2 {
		t.Fatal(pv)
	}
	if list := expectStatus(t, do(t, h, "GET", "/api/v1/contacts?org_id="+itoa(org), "", owner), 200)["contacts"].([]any); len(list) != 0 {
		t.Fatal("preview wrote contacts", list)
	}
	m := expectStatus(t, do(t, h, "POST", "/api/v1/contacts/import?org_id="+itoa(org), "ContactName,EmailAddress,Role\nAcme,a@example.com,supplier\nBeta,,\n", owner), 200)
	if m["created"].(float64) != 2 {
		t.Fatal(m)
	}
	m = expectStatus(t, do(t, h, "POST", "/api/v1/contacts/import?org_id="+itoa(org), "ContactName,EmailAddress,PhoneNumber\nacme,,12345\n", owner), 200)
	if m["created"].(float64) != 0 || m["updated"].(float64) != 1 {
		t.Fatal(m)
	}
	list := expectStatus(t, do(t, h, "GET", "/api/v1/contacts?org_id="+itoa(org), "", owner), 200)["contacts"].([]any)
	if len(list) != 2 {
		t.Fatal(list)
	}
	for i := 0; i < 2; i++ {
		expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+itoa(org)+`,"role":"customer","name":"Twin"}`, owner), 201)
	}
	if rec := do(t, h, "POST", "/api/v1/contacts/import?org_id="+itoa(org), "ContactName\nTwin\n", owner); rec.Code != 409 {
		t.Fatal("ambiguous name accepted", rec.Code)
	}
	// Without delete mode, an empty cell leaves the existing email in place.
	emailOf := func() string {
		for _, c := range expectStatus(t, do(t, h, "GET", "/api/v1/contacts?org_id="+itoa(org), "", owner), 200)["contacts"].([]any) {
			if m := c.(map[string]any); m["name"].(string) == "Acme" {
				return m["email"].(string)
			}
		}
		t.Fatal("Acme missing")
		return ""
	}
	if got := emailOf(); got != "a@example.com" {
		t.Fatalf("empty cell overwrote email: %q", got)
	}
	// With ?delete=1, columns present but empty are cleared; the name and role survive.
	expectStatus(t, do(t, h, "POST", "/api/v1/contacts/import?delete=1&org_id="+itoa(org), "ContactName,EmailAddress\nacme,\n", owner), 200)
	if got := emailOf(); got != "" {
		t.Fatalf("delete mode kept email: %q", got)
	}
	// Delete mode refuses a name that matches more than one contact.
	if rec := do(t, h, "POST", "/api/v1/contacts/import?delete=1&org_id="+itoa(org), "ContactName,EmailAddress\ntwin,\n", owner); rec.Code != 409 {
		t.Fatal("delete mode accepted ambiguous name", rec.Code)
	}
}

// Export writes the same column names the importer reads, so an exported file can be re-imported.
func TestContactExport(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "export-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"Export co","fiscal_year_end":"12-31"}`)
	oid := itoa(org)
	expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+oid+`,"role":"supplier","name":"Bob Pte Ltd","email":"bob@example.com","account_number":"AC-9"}`, owner), 201)
	rec := do(t, h, "GET", "/api/v1/contacts/export?org_id="+oid, "", owner)
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("export failed: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "ContactName,Salutation,FirstName,LastName,EmailAddress") {
		t.Fatal(lines)
	}
	if !strings.Contains(lines[1], "Bob Pte Ltd") || !strings.Contains(lines[1], "bob@example.com") || !strings.Contains(lines[1], "AC-9") || !strings.Contains(lines[1], "supplier") {
		t.Fatal(lines[1])
	}
	// Same file imports back.
	expectStatus(t, do(t, h, "POST", "/api/v1/contacts/import?org_id="+oid, rec.Body.String(), owner), 200)
}

func TestContactImportPersonsAndAddresses(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "people-owner@example.com")
	org := orgFromList(t, a, owner, `{"name":"People co","fiscal_year_end":"12-31"}`)
	body := "ContactName,Salutation,Person1FirstName,Person1LastName,Person1Email,Person1WorkPhone,Person2FirstName,Person2Email," +
		"BillingLine1,BillingCity,BillingPostalCode,BillingCountry,DeliveryCity," +
		"FaxNumber,BankAccountName,BankAccountNumber,Particulars\n" +
		"Acme Ltd,Ms,Ann,Lee,ann@example.com,+65 6000 0001,Bob,bob@example.com," +
		"1 Raffles Quay,Singapore,048583,SG,Singapore," +
		"+65 6123 4567,Acme Ltd,123-456,INV\n"
	m := expectStatus(t, do(t, h, "POST", "/api/v1/contacts/import?org_id="+itoa(org), body, owner), 200)
	if m["created"].(float64) != 1 {
		t.Fatal(m)
	}
	list := expectStatus(t, do(t, h, "GET", "/api/v1/contacts?org_id="+itoa(org), "", owner), 200)["contacts"].([]any)
	c := list[0].(map[string]any)
	if c["fax"] != "+65 6123 4567" || c["bank_account_name"] != "Acme Ltd" || c["bank_particulars"] != "INV" {
		t.Fatal(c)
	}
	people := c["contact_persons"].([]any)
	if len(people) != 2 || people[0].(map[string]any)["first_name"] != "Ann" || people[1].(map[string]any)["email"] != "bob@example.com" {
		t.Fatal(people)
	}
	addr := c["billing_address"].(map[string]any)
	if addr["line1"] != "1 Raffles Quay" || addr["postal_code"] != "048583" || addr["country"] != "SG" {
		t.Fatal(addr)
	}
	// Export carries the same columns back, so the exported file re-imports cleanly.
	rec := do(t, h, "GET", "/api/v1/contacts/export?org_id="+itoa(org), "", owner)
	head := strings.SplitN(rec.Body.String(), "\n", 2)[0]
	for _, want := range []string{"BillingLine1", "DeliveryCity", "Person1FirstName", "Person2Email", "BankAccountName", "Particulars"} {
		if !strings.Contains(head, want) {
			t.Fatalf("export header missing %s: %s", want, head)
		}
	}
	m2 := expectStatus(t, do(t, h, "POST", "/api/v1/contacts/import?org_id="+itoa(org), rec.Body.String(), owner), 200)
	if m2["updated"].(float64) != 1 {
		t.Fatal(m2)
	}
	// A file that never mentions addresses or people must not wipe them.
	m3 := expectStatus(t, do(t, h, "POST", "/api/v1/contacts/import?org_id="+itoa(org), "ContactName,Notes\nAcme Ltd,hello\n", owner), 200)
	if m3["updated"].(float64) != 1 {
		t.Fatal(m3)
	}
	list2 := expectStatus(t, do(t, h, "GET", "/api/v1/contacts?org_id="+itoa(org), "", owner), 200)["contacts"].([]any)
	c2 := list2[0].(map[string]any)
	if len(c2["contact_persons"].([]any)) != 2 || c2["billing_address"].(map[string]any)["line1"] != "1 Raffles Quay" {
		t.Fatal(c2)
	}
	// ?delete=1 with an address column refreshes them anyway.
	expectStatus(t, do(t, h, "POST", "/api/v1/contacts/import?delete=1&org_id="+itoa(org), "ContactName,BillingCity\nAcme Ltd,Johor\n", owner), 200)
	list3 := expectStatus(t, do(t, h, "GET", "/api/v1/contacts?org_id="+itoa(org), "", owner), 200)["contacts"].([]any)
	c3 := list3[0].(map[string]any)
	addr3 := c3["billing_address"].(map[string]any)
	// BillingCity mentioned: replaced. BillingLine1 not mentioned: delete mode does not clear it (addresses move as blocks).
	if addr3["city"] != "Johor" || len(c3["contact_persons"].([]any)) != 2 {
		t.Fatal(c3)
	}
}

// TestContactExportNeutralisesFormulas covers R20: exported text that a spreadsheet would run as a
// formula is prefixed, while ordinary text and numbers are unchanged.
func TestContactExportNeutralisesFormulas(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "export-formula@example.com")
	org := orgFromList(t, a, owner, `{"name":"Formula co","fiscal_year_end":"12-31"}`)
	oid := itoa(org)
	expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+oid+`,"role":"supplier","name":"=1+1","notes":"@SUM(A1)"}`, owner), 201)
	rec := do(t, h, "GET", "/api/v1/contacts/export?org_id="+oid, "", owner)
	if rec.Code != 200 {
		t.Fatalf("export failed: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "'=1+1") || !strings.Contains(body, "'@SUM(A1)") {
		t.Fatalf("formula cells were not neutralised: %s", body)
	}
	if strings.Contains(body, ",=1+1,") {
		t.Fatalf("bare formula still exported: %s", body)
	}
	if got := csvSafeCell("-12.50"); got != "-12.50" {
		t.Fatalf("numeric cell changed: %q", got)
	}
}
