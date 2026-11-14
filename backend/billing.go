package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Billing: a catalog (products, plans, addons, coupons) and subscriptions. A subscription snapshots its prices when created;
// running it creates one invoice DRAFT per due cycle. Nothing is posted, charged or emailed automatically.
// ponytail: catalog rows are not edited, only deactivated (a subscription keeps its own snapshot, so old prices stay safe).
// Subscription invoice lines carry the plan, setup-fee or addon income account chosen on the catalog entry; posting credits each
// line's account (Sales when none). Other invoices post to Sales.

var billingCode = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,49}$`)

func validInterval(n int, unit string) bool {
	return n >= 1 && n <= 120 && validStage(unit, "week", "month", "year")
}

// revenueAccount returns the given account if it is a revenue account of the org, or the Sales account when id is 0.
func revenueAccount(ctx context.Context, tx pgx.Tx, org, id int64) (int64, bool, error) {
	if id == 0 {
		ids, err := accountIDs(ctx, tx, org)
		return ids[acctSales], err == nil, err
	}
	var typ string
	err := tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, org, id).Scan(&typ)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil && typ == "revenue", err
}

func (a *app) billingList(table, extra string) func(http.ResponseWriter, *http.Request, currentUser) {
	return func(w http.ResponseWriter, r *http.Request, u currentUser) {
		org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
		if !ok {
			return
		}
		access, ok := a.orgFor(w, r, u.ID, org, false)
		if !ok {
			return
		}
		var out []byte
		// table and extra are constants chosen at route registration, never user input.
		q := `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.id DESC),'[]'::jsonb) FROM (SELECT x.*` + extra + ` FROM ` + table + ` x WHERE x.org_id=$1 ORDER BY x.id DESC LIMIT 200) t`
		if err := a.db.QueryRow(r.Context(), q, access.ID).Scan(&out); err != nil {
			serverError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"rows":`))
		w.Write(out)
		w.Write([]byte(`}`))
	}
}

// billingStage activates or deactivates a catalog row.
func (a *app) billingStage(table string) func(http.ResponseWriter, *http.Request, currentUser) {
	return func(w http.ResponseWriter, r *http.Request, u currentUser) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var in stageIn
		if !decodeJSON(w, r, &in) {
			return
		}
		if !validStage(in.Stage, "activate", "deactivate") {
			writeError(w, 400, "invalid_input")
			return
		}
		access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
		if !ok {
			return
		}
		tag, err := a.db.Exec(r.Context(), `UPDATE `+table+` SET active=$3 WHERE org_id=$1 AND id=$2`, access.ID, id, in.Stage == "activate")
		if err != nil {
			serverError(w, err)
			return
		}
		if tag.RowsAffected() == 0 {
			writeError(w, 404, "not_found")
			return
		}
		writeJSON(w, 200, map[string]any{"id": id, "active": in.Stage == "activate"})
	}
}

func (a *app) createBillingProduct(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID       int64  `json:"org_id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || !validTaxText(in.Name, 100) || !validTaxText(in.Description, 4000) {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	var id int64
	if err := a.db.QueryRow(r.Context(), `INSERT INTO billing_products(org_id,name,description) VALUES($1,$2,$3) RETURNING id`, access.ID, in.Name, in.Description).Scan(&id); err != nil {
		if strings.Contains(err.Error(), "billing_products_name_uq") {
			writeError(w, 409, "duplicate_name")
			return
		}
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

type planInput struct {
	OrgID           int64  `json:"org_id"`
	ProductID       int64  `json:"product_id"`
	Name            string `json:"name"`
	Code            string `json:"code"`
	IntervalCount   int    `json:"interval_count"`
	IntervalUnit    string `json:"interval_unit"`
	BillingCycles   *int   `json:"billing_cycles"`
	Description     string `json:"description"`
	PricingModel    string `json:"pricing_model"`
	UnitName        string `json:"unit_name"`
	PriceCents      int64  `json:"price_cents"`
	TrialDays       int    `json:"trial_days"`
	SetupFeeCents   int64  `json:"setup_fee_cents"`
	ItemType        string `json:"item_type"`
	Tax             string `json:"tax"`
	ShowInWidget    bool   `json:"show_in_widget"`
	AllowPlanChange bool   `json:"allow_plan_change"`
	PlanAccountID   int64  `json:"plan_account_id"`
	SetupFeeAccount int64  `json:"setup_fee_account_id"`
}

func (a *app) createBillingPlan(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in planInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name, in.Code = strings.TrimSpace(in.Name), strings.TrimSpace(in.Code)
	if in.PricingModel == "" {
		in.PricingModel = "per_unit"
	}
	if in.ProductID <= 0 || in.Name == "" || !validTaxText(in.Name, 100) || !billingCode.MatchString(in.Code) || !validInterval(in.IntervalCount, in.IntervalUnit) ||
		(in.BillingCycles != nil && (*in.BillingCycles < 1 || *in.BillingCycles > 1200)) || !validTaxText(in.Description, 4000) || in.PricingModel != "per_unit" || !validTaxText(in.UnitName, 50) ||
		in.PriceCents < 0 || in.PriceCents > maxMoneyCents || in.TrialDays < 0 || in.TrialDays > 365 || in.SetupFeeCents < 0 || in.SetupFeeCents > maxMoneyCents || !validStage(in.ItemType, "goods", "service") {
		writeError(w, 400, "invalid_input")
		return
	}
	if in.Tax != "" && in.Tax != "none" {
		writeError(w, 422, "unsupported_gst")
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
	if !a.productExists(w, ctx, tx, access.ID, in.ProductID) {
		return
	}
	planAcct, ok1, err := revenueAccount(ctx, tx, access.ID, in.PlanAccountID)
	if err != nil {
		serverError(w, err)
		return
	}
	setupAcct, ok2, err := revenueAccount(ctx, tx, access.ID, in.SetupFeeAccount)
	if err != nil {
		serverError(w, err)
		return
	}
	if !ok1 || !ok2 {
		writeError(w, 422, "invalid_revenue_account")
		return
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO billing_plans(org_id,product_id,name,code,interval_count,interval_unit,billing_cycles,description,unit_name,price_cents,trial_days,setup_fee_cents,item_type,show_in_widget,allow_plan_change,plan_account_id,setup_fee_account_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17) RETURNING id`, access.ID, in.ProductID, in.Name, in.Code, in.IntervalCount, in.IntervalUnit, in.BillingCycles, in.Description, in.UnitName, in.PriceCents, in.TrialDays, in.SetupFeeCents, in.ItemType, in.ShowInWidget, in.AllowPlanChange, planAcct, setupAcct).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "billing_plans_code_uq") {
			writeError(w, 409, "duplicate_code")
			return
		}
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *app) productExists(w http.ResponseWriter, ctx context.Context, tx pgx.Tx, org, id int64) bool {
	var one int
	err := tx.QueryRow(ctx, `SELECT 1 FROM billing_products WHERE org_id=$1 AND id=$2`, org, id).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 422, "invalid_product")
		return false
	}
	if err != nil {
		serverError(w, err)
		return false
	}
	return true
}

// idsInOrg reports whether every id names a row of the table in this org.
func idsInOrg(ctx context.Context, tx pgx.Tx, table string, org int64, ids []int64, extra string) (bool, error) {
	if len(ids) == 0 {
		return true, nil
	}
	var n int
	err := tx.QueryRow(ctx, `SELECT count(DISTINCT id) FROM `+table+` WHERE org_id=$1 AND id=ANY($2)`+extra, org, ids).Scan(&n)
	seen := map[int64]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	return n == len(seen), err
}

type addonInput struct {
	OrgID         int64   `json:"org_id"`
	ProductID     int64   `json:"product_id"`
	Name          string  `json:"name"`
	Code          string  `json:"code"`
	Description   string  `json:"description"`
	AddonType     string  `json:"addon_type"`
	IntervalCount int     `json:"interval_count"`
	IntervalUnit  string  `json:"interval_unit"`
	PricingModel  string  `json:"pricing_model"`
	UnitName      string  `json:"unit_name"`
	PriceCents    int64   `json:"price_cents"`
	PlanIDs       []int64 `json:"plan_ids"`
	ItemType      string  `json:"item_type"`
	Tax           string  `json:"tax"`
	ShowInWidget  bool    `json:"show_in_widget"`
	AllowPortal   bool    `json:"allow_portal_association"`
	AccountID     int64   `json:"account_id"`
}

func (a *app) createBillingAddon(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in addonInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name, in.Code = strings.TrimSpace(in.Name), strings.TrimSpace(in.Code)
	if in.PricingModel == "" {
		in.PricingModel = "per_unit"
	}
	if in.AddonType == "one_time" {
		in.IntervalCount, in.IntervalUnit = 1, "month"
	}
	if in.PlanIDs == nil {
		in.PlanIDs = []int64{}
	}
	if in.ProductID <= 0 || in.Name == "" || !validTaxText(in.Name, 100) || !billingCode.MatchString(in.Code) || !validTaxText(in.Description, 4000) || !validStage(in.AddonType, "one_time", "recurring") ||
		!validInterval(in.IntervalCount, in.IntervalUnit) || in.PricingModel != "per_unit" || !validTaxText(in.UnitName, 50) || in.PriceCents < 0 || in.PriceCents > maxMoneyCents ||
		!validStage(in.ItemType, "goods", "service") || len(in.PlanIDs) > 200 {
		writeError(w, 400, "invalid_input")
		return
	}
	if in.Tax != "" && in.Tax != "none" {
		writeError(w, 422, "unsupported_gst")
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
	if !a.productExists(w, ctx, tx, access.ID, in.ProductID) {
		return
	}
	if good, err := idsInOrg(ctx, tx, "billing_plans", access.ID, in.PlanIDs, ` AND product_id=`+itoa64(in.ProductID)); err != nil {
		serverError(w, err)
		return
	} else if !good {
		writeError(w, 422, "invalid_plan")
		return
	}
	acct, good, err := revenueAccount(ctx, tx, access.ID, in.AccountID)
	if err != nil {
		serverError(w, err)
		return
	}
	if !good {
		writeError(w, 422, "invalid_revenue_account")
		return
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO billing_addons(org_id,product_id,name,code,description,addon_type,interval_count,interval_unit,unit_name,price_cents,plan_ids,item_type,show_in_widget,allow_portal_association,account_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING id`, access.ID, in.ProductID, in.Name, in.Code, in.Description, in.AddonType, in.IntervalCount, in.IntervalUnit, in.UnitName, in.PriceCents, in.PlanIDs, in.ItemType, in.ShowInWidget, in.AllowPortal, acct).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "billing_addons_code_uq") {
			writeError(w, 409, "duplicate_code")
			return
		}
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

type couponInput struct {
	OrgID            int64   `json:"org_id"`
	ProductID        int64   `json:"product_id"`
	Name             string  `json:"name"`
	Code             string  `json:"code"`
	DiscountType     string  `json:"discount_type"`
	DiscountCents    int64   `json:"discount_cents"`
	DiscountBP       int     `json:"discount_basis_points"`
	RedemptionType   string  `json:"redemption_type"`
	RedemptionCycles int     `json:"redemption_cycles"`
	Applicability    string  `json:"applicability"`
	PlanIDs          []int64 `json:"plan_ids"`
	AddonIDs         []int64 `json:"addon_ids"`
	ExpiresOn        string  `json:"expires_on"`
	MaxRedemptions   *int    `json:"max_redemptions"`
}

func (a *app) createBillingCoupon(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in couponInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name, in.Code = strings.TrimSpace(in.Name), strings.TrimSpace(in.Code)
	if in.PlanIDs == nil {
		in.PlanIDs = []int64{}
	}
	if in.AddonIDs == nil {
		in.AddonIDs = []int64{}
	}
	var expires *time.Time
	okDate := true
	if in.ExpiresOn != "" {
		t, good := parseDay(in.ExpiresOn)
		okDate, expires = good, &t
	}
	flat := in.DiscountType == "flat"
	// 100% would make an invoice total zero, which invoices refuse.
	okDiscount := (flat && in.DiscountCents > 0 && in.DiscountCents <= maxMoneyCents && in.DiscountBP == 0) || (in.DiscountType == "percent" && in.DiscountBP > 0 && in.DiscountBP <= 9999 && in.DiscountCents == 0)
	if in.ProductID <= 0 || in.Name == "" || !validTaxText(in.Name, 100) || !billingCode.MatchString(in.Code) || !okDate || !okDiscount ||
		!validStage(in.RedemptionType, "one_time", "limited", "forever") || (in.RedemptionType == "limited") != (in.RedemptionCycles > 0) || in.RedemptionCycles > 1200 ||
		!validStage(in.Applicability, "plan", "addon", "both") || len(in.PlanIDs) > 200 || len(in.AddonIDs) > 200 || (in.MaxRedemptions != nil && (*in.MaxRedemptions < 1 || *in.MaxRedemptions > 1000000)) {
		writeError(w, 400, "invalid_input")
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
	if !a.productExists(w, ctx, tx, access.ID, in.ProductID) {
		return
	}
	goodPlans, err := idsInOrg(ctx, tx, "billing_plans", access.ID, in.PlanIDs, ` AND product_id=`+itoa64(in.ProductID))
	if err != nil {
		serverError(w, err)
		return
	}
	goodAddons, err := idsInOrg(ctx, tx, "billing_addons", access.ID, in.AddonIDs, ` AND addon_type='recurring' AND product_id=`+itoa64(in.ProductID))
	if err != nil {
		serverError(w, err)
		return
	}
	if !goodPlans || !goodAddons {
		writeError(w, 422, "invalid_plan")
		return
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO billing_coupons(org_id,product_id,name,code,discount_type,discount_cents,discount_basis_points,redemption_type,redemption_cycles,applicability,plan_ids,addon_ids,expires_on,max_redemptions)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id`, access.ID, in.ProductID, in.Name, in.Code, in.DiscountType, in.DiscountCents, in.DiscountBP, in.RedemptionType, in.RedemptionCycles, in.Applicability, in.PlanIDs, in.AddonIDs, expires, in.MaxRedemptions).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "billing_coupons_code_uq") {
			writeError(w, 409, "duplicate_code")
			return
		}
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

// ---- subscriptions ----

type subLine struct {
	Kind           string `json:"kind"` // plan, addon, setup, onetime, item
	AddonID        int64  `json:"addon_id,omitempty"`
	Description    string `json:"description"`
	Quantity       int64  `json:"quantity"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	AccountID      int64  `json:"account_id,omitempty"` // income account frozen at subscribe time; 0 = Sales
}

type subAddonIn struct {
	AddonID  int64 `json:"addon_id"`
	Quantity int64 `json:"quantity"`
}

type subscriptionInput struct {
	OrgID         int64           `json:"org_id"`
	ContactID     int64           `json:"contact_id"`
	Mode          string          `json:"mode"`
	PlanID        int64           `json:"plan_id"`
	Quantity      int64           `json:"quantity"`
	Addons        []subAddonIn    `json:"addons"`
	Items         []invoiceLineIn `json:"items"`
	IntervalCount int             `json:"interval_count"`
	IntervalUnit  string          `json:"interval_unit"`
	BillingCycles *int            `json:"billing_cycles"`
	StartDate     string          `json:"start_date"`
	CouponCode    string          `json:"coupon_code"`
	Reference     string          `json:"reference"`
	Notes         string          `json:"notes"`
}

func lineAmount(q, p int64) (int64, bool) {
	if q < 1 || q > maxQty || p < 0 || p > maxMoneyCents || (p > 0 && p > maxMoneyCents/q) {
		return 0, false
	}
	return q * p, true
}

func (a *app) createSubscription(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in subscriptionInput
	if !decodeJSON(w, r, &in) {
		return
	}
	start, okDate := parseDay(in.StartDate)
	in.CouponCode = strings.TrimSpace(in.CouponCode)
	if !okDate || in.ContactID <= 0 || !validStage(in.Mode, "plan", "items") || !validTaxText(in.Reference, 100) || !validTaxText(in.Notes, 4000) || len(in.Addons) > 50 {
		writeError(w, 400, "invalid_input")
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
	var kind string
	err = tx.QueryRow(ctx, `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, in.ContactID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && kind == "supplier") {
		writeError(w, 422, "invalid_contact")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	var first, recurring []subLine
	var addonsSnap []map[string]any
	var planID *int64
	var planName string
	var couponID *int64
	var couponSnap []byte
	intervalCount, intervalUnit, cycles := in.IntervalCount, in.IntervalUnit, in.BillingCycles
	firstBill := start
	qty := in.Quantity
	if in.Mode == "plan" {
		var p struct {
			name, unitName, itemType, code string
			price, setup                   int64
			trial, count                   int
			unit                           string
			cycles                         *int
			product                        int64
			active                         bool
			planAcct, setupAcct            int64
		}
		err = tx.QueryRow(ctx, `SELECT name,code,unit_name,price_cents,setup_fee_cents,trial_days,interval_count,interval_unit,billing_cycles,product_id,active,plan_account_id,setup_fee_account_id FROM billing_plans WHERE org_id=$1 AND id=$2`, access.ID, in.PlanID).
			Scan(&p.name, &p.code, &p.unitName, &p.price, &p.setup, &p.trial, &p.count, &p.unit, &p.cycles, &p.product, &p.active, &p.planAcct, &p.setupAcct)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !p.active) {
			writeError(w, 422, "invalid_plan")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if len(in.Items) > 0 || qty < 1 || qty > maxQty {
			writeError(w, 400, "invalid_input")
			return
		}
		intervalCount, intervalUnit, cycles = p.count, p.unit, p.cycles
		firstBill = start.AddDate(0, 0, p.trial)
		planID, planName = &in.PlanID, p.name
		recurring = append(recurring, subLine{Kind: "plan", Description: p.name, Quantity: qty, UnitPriceCents: p.price, AccountID: p.planAcct})
		first = append(first, recurring[0])
		if p.setup > 0 {
			first = append(first, subLine{Kind: "setup", Description: p.name + " setup fee", Quantity: 1, UnitPriceCents: p.setup, AccountID: p.setupAcct})
		}
		for _, ad := range in.Addons {
			var ao struct {
				name, typ string
				price     int64
				plans     []int64
				active    bool
				product   int64
				acct      int64
			}
			err = tx.QueryRow(ctx, `SELECT name,addon_type,price_cents,plan_ids,active,product_id,account_id FROM billing_addons WHERE org_id=$1 AND id=$2`, access.ID, ad.AddonID).Scan(&ao.name, &ao.typ, &ao.price, &ao.plans, &ao.active, &ao.product, &ao.acct)
			okPlan := len(ao.plans) == 0
			for _, id := range ao.plans {
				okPlan = okPlan || id == in.PlanID
			}
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!ao.active || !okPlan || ao.product != p.product)) || ad.Quantity < 1 || ad.Quantity > maxQty {
				writeError(w, 422, "invalid_addon")
				return
			}
			if err != nil {
				serverError(w, err)
				return
			}
			if ao.typ == "recurring" {
				l := subLine{Kind: "addon", AddonID: ad.AddonID, Description: ao.name, Quantity: ad.Quantity, UnitPriceCents: ao.price, AccountID: ao.acct}
				first, recurring = append(first, l), append(recurring, l)
			} else {
				first = append(first, subLine{Kind: "onetime", Description: ao.name, Quantity: ad.Quantity, UnitPriceCents: ao.price, AccountID: ao.acct})
			}
			addonsSnap = append(addonsSnap, map[string]any{"addon_id": ad.AddonID, "quantity": ad.Quantity})
		}
		if in.CouponCode != "" {
			var c struct {
				id                         int64
				name, dtype, redType, appl string
				cents                      int64
				bp, redCycles, redemptions int
				plans, addons              []int64
				expires                    *time.Time
				max                        *int
				active                     bool
				product                    int64
			}
			err = tx.QueryRow(ctx, `SELECT id,name,discount_type,discount_cents,discount_basis_points,redemption_type,redemption_cycles,applicability,plan_ids,addon_ids,expires_on,max_redemptions,redemptions,active,product_id
				FROM billing_coupons WHERE org_id=$1 AND lower(code)=lower($2) FOR UPDATE`, access.ID, in.CouponCode).
				Scan(&c.id, &c.name, &c.dtype, &c.cents, &c.bp, &c.redType, &c.redCycles, &c.appl, &c.plans, &c.addons, &c.expires, &c.max, &c.redemptions, &c.active, &c.product)
			okPlan := len(c.plans) == 0
			for _, id := range c.plans {
				okPlan = okPlan || id == in.PlanID
			}
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!c.active || !okPlan || c.product != p.product || (c.expires != nil && c.expires.Before(start)) || (c.max != nil && c.redemptions >= *c.max))) {
				writeError(w, 422, "invalid_coupon")
				return
			}
			if err != nil {
				serverError(w, err)
				return
			}
			if _, err = tx.Exec(ctx, `UPDATE billing_coupons SET redemptions=redemptions+1 WHERE org_id=$1 AND id=$2`, access.ID, c.id); err != nil {
				serverError(w, err)
				return
			}
			couponID = &c.id
			couponSnap, _ = json.Marshal(map[string]any{"code": in.CouponCode, "name": c.name, "discount_type": c.dtype, "discount_cents": c.cents, "discount_basis_points": c.bp,
				"redemption_type": c.redType, "redemption_cycles": c.redCycles, "applicability": c.appl, "addon_ids": c.addons, "subscribed_addons": addonsSnap})
		}
	} else {
		if in.PlanID != 0 || len(in.Addons) > 0 || in.CouponCode != "" || !validInterval(intervalCount, intervalUnit) || (cycles != nil && (*cycles < 1 || *cycles > 1200)) {
			writeError(w, 400, "invalid_input")
			return
		}
		lines, _, code := buildInvoiceLines(in.Items)
		if code != "" {
			status := 400
			if code == "unsupported_gst" {
				status = 422
			}
			writeError(w, status, code)
			return
		}
		for _, l := range lines {
			recurring = append(recurring, subLine{Kind: "item", Description: l.Description, Quantity: l.Quantity, UnitPriceCents: l.UnitPriceCents})
		}
		first, qty = recurring, 1
	}
	for _, set := range [][]subLine{first, recurring} {
		var total int64
		for _, l := range set {
			amt, good := lineAmount(l.Quantity, l.UnitPriceCents)
			total += amt
			if !good || total > maxMoneyCents {
				writeError(w, 400, "invalid_input")
				return
			}
		}
	}
	firstJSON, _ := json.Marshal(first)
	recJSON, _ := json.Marshal(recurring)
	if addonsSnap == nil {
		addonsSnap = []map[string]any{}
	}
	addonJSON, _ := json.Marshal(addonsSnap)
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO billing_subscriptions(org_id,contact_id,mode,plan_id,plan_name,quantity,interval_count,interval_unit,billing_cycles,start_date,first_bill_date,next_bill_date,first_lines,recurring_lines,addons,coupon_id,coupon,reference,notes,created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11,$12,$13,$14,$15,$16,$17,$18,$19) RETURNING id`, access.ID, in.ContactID, in.Mode, planID, planName, qty, intervalCount, intervalUnit, cycles, start, firstBill, firstJSON, recJSON, addonJSON, couponID, nilBytes(couponSnap), in.Reference, in.Notes, u.ID).Scan(&id)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'subscription.created',jsonb_build_object('subscription_id',$3::bigint))`, access.ID, u.ID, id); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "first_bill_date": day(firstBill)})
}

func nilBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func (a *app) cancelSubscription(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in stageIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Stage != "cancel" {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE billing_subscriptions SET status='cancelled',next_bill_date=NULL,cancelled_at=now() WHERE org_id=$1 AND id=$2 AND status='active'`, access.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 409, "not_active")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": "cancelled"})
}

// couponDiscount works out the discount on one invoice: only plan/addon lines the coupon applies to, only in cycles it covers.
func couponDiscount(c map[string]any, lines []subLine, cycle int) int64 {
	if c == nil {
		return 0
	}
	redType, _ := c["redemption_type"].(string)
	cyc, _ := c["redemption_cycles"].(float64)
	if (redType == "one_time" && cycle > 1) || (redType == "limited" && float64(cycle) > cyc) {
		return 0
	}
	appl, _ := c["applicability"].(string)
	var eligible int64
	listed := map[int64]bool{}
	if ids, ok := c["addon_ids"].([]any); ok {
		for _, v := range ids {
			if f, ok := v.(float64); ok {
				listed[int64(f)] = true
			}
		}
	}
	for _, l := range lines {
		if (l.Kind == "plan" && appl != "addon") || (l.Kind == "addon" && appl != "plan" && (len(listed) == 0 || listed[l.AddonID])) {
			amt, _ := lineAmount(l.Quantity, l.UnitPriceCents)
			eligible += amt
		}
	}
	dtype, _ := c["discount_type"].(string)
	if dtype == "percent" {
		bp, _ := c["discount_basis_points"].(float64)
		return (eligible*int64(bp) + 5000) / 10000
	}
	cents, _ := c["discount_cents"].(float64)
	return min(eligible, int64(cents))
}

// runSubscriptions creates one invoice draft per due cycle (up to 24 per subscription per run), then advances the schedule.
// A cycle whose total is zero after a coupon (or a free plan) advances the schedule but creates no invoice, and is counted as skipped.
func (a *app) runSubscriptions(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID int64  `json:"org_id"`
		AsOf  string `json:"as_of"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	asOf, ok := parseDay(in.AsOf)
	if !ok {
		writeError(w, 400, "invalid_input")
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
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	type sub struct {
		id, contact                  int64
		count, billed                int
		unit, ref, notes             string
		cycles                       *int
		first, next                  time.Time
		firstLines, recLines, coupon []byte
		terms                        int
	}
	rows, err := tx.Query(ctx, `SELECT s.id,s.contact_id,s.interval_count,s.cycles_billed,s.interval_unit,s.reference,s.notes,s.billing_cycles,s.first_bill_date,s.next_bill_date,s.first_lines,s.recurring_lines,COALESCE(s.coupon,'null'::jsonb),c.payment_terms_days
		FROM billing_subscriptions s JOIN contacts c ON c.org_id=s.org_id AND c.id=s.contact_id WHERE s.org_id=$1 AND s.status='active' AND s.next_bill_date<=$2 ORDER BY s.id FOR UPDATE OF s`, access.ID, asOf)
	if err != nil {
		serverError(w, err)
		return
	}
	var subs []sub
	for rows.Next() {
		var s sub
		if err = rows.Scan(&s.id, &s.contact, &s.count, &s.billed, &s.unit, &s.ref, &s.notes, &s.cycles, &s.first, &s.next, &s.firstLines, &s.recLines, &s.coupon, &s.terms); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		subs = append(subs, s)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	created, skipped := []int64{}, 0
	for _, s := range subs {
		var couponMap map[string]any
		_ = json.Unmarshal(s.coupon, &couponMap)
		for n := 0; n < 24 && !s.next.After(asOf); n++ {
			cycle := s.billed + 1
			raw := s.recLines
			if cycle == 1 {
				raw = s.firstLines
			}
			var lines []subLine
			if err = json.Unmarshal(raw, &lines); err != nil {
				serverError(w, err)
				return
			}
			var sum int64
			for _, l := range lines {
				amt, _ := lineAmount(l.Quantity, l.UnitPriceCents)
				sum += amt
			}
			discount := couponDiscount(couponMap, lines, cycle)
			if sum-discount <= 0 {
				skipped++
			} else {
				commercial, _ := json.Marshal(map[string]any{"discount_type": "flat", "discount_cents": discount})
				var invID int64
				err = tx.QueryRow(ctx, `INSERT INTO invoices(org_id,contact_id,invoice_date,due_date,total_cents,created_by,reference,notes,commercial,subscription_id,subscription_cycle)
					VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (org_id,subscription_id,subscription_cycle) DO NOTHING RETURNING id`,
					access.ID, s.contact, s.next, s.next.AddDate(0, 0, s.terms), sum-discount, u.ID, s.ref, s.notes, commercial, s.id, cycle).Scan(&invID)
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					serverError(w, err)
					return
				}
				if err == nil {
					for i, l := range lines {
						amt, _ := lineAmount(l.Quantity, l.UnitPriceCents)
						if _, err = tx.Exec(ctx, `INSERT INTO invoice_lines(org_id,invoice_id,line_no,description,quantity,unit_price_cents,amount_cents,revenue_account_id) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8::bigint,0))`,
							access.ID, invID, i+1, l.Description, l.Quantity, l.UnitPriceCents, amt, l.AccountID); err != nil {
							serverError(w, err)
							return
						}
					}
					created = append(created, invID)
				}
			}
			s.billed++
			if s.cycles != nil && s.billed >= *s.cycles {
				if _, err = tx.Exec(ctx, `UPDATE billing_subscriptions SET cycles_billed=$3,status='completed',next_bill_date=NULL WHERE org_id=$1 AND id=$2`, access.ID, s.id, s.billed); err != nil {
					serverError(w, err)
					return
				}
				s.next = time.Time{}
				break
			}
			s.next = nthRun(s.first, s.count, s.unit, s.billed)
			if _, err = tx.Exec(ctx, `UPDATE billing_subscriptions SET cycles_billed=$3,next_bill_date=$4 WHERE org_id=$1 AND id=$2`, access.ID, s.id, s.billed, s.next); err != nil {
				serverError(w, err)
				return
			}
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'subscriptions.run',jsonb_build_object('created',$3::int,'skipped',$4::int))`, access.ID, u.ID, len(created), skipped); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"created_invoice_ids": created, "skipped": skipped})
}
