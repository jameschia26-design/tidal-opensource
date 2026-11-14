package main

import "testing"

func TestTimeEntries(t *testing.T) {
	a, _ := testApp(t)
	h := a.routes()
	owner := seedSession(t, a, "time-owner@example.com")
	outsider := seedSession(t, a, "time-outsider@example.com")
	org := orgFromList(t, a, owner, `{"name":"Time co","fiscal_year_end":"12-31"}`)
	o := itoa(org)
	contact := expectStatus(t, do(t, h, "POST", "/api/v1/contacts", `{"org_id":`+o+`,"role":"customer","name":"Client"}`, owner), 201)
	project := expectStatus(t, do(t, h, "POST", "/api/v1/projects", `{"org_id":`+o+`,"contact_id":`+itoa(int64(contact["id"].(float64)))+`,"name":"Project"}`, owner), 201)
	p := itoa(int64(project["id"].(float64)))
	body := `{"org_id":` + o + `,"project_id":` + p + `,"date":"2026-10-30","task":"Design","description":"Work","minutes":30}`
	expectStatus(t, do(t, h, "POST", "/api/v1/time-entries", body, outsider), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/time-entries", body, owner), 201)
	result := expectStatus(t, do(t, h, "GET", "/api/v1/time-entries?org_id="+o, "", owner), 200)
	rows := result["entries"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["minutes"] != float64(30) || rows[0].(map[string]any)["staff"] == "" || rows[0].(map[string]any)["recorder_id"].(float64) <= 0 {
		t.Fatal(result)
	}
	filtered := expectStatus(t, do(t, h, "GET", "/api/v1/time-entries?org_id="+o+"&from=2026-10-01&to=2026-10-29", "", owner), 200)
	if len(filtered["entries"].([]any)) != 0 || filtered["truncated"] != false {
		t.Fatal(filtered)
	}
	filtered = expectStatus(t, do(t, h, "GET", "/api/v1/time-entries?org_id="+o+"&from=2026-10-30&to=2026-10-30", "", owner), 200)
	if len(filtered["entries"].([]any)) != 1 {
		t.Fatal(filtered)
	}
	expectStatus(t, do(t, h, "GET", "/api/v1/time-entries?org_id="+o+"&from=2026-10-30", "", owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/time-entries", `{"org_id":`+o+`,"project_id":`+p+`,"date":"bad","task":"Design","minutes":30}`, owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/time-entries", `{"org_id":`+o+`,"project_id":`+p+`,"date":"2026-10-30","task":"Design","minutes":0}`, owner), 400)
	expectStatus(t, do(t, h, "POST", "/api/v1/time-entries", `{"org_id":`+o+`,"project_id":999999,"date":"2026-10-30","task":"Design","minutes":1}`, owner), 422)
	expectStatus(t, do(t, h, "GET", "/api/v1/time-entries?org_id="+o, "", outsider), 403)
	expectStatus(t, do(t, h, "POST", "/api/v1/projects/"+p+"/active", `{"org_id":`+o+`,"active":false}`, owner), 200)
	expectStatus(t, do(t, h, "POST", "/api/v1/time-entries", body, owner), 422)
	result = expectStatus(t, do(t, h, "GET", "/api/v1/time-entries?org_id="+o, "", owner), 200)
	if len(result["entries"].([]any)) != 1 {
		t.Fatal("deactivating project must preserve recorded time", result)
	}
	report := "/api/v1/reports/project-time?org_id=" + o + "&from=2026-10-01&to=2026-10-31&status="
	for status, want := range map[string]int{"all": 1, "inactive": 1, "active": 0} {
		rows := expectStatus(t, do(t, h, "GET", report+status, "", owner), 200)["rows"].([]any)
		if len(rows) != want {
			t.Fatal(status, rows)
		}
		if want == 1 && (rows[0].(map[string]any)["minutes"] != float64(30) || rows[0].(map[string]any)["entries"] != float64(1)) {
			t.Fatal(rows)
		}
	}
	if rows := expectStatus(t, do(t, h, "GET", "/api/v1/reports/project-time?org_id="+o+"&from=2026-09-01&to=2026-09-30&status=all", "", owner), 200)["rows"].([]any); len(rows) != 1 || rows[0].(map[string]any)["minutes"] != float64(0) {
		t.Fatal(rows)
	}
	expectStatus(t, do(t, h, "GET", report+"all", "", outsider), 403)
	expectStatus(t, do(t, h, "GET", report+"bad", "", owner), 400)
}
