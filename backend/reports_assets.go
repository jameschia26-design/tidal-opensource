package main

import "net/http"

// fixedAssetReport reports registered assets and recorded depreciation.
// ponytail: disposal schedule includes recorded proceeds and gains/losses, not Xero's grouped account layout.
func (a *app) fixedAssetReport(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	rows, err := a.db.Query(r.Context(), `SELECT f.id,f.name,f.acquired_on::text,f.cost_cents,
 COALESCE((SELECT sum(d.amount_cents) FROM asset_depreciation d WHERE d.org_id=f.org_id AND d.asset_id=f.id AND d.period_month < $2),0),
 COALESCE((SELECT sum(d.amount_cents) FROM asset_depreciation d WHERE d.org_id=f.org_id AND d.asset_id=f.id AND d.period_month BETWEEN $2 AND $3),0)
 FROM fixed_assets f WHERE f.org_id=$1 AND f.acquired_on<=$3 ORDER BY f.name,f.id LIMIT 501`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		AcquiredOn   string `json:"acquired_on"`
		Cost         int64  `json:"cost_cents"`
		OpeningAccum int64  `json:"opening_accum_cents"`
		PeriodDep    int64  `json:"period_dep_cents"`
		ClosingAccum int64  `json:"closing_accum_cents"`
		OpeningBook  int64  `json:"opening_book_cents"`
		ClosingBook  int64  `json:"closing_book_cents"`
	}
	out := []row{}
	for rows.Next() {
		var v row
		var acquired string
		if err := rows.Scan(&v.ID, &v.Name, &acquired, &v.Cost, &v.OpeningAccum, &v.PeriodDep); err != nil {
			serverError(w, err)
			return
		}
		v.AcquiredOn = acquired
		v.ClosingAccum = v.OpeningAccum + v.PeriodDep
		// Preserve recorded early depreciation as a negative opening book value.
		if acquired >= from {
			v.OpeningBook = -v.OpeningAccum
		} else {
			v.OpeningBook = v.Cost - v.OpeningAccum
		}
		v.ClosingBook = v.Cost - v.ClosingAccum
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
