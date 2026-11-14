package main

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"errors"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Fixed assets: straight-line depreciation, one monthly journal per asset.
// Dr Depreciation expense (6500) / Cr Accumulated depreciation (1590). Purchase posting is done separately via a manual journal.

const (
	acctDepExp = "6500"
	acctAccDep = "1590"
)

type assetInput struct {
	OrgID            int64  `json:"org_id"`
	Name             string `json:"name"`
	AcquiredOn       string `json:"acquired_on"`
	CostCents        int64  `json:"cost_cents"`
	UsefulLifeMonths int    `json:"useful_life_months"`
	Method           string `json:"depreciation_method"`
	AveragingRule    string `json:"averaging_rule"`
	ResidualValue    int64  `json:"residual_value_cents"`
}

func (a *app) createAsset(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in assetInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Method == "" {
		in.Method = "straight_line"
	}
	if in.AveragingRule == "" {
		in.AveragingRule = "full_month"
	}
	acq, ok := parseDay(in.AcquiredOn)
	if !ok || in.Name == "" || !validTaxText(in.Name, 200) || in.CostCents < 1 || in.CostCents > maxMoneyCents || in.UsefulLifeMonths < 1 || in.UsefulLifeMonths > 600 || in.ResidualValue < 0 || in.ResidualValue > in.CostCents || (in.Method != "straight_line" && in.Method != "diminishing_value" && in.Method != "manual") || (in.AveragingRule != "full_month" && in.AveragingRule != "half_month") {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	var id int64
	if err := a.db.QueryRow(r.Context(), `INSERT INTO fixed_assets (org_id,name,acquired_on,cost_cents,useful_life_months,created_by,depreciation_method,averaging_rule,residual_value_cents) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		access.ID, in.Name, acq, in.CostCents, in.UsefulLifeMonths, u.ID, in.Method, in.AveragingRule, in.ResidualValue).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (a *app) listAssets(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT f.id,f.name,f.acquired_on::text,f.cost_cents,f.useful_life_months,f.depreciated_months,f.depreciation_method,f.averaging_rule,f.residual_value_cents,(SELECT to_char(max(d.period_month),'YYYY-MM') FROM asset_depreciation d WHERE d.org_id=f.org_id AND d.asset_id=f.id) FROM fixed_assets f WHERE f.org_id=$1 ORDER BY f.id DESC LIMIT 200`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type asset struct {
		ID                int64  `json:"id"`
		Name              string `json:"name"`
		AcquiredOn        string `json:"acquired_on"`
		CostCents         int64  `json:"cost_cents"`
		UsefulLifeMonths  int    `json:"useful_life_months"`
		DepreciatedMonths int    `json:"depreciated_months"`
		NetBookCents      int64  `json:"net_book_cents"`
		Method            string `json:"depreciation_method"`
		AveragingRule     string `json:"averaging_rule"`
		ResidualValue     int64  `json:"residual_value_cents"`
		LastDepreciation  string `json:"last_depreciation"`
	}
	out := []asset{}
	for rows.Next() {
		var s asset
		if err := rows.Scan(&s.ID, &s.Name, &s.AcquiredOn, &s.CostCents, &s.UsefulLifeMonths, &s.DepreciatedMonths, &s.Method, &s.AveragingRule, &s.ResidualValue, &s.LastDepreciation); err != nil {
			serverError(w, err)
			return
		}
		s.NetBookCents = s.CostCents - depreciationTo(s.CostCents, s.UsefulLifeMonths, s.DepreciatedMonths)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"assets": out})
}

// monthlyCharge is cost / life, with the rounding remainder added to the final month so totals equal cost exactly.
func monthlyCharge(cost int64, life int, monthIndex int) int64 {
	base := cost / int64(life)
	if monthIndex == life-1 {
		return cost - base*int64(life-1)
	}
	return base
}

func depreciationTo(cost int64, life int, months int) int64 {
	var total int64
	for i := 0; i < months; i++ {
		total += monthlyCharge(cost, life, i)
	}
	return total
}

// runDepreciation posts one month of straight-line depreciation for every asset that has months left.
// The asset row is locked and the monthly key is unique, so a repeat run for the same month posts nothing new.
func (a *app) runDepreciation(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID  int64  `json:"org_id"`
		Month  string `json:"month"`
		Manual []struct {
			AssetID     int64 `json:"asset_id"`
			AmountCents int64 `json:"amount_cents"`
		} `json:"manual_amounts"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	m, ok := parseDay(in.Month + "-01")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO accounts (org_id,code,name,type,class) VALUES ($1,$2,'Depreciation expense','expense',$3) ON CONFLICT (org_id,code) DO NOTHING`, access.ID, acctDepExp, defaultAccountClass("expense", acctDepExp)); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO accounts (org_id,code,name,type,class) VALUES ($1,$2,'Accumulated depreciation','asset',$3) ON CONFLICT (org_id,code) DO NOTHING`, access.ID, acctAccDep, defaultAccountClass("asset", acctAccDep)); err != nil {
		serverError(w, err)
		return
	}
	var depExp, accDep int64
	if err := tx.QueryRow(ctx, `SELECT id FROM accounts WHERE org_id=$1 AND code=$2`, access.ID, acctDepExp).Scan(&depExp); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM accounts WHERE org_id=$1 AND code=$2`, access.ID, acctAccDep).Scan(&accDep); err != nil {
		serverError(w, err)
		return
	}
	rows, err := tx.Query(ctx, `SELECT f.id,f.name,f.acquired_on::text,f.cost_cents,f.useful_life_months,f.depreciated_months,f.depreciation_method,f.averaging_rule,f.residual_value_cents,COALESCE((SELECT sum(d.amount_cents) FROM asset_depreciation d WHERE d.org_id=f.org_id AND d.asset_id=f.id),0) FROM fixed_assets f WHERE f.org_id=$1 AND NOT f.low_value AND f.disposed_on IS NULL AND (f.depreciated_months < f.useful_life_months OR f.depreciation_method='manual') ORDER BY f.id FOR UPDATE`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	type pending struct {
		id, cost, life, done              int64
		name, acquired, method, averaging string
		residual, accumulated             int64
	}
	var list []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.name, &p.acquired, &p.cost, &p.life, &p.done, &p.method, &p.averaging, &p.residual, &p.accumulated); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		list = append(list, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	// A run that targets a closed month is refused outright; a catch-up that meets a closed earlier month
	// skips only that asset, so other assets still post.
	var closedThrough *time.Time
	if err := tx.QueryRow(ctx, `SELECT closed_through FROM period_closes WHERE org_id=$1`, access.ID).Scan(&closedThrough); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	if closedThrough != nil && !m.After(*closedThrough) {
		writeError(w, http.StatusUnprocessableEntity, "period_closed")
		return
	}
	posted := []int64{}
	skipped := []int64{}
	manualAmounts := map[int64]int64{}
	for _, line := range in.Manual {
		if line.AssetID <= 0 || line.AmountCents <= 0 || line.AmountCents > maxMoneyCents || manualAmounts[line.AssetID] != 0 {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		manualAmounts[line.AssetID] = line.AmountCents
	}
	for _, p := range list {
		acquired, okDay := parseDay(p.acquired)
		if !okDay {
			continue
		}
		// Straight-line schedule: month k is the k-th month after acquisition. Every missed month up to the
		// requested one posts in its own period (D8), so a late or out-of-order run reaches the same total.
		month := time.Date(acquired.Year(), acquired.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, int(p.done), 0)
		if p.method == "manual" {
			if !month.After(m) {
				amount := manualAmounts[p.id]
				if amount > 0 && amount <= p.cost-p.residual-p.accumulated {
					if err := postAssetDepreciation(ctx, tx, access.ID, u.ID, p.id, p.name, month, amount); err != nil {
						serverError(w, err)
						return
					}
					posted = append(posted, p.id)
					_, err = tx.Exec(ctx, `UPDATE fixed_assets SET depreciated_months=depreciated_months+1 WHERE org_id=$1 AND id=$2`, access.ID, p.id)
					if err != nil {
						serverError(w, err)
						return
					}
				}
			}
			continue
		}
		for !month.After(m) && p.done < p.life {
			// A month inside a closed period cannot change: skip this asset rather than abort the batch.
			if closedThrough != nil && !month.After(*closedThrough) {
				skipped = append(skipped, p.id)
				break
			}
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM asset_depreciation WHERE org_id=$1 AND asset_id=$2 AND period_month=$3)`, access.ID, p.id, month).Scan(&exists); err != nil {
				serverError(w, err)
				return
			}
			depreciable := p.cost - p.residual
			remaining := depreciable - p.accumulated
			if remaining <= 0 {
				break
			}
			amount := monthlyCharge(depreciable, int(p.life), int(p.done))
			if p.method == "diminishing_value" {
				// Diminishing value charges a rate on the opening carrying value, so each period's
				// charge falls as the asset is written down. Charging on original cost would instead
				// repeat a fixed amount, which is straight line under another name.
				opening := p.cost - p.accumulated
				life := int64(p.life)
				amount = (opening*2 + life/2) / life
				if amount > remaining {
					amount = remaining
				}
			}
			if p.averaging == "half_month" && p.done == 0 {
				amount /= 2
			}
			if amount > remaining {
				amount = remaining
			}
			if p.done == p.life-1 {
				amount = remaining
			}
			if !exists && amount > 0 {
				key := "depreciation:" + strconv.FormatInt(p.id, 10) + ":" + day(month)
				hash := sha256.Sum256([]byte(key))
				jid, err := postJournal(ctx, tx, access.ID, u.ID, month, "Depreciation: "+p.name+" "+day(month), key, hash, depExp, accDep, amount)
				if err != nil {
					serverError(w, err)
					return
				}
				if _, err := tx.Exec(ctx, `INSERT INTO asset_depreciation (org_id,asset_id,period_month,amount_cents,journal_id) VALUES ($1,$2,$3,$4,$5)`, access.ID, p.id, month, amount, jid); err != nil {
					serverError(w, err)
					return
				}
				posted = append(posted, p.id)
				p.accumulated += amount
			}
			// A month whose share rounds to zero is counted and skipped: no zero-value journal can post.
			if _, err := tx.Exec(ctx, `UPDATE fixed_assets SET depreciated_months=depreciated_months+1 WHERE org_id=$1 AND id=$2`, access.ID, p.id); err != nil {
				serverError(w, err)
				return
			}
			p.done++
			month = month.AddDate(0, 1, 0)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'asset.depreciation_run',jsonb_build_object('month',$3::text,'assets',$4::int))`, access.ID, u.ID, day(m), len(posted)); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"month": day(m), "assets_depreciated": posted, "skipped_closed_period": skipped})
}

func postAssetDepreciation(ctx context.Context, tx pgx.Tx, orgID, userID, assetID int64, name string, month time.Time, amount int64) error {
	depExp, err := ensureAccount(ctx, tx, orgID, acctDepExp, "Depreciation expense", "expense")
	if err != nil {
		return err
	}
	accDep, err := ensureAccount(ctx, tx, orgID, acctAccDep, "Accumulated depreciation", "asset")
	if err != nil {
		return err
	}
	key := "depreciation:" + strconv.FormatInt(assetID, 10) + ":" + day(month)
	hash := sha256.Sum256([]byte(key))
	jid, err := postJournal(ctx, tx, orgID, userID, month, "Depreciation: "+name+" "+day(month), key, hash, depExp, accDep, amount)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO asset_depreciation(org_id,asset_id,period_month,amount_cents,journal_id) VALUES($1,$2,$3,$4,$5)`, orgID, assetID, month, amount, jid)
	return err
}

// disposalInput records the sale or scrapping of a register asset.
type disposalInput struct {
	OrgID             int64  `json:"org_id"`
	DisposedOn        string `json:"disposed_on"`
	ProceedsCents     int64  `json:"proceeds_cents"`
	ProceedsAccountID int64  `json:"proceeds_account_id"`
}

// disposeAsset: POST /api/v1/assets/{id}/dispose — removes the asset from the register and posts the
// disposal: accumulated depreciation and the proceeds against cost, with any gain or loss, and depreciation
// stops because the asset is marked disposed.
func (a *app) disposalScheduleLegacy(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	var from, to any
	if v := r.URL.Query().Get("from"); v != "" {
		d, valid := parseDay(v)
		if !valid {
			writeError(w, 400, "invalid_input")
			return
		}
		from = d
	}
	if v := r.URL.Query().Get("to"); v != "" {
		d, valid := parseDay(v)
		if !valid {
			writeError(w, 400, "invalid_input")
			return
		}
		to = d
	}
	rows, err := a.db.Query(r.Context(), `SELECT f.id,f.name,f.acquired_on::text,f.disposed_on::text,f.cost_cents,COALESCE(f.proceeds_cents,0),COALESCE(f.gain_loss_cents,0),
		COALESCE(a.code||' '||a.name,''),COALESCE((SELECT sum(d.amount_cents) FROM asset_depreciation d WHERE d.org_id=f.org_id AND d.asset_id=f.id),0)
		FROM fixed_assets f LEFT JOIN accounts a ON a.org_id=f.org_id AND a.id=f.cost_account_id
		WHERE f.org_id=$1 AND f.disposed_on IS NOT NULL
		AND ($2::date IS NULL OR f.disposed_on >= $2::date) AND ($3::date IS NULL OR f.disposed_on <= $3::date)
		ORDER BY f.disposed_on DESC,f.id LIMIT 501`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type disposal struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		Acquired     string `json:"acquired_on"`
		Disposed     string `json:"disposed_on"`
		Cost         int64  `json:"cost_cents"`
		Proceeds     int64  `json:"proceeds_cents"`
		GainLoss     int64  `json:"gain_loss_cents"`
		CostAccount  string `json:"cost_account"`
		Depreciation int64  `json:"accumulated_depreciation_cents"`
	}
	out := []disposal{}
	for rows.Next() {
		var v disposal
		if err := rows.Scan(&v.ID, &v.Name, &v.Acquired, &v.Disposed, &v.Cost, &v.Proceeds, &v.GainLoss, &v.CostAccount, &v.Depreciation); err != nil {
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

func (a *app) disposeAsset(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in disposalInput
	if !decodeJSON(w, r, &in) {
		return
	}
	on, okDay := parseDay(in.DisposedOn)
	if !okDay || in.ProceedsCents < 0 || in.ProceedsCents > maxMoneyCents || in.ProceedsAccountID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	var name, acquired string
	var cost, life, done int64
	var costAccount *int64
	var disposedOn *time.Time
	err = tx.QueryRow(ctx, `SELECT name,acquired_on::text,cost_cents,useful_life_months,depreciated_months,cost_account_id,disposed_on
		FROM fixed_assets WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&name, &acquired, &cost, &life, &done, &costAccount, &disposedOn)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if disposedOn != nil {
		writeError(w, http.StatusConflict, "asset_already_disposed")
		return
	}
	acq, _ := parseDay(acquired)
	if on.Before(acq) {
		writeError(w, http.StatusUnprocessableEntity, "disposal_before_acquisition")
		return
	}
	var proceedsType string
	if err := tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, in.ProceedsAccountID).Scan(&proceedsType); err != nil || proceedsType != "asset" {
		writeError(w, http.StatusBadRequest, "invalid_proceeds_account")
		return
	}
	ids, err := accountIDs(ctx, tx, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	costAcct := ids[acctAssetCost]
	if costAccount != nil {
		costAcct = *costAccount
	}
	if costAcct == 0 {
		costAcct = inventoryAccountID(ctx, tx, access.ID)
	}
	gainAcct, err := ensureAccount(ctx, tx, access.ID, "4900", "Gain on disposal", "revenue")
	if err != nil {
		serverError(w, err)
		return
	}
	lossAcct, err := ensureAccount(ctx, tx, access.ID, "6900", "Loss on disposal", "expense")
	if err != nil {
		serverError(w, err)
		return
	}
	var accumulated int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount_cents),0) FROM asset_depreciation WHERE org_id=$1 AND asset_id=$2`, access.ID, id).Scan(&accumulated); err != nil {
		serverError(w, err)
		return
	}
	netBook := cost - accumulated
	gain, loss := int64(0), int64(0)
	if in.ProceedsCents > netBook {
		gain = in.ProceedsCents - netBook
	} else {
		loss = netBook - in.ProceedsCents
	}
	debits := []accountAmount{}
	credits := []accountAmount{{costAcct, cost, ""}}
	for _, leg := range []accountAmount{{ids[acctAccDep], accumulated, ""}, {in.ProceedsAccountID, in.ProceedsCents, ""}, {lossAcct, loss, ""}} {
		if leg.cents > 0 {
			debits = append(debits, leg)
		}
	}
	if gain > 0 {
		credits = append(credits, accountAmount{gainAcct, gain, ""})
	}
	key := "asset-dispose:" + strconv.FormatInt(id, 10)
	hash := sha256.Sum256([]byte(key))
	jid, err := postJournalLegs(ctx, tx, access.ID, u.ID, on, "Disposal: "+name, key, hash, debits, credits)
	if err != nil {
		if isPeriodClosed(err) {
			writeError(w, http.StatusUnprocessableEntity, "period_closed")
			return
		}
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE fixed_assets SET disposed_on=$3::date,proceeds_cents=$4,gain_loss_cents=$5,disposal_journal_id=$6 WHERE org_id=$1 AND id=$2`,
		access.ID, id, day(on), in.ProceedsCents, in.ProceedsCents-netBook, jid); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'asset.disposed',jsonb_build_object('asset_id',$3::bigint,'gain_loss_cents',$4::bigint))`,
		access.ID, u.ID, id, in.ProceedsCents-netBook); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "journal_id": jid, "gain_loss_cents": in.ProceedsCents - netBook, "net_book_cents": netBook})
}

// ensureAccount returns the account with a code, creating it with the given name and type when absent.
func ensureAccount(ctx context.Context, tx pgx.Tx, orgID int64, code, name, typ string) (int64, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO accounts(org_id,code,name,type,class) VALUES($1,$2,$3,$4,$5) ON CONFLICT (org_id,code) DO NOTHING`, orgID, code, name, typ, defaultAccountClass(typ, code)); err != nil {
		return 0, err
	}
	var id int64
	err := tx.QueryRow(ctx, `SELECT id FROM accounts WHERE org_id=$1 AND code=$2`, orgID, code).Scan(&id)
	return id, err
}

func (a *app) importAssets(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID int64  `json:"org_id"`
		CSV   string `json:"csv"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if len(in.CSV) > 1<<20 {
		writeError(w, 413, "payload_too_large")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	cr := csv.NewReader(strings.NewReader(in.CSV))
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		writeError(w, 400, "invalid_csv")
		return
	}
	indexes := map[string]int{}
	for i, h := range header {
		indexes[strings.ToLower(strings.TrimSpace(h))] = i
	}
	required := []string{"name", "acquired_on", "cost_cents", "useful_life_months"}
	for _, k := range required {
		if _, ok := indexes[k]; !ok {
			writeError(w, 400, "invalid_csv_headers")
			return
		}
	}
	type row struct {
		name, acq, method, averaging string
		cost, residual               int64
		life                         int
	}
	values := []row{}
	for {
		rec, e := cr.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			writeError(w, 400, "invalid_csv")
			return
		}
		if len(rec) != len(header) || len(values) >= 500 {
			writeError(w, 400, "invalid_csv")
			return
		}
		get := func(k, def string) string {
			i, ok := indexes[k]
			if !ok {
				return def
			}
			return strings.TrimSpace(rec[i])
		}
		cost, e1 := strconv.ParseInt(get("cost_cents", ""), 10, 64)
		life, e2 := strconv.Atoi(get("useful_life_months", ""))
		res, e3 := strconv.ParseInt(get("residual_value_cents", "0"), 10, 64)
		method := get("depreciation_method", "straight_line")
		avg := get("averaging_rule", "full_month")
		_, dateOK := parseDay(get("acquired_on", ""))
		name := get("name", "")
		if e1 != nil || e2 != nil || e3 != nil || !dateOK || !validTaxText(name, 200) || name == "" || cost < 1 || cost > maxMoneyCents || life < 1 || life > 600 || res < 0 || res > cost || (method != "straight_line" && method != "diminishing_value" && method != "manual") || (avg != "full_month" && avg != "half_month") {
			writeError(w, 400, "invalid_csv_row")
			return
		}
		values = append(values, row{name, get("acquired_on", ""), method, avg, cost, res, life})
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	for _, v := range values {
		_, err = tx.Exec(ctx, `INSERT INTO fixed_assets(org_id,name,acquired_on,cost_cents,useful_life_months,created_by,depreciation_method,averaging_rule,residual_value_cents) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, access.ID, v.name, v.acq, v.cost, v.life, u.ID, v.method, v.averaging, v.residual)
		if err != nil {
			serverError(w, err)
			return
		}
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]int{"imported": len(values)})
}

func (a *app) disposalSchedule(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	var from, to any
	if v := r.URL.Query().Get("from"); v != "" {
		d, valid := parseDay(v)
		if !valid {
			writeError(w, 400, "invalid_input")
			return
		}
		from = d
	}
	if v := r.URL.Query().Get("to"); v != "" {
		d, valid := parseDay(v)
		if !valid {
			writeError(w, 400, "invalid_input")
			return
		}
		to = d
	}
	rows, err := a.db.Query(r.Context(), `SELECT f.id,f.name,f.acquired_on::text,f.disposed_on::text,f.cost_cents,COALESCE(f.proceeds_cents,0),COALESCE(f.gain_loss_cents,0),
		COALESCE(a.code||' '||a.name,''),COALESCE((SELECT sum(d.amount_cents) FROM asset_depreciation d WHERE d.org_id=f.org_id AND d.asset_id=f.id),0)
		FROM fixed_assets f LEFT JOIN accounts a ON a.org_id=f.org_id AND a.id=f.cost_account_id
		WHERE f.org_id=$1 AND f.disposed_on IS NOT NULL
		AND ($2::date IS NULL OR f.disposed_on >= $2::date) AND ($3::date IS NULL OR f.disposed_on <= $3::date)
		ORDER BY f.disposed_on DESC,f.id LIMIT 501`, access.ID, from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type disposal struct {
		ID           int64  `json:"id"`
		Name         string `json:"name"`
		Acquired     string `json:"acquired_on"`
		Disposed     string `json:"disposed_on"`
		Cost         int64  `json:"cost_cents"`
		Proceeds     int64  `json:"proceeds_cents"`
		GainLoss     int64  `json:"gain_loss_cents"`
		CostAccount  string `json:"cost_account"`
		Depreciation int64  `json:"accumulated_depreciation_cents"`
	}
	out := []disposal{}
	for rows.Next() {
		var v disposal
		if err := rows.Scan(&v.ID, &v.Name, &v.Acquired, &v.Disposed, &v.Cost, &v.Proceeds, &v.GainLoss, &v.CostAccount, &v.Depreciation); err != nil {
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
