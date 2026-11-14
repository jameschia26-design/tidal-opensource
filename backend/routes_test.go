package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIncomeWorksheetsWithoutDatabase(t *testing.T) {
	a := &app{}
	for _, tc := range []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request, currentUser)
		body    string
		want    int
	}{
		{"company", a.citWorksheet, `{"ya":2026,"revenue":10000000,"netProfitBeforeTax":5000000,"onlyIncomeAt17":true}`, 200},
		{"personal", a.pitWorksheet, `{"ya":2026,"age":40,"resident":true,"revenue":10000000,"grossProfit":5000000}`, 200},
		{"company loss", a.citWorksheet, `{"ya":2026,"netProfitBeforeTax":-100000,"separateSourceIncome":[{"label":"Interest","amount":1000,"reason":"Separate source","ref":"A1"}],"nonTaxableIncome":[{"label":"Exempt","amount":500}],"nonDeductible":[{"label":"Private cost","amount":100}]}`, 200},
		{"personal reliefs", a.pitWorksheet, `{"ya":2026,"age":40,"resident":true,"grossProfit":5000000,"reliefs":[{"label":"srs","amount":100000,"reason":"Contribution","ref":"S1"}]}`, 200},
		{"invalid item text", a.citWorksheet, `{"ya":2026,"nonDeductible":[{"label":"bad\u0000text","amount":100}]}`, 422},
		{"invalid review text", a.pitWorksheet, `{"ya":2026,"capitalAllowanceIssues":["bad\u0000text"]}`, 422},
		{"company extreme", a.citWorksheet, `{"ya":2026,"netProfitBeforeTax":9223372036854775807}`, 422},
		{"personal extreme", a.pitWorksheet, `{"ya":2026,"grossProfit":-9223372036854775808}`, 422},
		{"unsupported year", a.citWorksheet, `{"ya":2100}`, 422},
		{"unknown field", a.citWorksheet, `{"ya":2026,"net_profit_before_tax":100}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			tc.handler(r, req, currentUser{})
			if r.Code != tc.want {
				t.Fatalf("status = %d, want %d, body %s", r.Code, tc.want, r.Body.String())
			}
			if tc.want == 200 && !strings.Contains(r.Body.String(), `"worksheet_only":true`) {
				t.Fatal("worksheet disclaimer missing")
			}
		})
	}
}

// Router and boundary checks run without a database or deployment credentials.
func TestRoutesRejectUntrustedWritesBeforeDatabase(t *testing.T) {
	a := &app{cfg: config{PublicOrigin: "http://app.test"}}
	paths := []string{
		"/api/v1/auth/signup", "/api/v1/auth/signup/verify", "/api/v1/auth/login", "/api/v1/auth/login/verify",
		"/api/v1/auth/password/forgot", "/api/v1/auth/password/reset",
		"/api/v1/organizations", "/api/v1/contacts", "/api/v1/invoices",
		"/api/v1/invoices/1/post", "/api/v1/invoices/1/payments",
		"/api/v1/leads", "/api/v1/deals", "/api/v1/tax/cit",
		"/api/v1/admin/invitations",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			r := httptest.NewRecorder()
			a.routes().ServeHTTP(r, httptest.NewRequest(http.MethodPost, path, nil))
			if r.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", r.Code)
			}
		})
	}
}

func TestCoreRoutesRequireSignIn(t *testing.T) {
	a := &app{cfg: config{PublicOrigin: "http://app.test"}}
	for _, path := range []string{
		"/api/v1/invoices", "/api/v1/invoices/1", "/api/v1/leads", "/api/v1/deals",
		"/api/v1/reports/trial-balance", "/api/v1/reports/pnl",
		"/api/v1/reports/balance-sheet", "/api/v1/tax/years", "/api/v1/admin/users",
	} {
		t.Run(path, func(t *testing.T) {
			r := do(t, a.routes(), http.MethodGet, path, "", "")
			if r.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", r.Code)
			}
			if r.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private response permits caching")
			}
		})
	}
}
