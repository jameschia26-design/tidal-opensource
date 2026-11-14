package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Tax catalogue and organisation tax settings (Phase 1c).
//
// The catalogue is dated statutory data: a code resolves to the rate that was in force on the
// document's own date, so a 2023 invoice keeps 8% and one raised from 2024 gets 9%. Document lines
// still store the rate they were priced at, so nothing re-prices posted history.

// taxRate is one dated catalogue row.
type taxRate struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	RateBP        int64  `json:"rate_bp"`
	Kind          string `json:"kind"`
	AppliesTo     string `json:"applies_to"`
	EffectiveFrom string `json:"effective_from"`
	EffectiveTo   string `json:"effective_to"`
	IsActive      bool   `json:"is_active"`
}

// taxKindsSupportedOnDocuments lists every Singapore catalogue kind a document can price. Imported
// services and reverse charge are self-assessed: they add matching GST input and output legs instead
// of increasing the supplier invoice total.
var taxKindsSupportedOnDocuments = map[string]bool{
	"standard": true, "zero": true, "exempt": true, "out_of_scope": true, "no_tax": true,
	"import": true, "reverse_charge": true,
}

// taxCatalogueOn returns the active rates for one side of the ledger on a date.
func taxCatalogueOn(ctx context.Context, q rowQueryer, side, on string) (map[string]taxRate, error) {
	rows, err := q.Query(ctx, `SELECT code,name,rate_bp,kind,applies_to,effective_from::text,COALESCE(effective_to::text,''),is_active
		FROM tax_rates WHERE applies_to IN ($1,'both') AND effective_from<=$2::date AND (effective_to IS NULL OR effective_to>=$2::date)
		ORDER BY code,effective_from`, side, on)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]taxRate{}
	for rows.Next() {
		var t taxRate
		if err := rows.Scan(&t.Code, &t.Name, &t.RateBP, &t.Kind, &t.AppliesTo, &t.EffectiveFrom, &t.EffectiveTo, &t.IsActive); err != nil {
			return nil, err
		}
		if t.IsActive {
			out[t.Code] = t
		}
	}
	return out, rows.Err()
}

// taxRateOn resolves a code to the rate in force on a date for one side of the ledger.
func taxRateOn(ctx context.Context, q rowQueryer, code, side, on string) (rateBP int64, kind string, found bool, err error) {
	err = q.QueryRow(ctx, `SELECT rate_bp, kind FROM tax_rates
		WHERE code=$1 AND applies_to IN ($2,'both') AND is_active
		  AND effective_from<=$3::date AND (effective_to IS NULL OR effective_to>=$3::date)
		ORDER BY effective_from DESC LIMIT 1`, strings.ToUpper(code), side, on).Scan(&rateBP, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	return rateBP, kind, true, nil
}

// taxRateBPOn is the rate a document should price a standard-rate line at. A missing catalogue row
// falls back to the statutory table so a date the catalogue does not cover is never silently zero.
func taxRateBPOn(ctx context.Context, q rowQueryer, code, side, on string, fallback func() int64) (int64, error) {
	rate, _, found, err := taxRateOn(ctx, q, code, side, on)
	if err != nil {
		return 0, err
	}
	if !found && fallback != nil {
		return fallback(), nil
	}
	return rate, nil
}

// accountTaxDefaults maps each account to the tax code a new line on it starts with.
func accountTaxDefaults(ctx context.Context, q rowQueryer, orgID int64) (map[int64]string, error) {
	rows, err := q.Query(ctx, `SELECT id, COALESCE(tax_rate_code,'') FROM accounts WHERE org_id=$1`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var code string
		if err := rows.Scan(&id, &code); err != nil {
			return nil, err
		}
		if code != "" {
			out[id] = code
		}
	}
	return out, rows.Err()
}

// accountTaxSide picks the side of the ledger an account's default rate belongs to.
func accountTaxSide(typ string) string {
	if typ == "revenue" {
		return "sale"
	}
	return "purchase"
}

// validAccountTaxCode accepts a blank code or one the catalogue can price on this account's side.
func validAccountTaxCode(ctx context.Context, q rowQueryer, typ, code string) (bool, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return true, nil
	}
	side := accountTaxSide(typ)
	var kind string
	err := q.QueryRow(ctx, `SELECT kind FROM tax_rates WHERE code=$1 AND applies_to IN ($2,'both') AND is_active
		ORDER BY effective_from DESC LIMIT 1`, code, side).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return taxKindsSupportedOnDocuments[kind], nil
}

// listTaxRates: GET /api/v1/tax/rates?org_id&on=YYYY-MM-DD — the catalogue in force on a date.
func (a *app) listTaxRates(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	if _, ok := a.orgFor(w, r, u.ID, org, false); !ok {
		return
	}
	on := r.URL.Query().Get("on")
	if on == "" {
		on = day(time.Now())
	}
	if _, ok := parseDay(on); !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT code, name, rate_bp, kind, applies_to,
		effective_from::text, COALESCE(effective_to::text,''), is_active
		FROM tax_rates
		WHERE effective_from<=$1::date AND (effective_to IS NULL OR effective_to>=$1::date)
		ORDER BY kind, code, effective_from`, on)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []taxRate{}
	for rows.Next() {
		var t taxRate
		if err := rows.Scan(&t.Code, &t.Name, &t.RateBP, &t.Kind, &t.AppliesTo, &t.EffectiveFrom, &t.EffectiveTo, &t.IsActive); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"on": on, "rates": out})
}

// taxSettings is Xero's Financial settings tax block. The tax ID number is the company's GST number,
// so there is one value, not two that can disagree.
type taxSettings struct {
	TaxBasis                 string `json:"tax_basis"`
	TaxID                    string `json:"tax_id"`
	TaxDisplayName           string `json:"tax_display_name"`
	TaxPeriod                string `json:"tax_period"`
	DefaultSalesTreatment    string `json:"default_sales_treatment"`
	DefaultPurchaseTreatment string `json:"default_purchase_treatment"`
}

func validTaxSettings(in *taxSettings) bool {
	in.TaxID = strings.TrimSpace(in.TaxID)
	in.TaxDisplayName = strings.TrimSpace(in.TaxDisplayName)
	if !validTaxText(in.TaxID, 30) || !validTaxText(in.TaxDisplayName, 40) || in.TaxDisplayName == "" {
		return false
	}
	if in.TaxBasis != "accrual" && in.TaxBasis != "payment" {
		return false
	}
	switch in.TaxPeriod {
	case "monthly", "quarterly", "twice_monthly":
	default:
		return false
	}
	for _, treatment := range []string{in.DefaultSalesTreatment, in.DefaultPurchaseTreatment} {
		if treatment != "exclusive" && treatment != "inclusive" {
			return false
		}
	}
	return true
}

// taxSettingsRoute: GET or PUT /api/v1/tax/settings.
func (a *app) taxSettingsRoute(w http.ResponseWriter, r *http.Request, u currentUser) {
	if r.Method == http.MethodGet {
		org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
		if !ok {
			return
		}
		access, ok := a.orgFor(w, r, u.ID, org, false)
		if !ok {
			return
		}
		var s taxSettings
		err := a.db.QueryRow(r.Context(), `SELECT tax_basis, gst_number, tax_display_name, tax_period,
			default_sales_treatment, default_purchase_treatment FROM organizations WHERE id=$1`, access.ID).
			Scan(&s.TaxBasis, &s.TaxID, &s.TaxDisplayName, &s.TaxPeriod, &s.DefaultSalesTreatment, &s.DefaultPurchaseTreatment)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, s)
		return
	}
	var in struct {
		OrgID int64 `json:"org_id"`
		taxSettings
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validTaxSettings(&in.taxSettings) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE organizations SET tax_basis=$2, gst_number=$3, tax_display_name=$4,
		tax_period=$5, default_sales_treatment=$6, default_purchase_treatment=$7, updated_at=now() WHERE id=$1`,
		access.ID, in.TaxBasis, in.TaxID, in.TaxDisplayName, in.TaxPeriod, in.DefaultSalesTreatment, in.DefaultPurchaseTreatment); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,'tax_settings.updated')`, access.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, in.taxSettings)
}

// updateAccountTax: PUT /api/v1/accounts/{id} — sets the account's default tax rate.
// Only the default rate is editable here; name, type and class keep their own rules.
// bulkAccountTax applies one default tax rate to several accounts, matching Xero's bulk change.
func (a *app) bulkAccountTax(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID       int64   `json:"org_id"`
		AccountIDs  []int64 `json:"account_ids"`
		TaxRateCode string  `json:"tax_rate_code"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if len(in.AccountIDs) == 0 || len(in.AccountIDs) > 500 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" && access.Role != "accountant" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	code := strings.ToUpper(strings.TrimSpace(in.TaxRateCode))
	if !validTaxCodeShape(code) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE accounts SET tax_rate_code=NULLIF($3,'') WHERE org_id=$1 AND id=ANY($2::bigint[])`, access.ID, in.AccountIDs, code)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": tag.RowsAffected(), "tax_rate_code": code})
}

func (a *app) updateAccountTax(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID       int64  `json:"org_id"`
		TaxRateCode string `json:"tax_rate_code"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" && access.Role != "accountant" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	ctx := r.Context()
	var typ string
	if err := a.db.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, id).Scan(&typ); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	code := strings.ToUpper(strings.TrimSpace(in.TaxRateCode))
	okCode, err := validAccountTaxCode(ctx, a.db, typ, code)
	if err != nil {
		serverError(w, err)
		return
	}
	if !okCode {
		writeError(w, http.StatusUnprocessableEntity, "tax_rate_not_allowed")
		return
	}
	if code == "" {
		if _, err := a.db.Exec(ctx, `UPDATE accounts SET tax_rate_code=NULL WHERE org_id=$1 AND id=$2`, access.ID, id); err != nil {
			serverError(w, err)
			return
		}
	} else if _, err := a.db.Exec(ctx, `UPDATE accounts SET tax_rate_code=$3 WHERE org_id=$1 AND id=$2`, access.ID, id, code); err != nil {
		serverError(w, err)
		return
	}
	if _, err := a.db.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,'account.tax_rate_updated')`, access.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "tax_rate_code": code})
}

// createTaxRate: POST /api/v1/tax/rates — adds a company-specific dated rate to the shared catalogue.
func validTaxCode(code string) bool {
	if len(code) == 0 || len(code) > 20 {
		return false
	}
	for _, r := range code {
		if !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' {
			return false
		}
	}
	return true
}

func (a *app) createTaxRate(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID         int64  `json:"org_id"`
		Code          string `json:"code"`
		Name          string `json:"name"`
		RateBP        int64  `json:"rate_bp"`
		Kind          string `json:"kind"`
		AppliesTo     string `json:"applies_to"`
		EffectiveFrom string `json:"effective_from"`
		EffectiveTo   string `json:"effective_to"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" && access.Role != "accountant" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	if !validTaxCode(in.Code) || in.Name == "" || len(in.Name) > 100 || in.RateBP < 0 || in.RateBP > 10000 ||
		!taxKindsSupportedOnDocuments[in.Kind] || (in.AppliesTo != "sale" && in.AppliesTo != "purchase" && in.AppliesTo != "both") {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	from, okFrom := parseDay(in.EffectiveFrom)
	if !okFrom || (in.EffectiveTo != "" && in.EffectiveTo < in.EffectiveFrom) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	_ = from
	_, err := a.db.Exec(r.Context(), `INSERT INTO tax_rates(code,name,rate_bp,kind,applies_to,effective_from,effective_to)
		VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,'')::date)`, in.Code, in.Name, in.RateBP, in.Kind, in.AppliesTo, in.EffectiveFrom, in.EffectiveTo)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "tax_rate_exists")
			return
		}
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"code": in.Code, "status": "created"})
}
