package main

import "net/http"

import "time"

// fixedAssetReconciliation: GET /api/v1/reports/fixed-asset-reconciliation?org_id&from&to
// Compares the asset register with the ledger accounts the register posts to.
// The register has one cost account and one accumulated-depreciation account because it carries no per-asset
// account, so there is a single reconciliation row rather than one per cost account.
// ponytail: no disposals, revaluations or tax allowances; add them with the register fields.
func (a *app) fixedAssetReconciliation(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	ctx := r.Context()
	var openCost, closeCost int64
	if err := a.db.QueryRow(ctx, `SELECT
		COALESCE(SUM(CASE WHEN acquired_on < $2::date THEN cost_cents END),0),
		COALESCE(SUM(CASE WHEN disposed_on IS NULL OR disposed_on > $3::date THEN cost_cents END),0)
		FROM fixed_assets WHERE org_id=$1 AND acquired_on <= $3::date AND (disposed_on IS NULL OR disposed_on >= $2::date)`,
		access.ID, from, to).Scan(&openCost, &closeCost); err != nil {
		serverError(w, err)
		return
	}
	var openAcc, closeAcc int64
	if err := a.db.QueryRow(ctx, `SELECT
		COALESCE(SUM(CASE WHEN d.period_month < $2::date THEN d.amount_cents END),0),
		COALESCE(SUM(CASE WHEN f.disposed_on IS NULL OR f.disposed_on > $3::date THEN d.amount_cents END),0)
		FROM asset_depreciation d JOIN fixed_assets f ON f.org_id=d.org_id AND f.id=d.asset_id
		WHERE d.org_id=$1 AND d.period_month <= $3::date AND (f.disposed_on IS NULL OR f.disposed_on >= $2::date)`,
		access.ID, from, to).Scan(&openAcc, &closeAcc); err != nil {
		serverError(w, err)
		return
	}
	ledgerAt := func(code string, before string) (int64, error) {
		var v int64
		err := a.db.QueryRow(ctx, `SELECT COALESCE(SUM(jl.debit_cents-jl.credit_cents),0) FROM journal_lines jl
			JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id
			JOIN accounts ac ON ac.org_id=jl.org_id AND ac.id=jl.account_id
			WHERE jl.org_id=$1 AND ac.code=$2 AND j.entry_date < $3::date`, access.ID, code, before).Scan(&v)
		return v, err
	}
	// The register has no recorded cost account, so the ledger side uses the standard asset account 1500.
	const acctCost = "1500"
	openLedgerCost, err := ledgerAt(acctCost, from)
	if err != nil {
		serverError(w, err)
		return
	}
	closeLedgerCost, err := ledgerAt(acctCost, day(mustDay(to).AddDate(0, 0, 1)))
	if err != nil {
		serverError(w, err)
		return
	}
	openLedgerAcc, err := ledgerAt(acctAccDep, from)
	if err != nil {
		serverError(w, err)
		return
	}
	closeLedgerAcc, err := ledgerAt(acctAccDep, day(mustDay(to).AddDate(0, 0, 1)))
	if err != nil {
		serverError(w, err)
		return
	}
	// Accumulated depreciation sits on the credit side, so a negative ledger balance is normal.
	openLedgerAcc, closeLedgerAcc = -openLedgerAcc, -closeLedgerAcc
	assetRows, err := a.db.Query(ctx, `SELECT f.id, f.name, f.acquired_on::text, f.cost_cents,
		COALESCE((SELECT SUM(d.amount_cents) FROM asset_depreciation d WHERE d.org_id=f.org_id AND d.asset_id=f.id AND d.period_month < $2::date),0),
		COALESCE((SELECT SUM(d.amount_cents) FROM asset_depreciation d WHERE d.org_id=f.org_id AND d.asset_id=f.id AND d.period_month <= $3::date),0),
		COALESCE(f.disposed_on::text,'')
		FROM fixed_assets f WHERE f.org_id=$1 AND f.acquired_on <= $3::date AND (f.disposed_on IS NULL OR f.disposed_on >= $2::date)
		ORDER BY f.name, f.id`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer assetRows.Close()
	assets := []map[string]any{}
	for assetRows.Next() {
		var id, cost, openDep, closeDep int64
		var name, acquired, disposed string
		if err := assetRows.Scan(&id, &name, &acquired, &cost, &openDep, &closeDep, &disposed); err != nil {
			serverError(w, err)
			return
		}
		assets = append(assets, map[string]any{
			"id": id, "name": name, "acquired_on": acquired, "disposed_on": disposed,
			"cost_cents": cost, "opening_accum_cents": openDep, "closing_accum_cents": closeDep,
			"opening_nbv_cents": cost - openDep, "closing_nbv_cents": cost - closeDep,
		})
	}
	if err := assetRows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"assets":       assets,
		"cost_account": acctCost, "accum_account": acctAccDep,
		"register": map[string]int64{"opening_cost_cents": openCost, "closing_cost_cents": closeCost,
			"opening_accum_cents": openAcc, "closing_accum_cents": closeAcc},
		"ledger": map[string]int64{"opening_cost_cents": openLedgerCost, "closing_cost_cents": closeLedgerCost,
			"opening_accum_cents": openLedgerAcc, "closing_accum_cents": closeLedgerAcc},
		"difference": map[string]int64{
			"opening_cost_cents": openLedgerCost - openCost, "closing_cost_cents": closeLedgerCost - closeCost,
			"opening_accum_cents": openLedgerAcc - openAcc, "closing_accum_cents": closeLedgerAcc - closeAcc},
	})
}

func mustDay(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }
