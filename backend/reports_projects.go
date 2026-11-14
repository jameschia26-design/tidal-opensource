package main

import "net/http"

// ponytail: time totals only; add project costs and revenue when transactions support project attribution.
func (a *app) projectTimeReport(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	if status != "active" && status != "inactive" && status != "all" {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT p.id,p.name,c.name,p.active,COALESCE(SUM(t.minutes),0)::bigint,COUNT(t.id),
			COALESCE(SUM(CASE WHEN t.billable THEN t.minutes ELSE 0 END),0)::bigint,
			COALESCE(SUM(CASE WHEN t.billable THEN t.minutes*t.rate_cents/60 ELSE 0 END),0)::bigint FROM projects p JOIN contacts c ON c.org_id=p.org_id AND c.id=p.contact_id LEFT JOIN time_entries t ON t.org_id=p.org_id AND t.project_id=p.id AND t.entry_date BETWEEN $2::date AND $3::date WHERE p.org_id=$1 AND ($4='all' OR p.active=($4='active')) GROUP BY p.id,p.name,c.name,p.active ORDER BY lower(p.name),p.id LIMIT 501`, access.ID, from, to, status)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID      int64  `json:"project_id"`
		Project string `json:"project"`
		Contact string `json:"contact"`
		Active  bool   `json:"active"`
		Minutes int64  `json:"minutes"`
		Entries int64  `json:"entries"`
		// Chargeable time is the billable subset of Minutes and its value at each entry's own rate,
		// so changing an item's default rate later cannot rewrite past chargeable hours.
		BillableMinutes int64 `json:"billable_minutes"`
		ChargeableCents int64 `json:"chargeable_value_cents"`
	}
	out := []row{}
	for rows.Next() {
		var v row
		if err := rows.Scan(&v.ID, &v.Project, &v.Contact, &v.Active, &v.Minutes, &v.Entries, &v.BillableMinutes, &v.ChargeableCents); err != nil {
			serverError(w, err)
			return
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
