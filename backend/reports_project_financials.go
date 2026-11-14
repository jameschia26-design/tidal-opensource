package main

import "net/http"

// projectFinancials: GET /api/v1/reports/project-financials?org_id&from&to[&status=active|inactive|all]
// Per project: logged time, labour cost from staff cost rates, and expenses linked to the project.
// revenue from posted invoice lines that carry the same project. Gross profit is revenue minus
// labour and expenses.
// ponytail: no budgets; add when project budgets exist.
func (a *app) projectFinancials(w http.ResponseWriter, r *http.Request, u currentUser) {
	q := r.URL.Query()
	org, ok := parseOrgID(w, q.Get("org_id"))
	if !ok {
		return
	}
	from, to, ok := ledgerRange(w, r)
	if !ok {
		return
	}
	status := q.Get("status")
	if status == "" {
		status = "all"
	}
	if status != "active" && status != "inactive" && status != "all" {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT p.id, p.name, c.name, p.active, p.estimated_cost_cents, p.estimated_charge_cents,
		COALESCE((SELECT SUM(t.minutes) FROM time_entries t WHERE t.org_id=p.org_id AND t.project_id=p.id AND t.entry_date BETWEEN $2::date AND $3::date),0)::bigint,
		COALESCE((SELECT COUNT(*) FROM time_entries t WHERE t.org_id=p.org_id AND t.project_id=p.id AND t.entry_date BETWEEN $2::date AND $3::date),0)::bigint,
		COALESCE((SELECT SUM((t.minutes * COALESCE(r.hourly_cents,0)) / 60) FROM time_entries t
			LEFT JOIN staff_cost_rates r ON r.org_id=t.org_id AND r.user_id=t.created_by
			WHERE t.org_id=p.org_id AND t.project_id=p.id AND t.entry_date BETWEEN $2::date AND $3::date),0)::bigint,
		COALESCE((SELECT SUM(e.amount_cents) FROM expenses e WHERE e.org_id=p.org_id AND e.project_id=p.id AND e.customer_id=p.contact_id AND e.expense_date BETWEEN $2::date AND $3::date),0)::bigint,
		COALESCE((SELECT SUM(e.amount_cents) FROM expenses e WHERE e.org_id=p.org_id AND e.project_id=p.id AND e.customer_id=p.contact_id AND e.billable AND e.expense_date BETWEEN $2::date AND $3::date),0)::bigint,
		COALESCE((SELECT SUM(l.amount_cents) FROM invoice_lines l JOIN invoices i ON i.org_id=l.org_id AND i.id=l.invoice_id
			WHERE l.org_id=p.org_id AND l.project_id=p.id AND i.status='posted' AND i.invoice_date BETWEEN $2::date AND $3::date),0)::bigint
		FROM projects p JOIN contacts c ON c.org_id=p.org_id AND c.id=p.contact_id
		WHERE p.org_id=$1 AND ($4='all' OR p.active=($4='active'))
		ORDER BY lower(p.name), p.id LIMIT 501`, access.ID, from, to, status)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID              int64  `json:"project_id"`
		Project         string `json:"project"`
		Contact         string `json:"contact"`
		Active          bool   `json:"active"`
		Minutes         int64  `json:"minutes"`
		Entries         int64  `json:"entries"`
		LabourCents     int64  `json:"labour_cents"`
		ExpenseCents    int64  `json:"expense_cents"`
		BillableCents   int64  `json:"billable_expense_cents"`
		EstimatedCost   int64  `json:"estimated_cost_cents"`
		EstimatedCharge int64  `json:"estimated_charge_cents"`
		RevenueCents    int64  `json:"revenue_cents"`
		ProfitCents     int64  `json:"gross_profit_cents"`
		ProfitBP        int64  `json:"profit_bp"`
	}
	out := []row{}
	for rows.Next() {
		var v row
		if err := rows.Scan(&v.ID, &v.Project, &v.Contact, &v.Active, &v.EstimatedCost, &v.EstimatedCharge, &v.Minutes, &v.Entries, &v.LabourCents, &v.ExpenseCents, &v.BillableCents, &v.RevenueCents); err != nil {
			serverError(w, err)
			return
		}
		v.ProfitCents = v.RevenueCents - v.LabourCents - v.ExpenseCents
		if v.RevenueCents != 0 {
			v.ProfitBP = v.ProfitCents * 10000 / v.RevenueCents
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	truncated := len(out) > 500
	if truncated {
		out = out[:500]
	}
	writeJSON(w, 200, map[string]any{"rows": out, "truncated": truncated})
}
