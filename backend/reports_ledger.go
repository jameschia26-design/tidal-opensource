package main

import (
	"net/http"
	"time"
)

// Ledger reports read posted journal lines only. They back Xero's Account Transactions, General Ledger Detail,
// Journal Report, General Ledger Summary, Trial Balance by Date Range and Account Summary screens.
// ponytail: capped at 5,000 lines per request, no paging or column chooser; add paging when a ledger outgrows it.

const ledgerLineCap = 5000

// reportToday is today's date in Singapore, matching the date the frontend sends.
func reportToday() string {
	return time.Now().In(time.FixedZone("SGT", 8*60*60)).Format("2006-01-02")
}

// reportEnd caps a "to date" report at today so future-dated postings never leak into current
// figures. A caller that really wants a future period asks with include_future=1.
func reportEnd(r *http.Request, to string) string {
	if r.URL.Query().Get("include_future") == "1" {
		return to
	}
	if today := reportToday(); to > today {
		return today
	}
	return to
}

func ledgerRange(w http.ResponseWriter, r *http.Request) (from, to string, ok bool) {
	f, ok1 := parseDay(r.URL.Query().Get("from"))
	t, ok2 := parseDay(r.URL.Query().Get("to"))
	if !ok1 || !ok2 || t.Before(f) || t.Sub(f) > 366*24*time.Hour {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return "", "", false
	}
	return f.Format("2006-01-02"), t.Format("2006-01-02"), true
}

// ledgerLines: GET /api/v1/reports/ledger-lines?org_id&from&to[&account_id]
func (a *app) ledgerLines(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	from, to, ok := ledgerRange(w, r)
	if !ok {
		return
	}
	var accountID *int64
	if s := r.URL.Query().Get("account_id"); s != "" {
		id, ok := parseOrgID(w, s)
		if !ok {
			return
		}
		accountID = &id
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT j.id, j.entry_date::text, j.memo, a.id, a.code, a.name, jl.line_no, jl.description, jl.debit_cents, jl.credit_cents
		FROM journal_lines jl JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
		JOIN accounts a ON a.org_id=jl.org_id AND a.id=jl.account_id
		WHERE jl.org_id=$1 AND j.entry_date BETWEEN $2::date AND $3::date AND ($4::bigint IS NULL OR a.id=$4)
		ORDER BY j.entry_date, j.id, jl.line_no LIMIT $5`, access.ID, from, to, accountID, ledgerLineCap+1)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type line struct {
		JournalID   int64  `json:"journal_id"`
		Date        string `json:"date"`
		Memo        string `json:"memo"`
		AccountID   int64  `json:"account_id"`
		Code        string `json:"code"`
		Account     string `json:"account"`
		LineNo      int    `json:"line_no"`
		Description string `json:"description"`
		Debit       int64  `json:"debit_cents"`
		Credit      int64  `json:"credit_cents"`
	}
	out := []line{}
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.JournalID, &l.Date, &l.Memo, &l.AccountID, &l.Code, &l.Account, &l.LineNo, &l.Description, &l.Debit, &l.Credit); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	truncated := len(out) > ledgerLineCap
	if truncated {
		out = out[:ledgerLineCap]
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": out, "truncated": truncated})
}

// ledgerSummary: GET /api/v1/reports/ledger-summary?org_id&from&to[&account_id] — opening, movement and closing per account.
func (a *app) ledgerSummary(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	from, to, ok := ledgerRange(w, r)
	if !ok {
		return
	}
	var accountID *int64
	if s := r.URL.Query().Get("account_id"); s != "" {
		id, ok := parseOrgID(w, s)
		if !ok {
			return
		}
		accountID = &id
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT a.id, a.code, a.name, a.type,
		COALESCE(SUM(CASE WHEN j.entry_date < $2::date THEN jl.debit_cents - jl.credit_cents END),0),
		COALESCE(SUM(CASE WHEN j.entry_date BETWEEN $2::date AND $3::date THEN jl.debit_cents END),0),
		COALESCE(SUM(CASE WHEN j.entry_date BETWEEN $2::date AND $3::date THEN jl.credit_cents END),0)
		FROM accounts a
		LEFT JOIN journal_lines jl ON jl.org_id=a.org_id AND jl.account_id=a.id
		LEFT JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id AND j.entry_date <= $3::date
		WHERE a.org_id=$1 AND ($4::bigint IS NULL OR a.id=$4) GROUP BY a.id, a.code, a.name, a.type ORDER BY a.code`, access.ID, from, to, accountID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		AccountID int64  `json:"account_id"`
		Code      string `json:"code"`
		Name      string `json:"name"`
		Type      string `json:"type"`
		Opening   int64  `json:"opening_cents"` // debit positive, credit negative
		Debit     int64  `json:"debit_cents"`
		Credit    int64  `json:"credit_cents"`
		Net       int64  `json:"net_cents"` // debit minus credit for the period
		Closing   int64  `json:"closing_cents"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.AccountID, &x.Code, &x.Name, &x.Type, &x.Opening, &x.Debit, &x.Credit); err != nil {
			serverError(w, err)
			return
		}
		if x.Opening == 0 && x.Debit == 0 && x.Credit == 0 {
			continue
		}
		x.Net = x.Debit - x.Credit
		x.Closing = x.Opening + x.Net
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": out})
}
