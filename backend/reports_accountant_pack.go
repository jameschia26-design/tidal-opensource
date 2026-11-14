package main

import "net/http"

// accountantPack: GET /api/v1/reports/accountant-pack?org_id&from&to&as_of
// One export an accountant can work from: the trial balance, the account mapping, the fixed-asset
// schedule and any manual adjustments touching the GST accounts (R29). It is a worksheet, not a filing.
func (a *app) accountantPack(w http.ResponseWriter, r *http.Request, u currentUser) {
	q := r.URL.Query()
	org, ok := parseOrgID(w, q.Get("org_id"))
	if !ok {
		return
	}
	from, to, ok := ledgerRange(w, r)
	if !ok {
		return
	}
	asOf := q.Get("as_of")
	if asOf == "" {
		asOf = to
	}
	if _, ok := parseDay(asOf); !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	ctx := r.Context()
	rows, err := a.balances(ctx, access.ID, nil, asOf)
	if err != nil {
		serverError(w, err)
		return
	}
	tbRows, debit, credit := trialRows(rows)

	mappings := []map[string]any{}
	mRows, err := a.db.Query(ctx, `SELECT code,name,type FROM accounts WHERE org_id=$1 ORDER BY code`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	for mRows.Next() {
		var code, name, typ string
		if err := mRows.Scan(&code, &name, &typ); err != nil {
			mRows.Close()
			serverError(w, err)
			return
		}
		mappings = append(mappings, map[string]any{"code": code, "name": name, "type": typ})
	}
	mRows.Close()

	assets := []map[string]any{}
	aRows, err := a.db.Query(ctx, `SELECT f.id,f.name,f.acquired_on::text,f.cost_cents,
		COALESCE((SELECT SUM(d.amount_cents) FROM asset_depreciation d WHERE d.org_id=f.org_id AND d.asset_id=f.id AND d.period_month<=$2::date),0)
		FROM fixed_assets f WHERE f.org_id=$1 ORDER BY f.acquired_on, f.id`, access.ID, asOf)
	if err != nil {
		serverError(w, err)
		return
	}
	for aRows.Next() {
		var id, cost, accumulated int64
		var name, acquired string
		if err := aRows.Scan(&id, &name, &acquired, &cost, &accumulated); err != nil {
			aRows.Close()
			serverError(w, err)
			return
		}
		assets = append(assets, map[string]any{"id": id, "name": name, "acquired_on": acquired,
			"cost_cents": cost, "accumulated_depreciation_cents": accumulated, "book_value_cents": cost - accumulated})
	}
	aRows.Close()

	adjustments := []map[string]any{}
	jRows, err := a.db.Query(ctx, `SELECT j.id,j.entry_date::text,j.memo,COALESCE(SUM(l.debit_cents-l.credit_cents),0)
		FROM journals j JOIN journal_lines l ON l.org_id=j.org_id AND l.journal_id=j.id
		JOIN accounts acc ON acc.org_id=l.org_id AND acc.id=l.account_id
		WHERE j.org_id=$1 AND acc.code IN ('2300','2310') AND j.entry_date BETWEEN $2::date AND $3::date
		  AND j.idempotency_key LIKE 'manual:%'
		GROUP BY j.id,j.entry_date,j.memo ORDER BY j.entry_date, j.id`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	for jRows.Next() {
		var id, net int64
		var date, memo string
		if err := jRows.Scan(&id, &date, &memo, &net); err != nil {
			jRows.Close()
			serverError(w, err)
			return
		}
		adjustments = append(adjustments, map[string]any{"journal_id": id, "entry_date": date, "memo": memo, "gst_net_cents": net})
	}
	jRows.Close()

	writeJSON(w, http.StatusOK, map[string]any{
		"worksheet_only": true, "filed": false,
		"period_start": from, "period_end": to, "as_of": asOf,
		"trial_balance":    map[string]any{"rows": tbRows, "total_debit_cents": debit, "total_credit_cents": credit, "balanced": debit == credit},
		"account_mappings": mappings,
		"fixed_assets":     assets,
		"tax_adjustments":  adjustments,
		"note":             "Worksheet for an accountant. Nothing here is filed with IRAS or ACRA.",
	})
}
