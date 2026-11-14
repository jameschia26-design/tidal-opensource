package main

import "net/http"

// glExceptions: GET /api/v1/reports/gl-exceptions?org_id&from&to — ledger entries a reviewer should look at:
// manual journals posted into a closed period, and any journal that touches a control account (AR/AP).
// ponytail: three fixed rules, no account grouping, columns or filter builder; add rules as they are needed.
func (a *app) glExceptions(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	from, to, ok := ledgerRange(w, r)
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	var closed *string
	var closedThrough string
	if err := a.db.QueryRow(r.Context(), `SELECT closed_through::text FROM period_closes WHERE org_id=$1`, access.ID).Scan(&closedThrough); err == nil {
		closed = &closedThrough
	}
	rows, err := a.db.Query(r.Context(), `SELECT j.id, j.entry_date::text, j.memo, ac.code, ac.name, jl.debit_cents, jl.credit_cents,
		CASE WHEN $4::date IS NOT NULL AND j.entry_date <= $4::date THEN true ELSE false END AS in_closed
		FROM journal_lines jl JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
		JOIN accounts ac ON ac.org_id=jl.org_id AND ac.id=jl.account_id
		WHERE jl.org_id=$1 AND j.entry_date BETWEEN $2::date AND $3::date
		  AND (ac.code IN ($5,$6) OR ($4::date IS NOT NULL AND j.entry_date <= $4::date))
		ORDER BY j.entry_date, j.id, jl.line_no LIMIT 5001`, access.ID, from, to, closed, acctAR, acctAP)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		Journal int64  `json:"journal_id"`
		Date    string `json:"date"`
		Memo    string `json:"memo"`
		Code    string `json:"account_code"`
		Account string `json:"account"`
		Debit   int64  `json:"debit_cents"`
		Credit  int64  `json:"credit_cents"`
		Rule    string `json:"rule"`
	}
	out := []row{}
	for rows.Next() {
		var v row
		var inClosed bool
		if err := rows.Scan(&v.Journal, &v.Date, &v.Memo, &v.Code, &v.Account, &v.Debit, &v.Credit, &inClosed); err != nil {
			serverError(w, err)
			return
		}
		switch {
		case v.Code == acctAR || v.Code == acctAP:
			v.Rule = "control_account"
		default:
			v.Rule = "closed_period"
		}
		if inClosed && v.Rule == "control_account" {
			v.Rule = "control_account_in_closed_period"
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	truncated := len(out) > 5000
	if truncated {
		out = out[:5000]
	}
	// Reconciliation checks beyond the line list: a journal must balance, have at least two lines,
	// document numbers must be unique, and each control account must equal its subledger.
	type check struct {
		Key   string `json:"key"`
		Label string `json:"label"`
		Count int64  `json:"count"`
		Note  string `json:"note"`
	}
	checks := []check{}
	var n int64
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FROM (
		SELECT j.id FROM journals j JOIN journal_lines jl ON jl.org_id=j.org_id AND jl.journal_id=j.id
		WHERE j.org_id=$1 AND j.entry_date BETWEEN $2::date AND $3::date
		GROUP BY j.id HAVING SUM(jl.debit_cents)<>SUM(jl.credit_cents)) x`, access.ID, from, to).Scan(&n); err != nil {
		serverError(w, err)
		return
	}
	checks = append(checks, check{"unbalanced_journal", "Journal does not balance", n, "Debits and credits differ."})
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FROM (
		SELECT j.id FROM journals j JOIN journal_lines jl ON jl.org_id=j.org_id AND jl.journal_id=j.id
		WHERE j.org_id=$1 AND j.entry_date BETWEEN $2::date AND $3::date
		GROUP BY j.id HAVING count(*)<2) x`, access.ID, from, to).Scan(&n); err != nil {
		serverError(w, err)
		return
	}
	checks = append(checks, check{"one_line_journal", "Journal has fewer than two lines", n, "A one-line journal cannot post both sides."})
	if err := a.db.QueryRow(r.Context(), `SELECT count(*) FROM (
		SELECT document_number FROM (
			SELECT number::text AS document_number FROM invoices WHERE org_id=$1 AND invoice_date BETWEEN $2::date AND $3::date AND number IS NOT NULL
			UNION ALL SELECT document_number FROM bills WHERE org_id=$1 AND bill_date BETWEEN $2::date AND $3::date
		) d GROUP BY document_number HAVING count(*)>1) x`, access.ID, from, to).Scan(&n); err != nil {
		serverError(w, err)
		return
	}
	checks = append(checks, check{"duplicate_document_number", "Document number used more than once", n, "Each document number should be unique."})
	controlDelta := func(code, subledger string) (int64, error) {
		var control, sub int64
		if err := a.db.QueryRow(r.Context(), `SELECT COALESCE(SUM(jl.debit_cents-jl.credit_cents),0) FROM journal_lines jl
			JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
			JOIN accounts ac ON ac.org_id=jl.org_id AND ac.id=jl.account_id
			WHERE jl.org_id=$1 AND ac.code=$2 AND j.entry_date<=$3::date`, access.ID, code, to).Scan(&control); err != nil {
			return 0, err
		}
		if err := a.db.QueryRow(r.Context(), subledger, access.ID, to).Scan(&sub); err != nil {
			return 0, err
		}
		return control - sub, nil
	}
	arDelta, err := controlDelta(acctAR, `SELECT COALESCE((SELECT SUM(i.total_cents) FROM invoices i WHERE i.org_id=$1 AND i.status='posted' AND i.invoice_date<=$2::date),0)
		- COALESCE((SELECT SUM(p.amount_cents) FROM payments p WHERE p.org_id=$1 AND p.payment_date<=$2::date),0)
		- COALESCE((SELECT SUM(n.amount_cents) FROM credit_notes n WHERE n.org_id=$1 AND n.credit_date<=$2::date),0)`)
	if err != nil {
		serverError(w, err)
		return
	}
	checks = append(checks, check{"receivable_control", "Receivable control differs from the invoice subledger", abs64(arDelta), "Control account minus posted invoices, payments and credits."})
	apDelta, err := controlDelta(acctAP, `SELECT COALESCE((SELECT SUM(b.total_cents) FROM bills b WHERE b.org_id=$1 AND b.status='posted' AND b.bill_date<=$2::date),0)
		- COALESCE((SELECT SUM(p.amount_cents) FROM bill_payments p WHERE p.org_id=$1 AND p.payment_date<=$2::date),0)
		- COALESCE((SELECT SUM(sc.total_cents) FROM supplier_credits sc WHERE sc.org_id=$1 AND sc.credit_date<=$2::date),0)`)
	if err != nil {
		serverError(w, err)
		return
	}
	checks = append(checks, check{"payable_control", "Payable control differs from the bill subledger", abs64(apDelta), "Control account minus posted bills, payments and credits."})
	writeJSON(w, http.StatusOK, map[string]any{"rows": out, "checks": checks, "closed_through": closedThrough, "truncated": truncated})
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
