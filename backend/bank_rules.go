package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Bank rules: a statement line suggests a rule's actions when the line runs in the rule's direction,
// its description matches the rule's text conditions and the extra conditions match in the rule's
// any/all mode. Active rules run in priority order (lowest number first), so position 1 beats position
// 2 regardless of id. Rules only ever suggest; nothing posts itself.
//
// A rule can also suggest a contact, a tax rate, a tracking option and, for a transfer, the other bank
// account. Those are shown on the suggestion and prefilled in the screen; they are never applied to a
// posted entry without a person choosing it.
//
// ponytail: conditions are text contains plus an amount range (a CSV statement has no payee column);
// add split lines and automatic application when a statement source can supply them.

type bankRuleCondition struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

type bankRule struct {
	ID                int64               `json:"id"`
	Name              string              `json:"name"`
	Direction         string              `json:"direction"`
	ContainsText      string              `json:"contains_text"`
	AccountID         int64               `json:"account_id"`
	Active            bool                `json:"active"`
	AmountMinCents    *int64              `json:"amount_min_cents"`
	AmountMaxCents    *int64              `json:"amount_max_cents"`
	ReferenceContains *string             `json:"reference_contains"`
	Priority          int                 `json:"priority"`
	ConditionMatch    string              `json:"condition_match"`
	ContactID         *int64              `json:"contact_id"`
	TaxRateCode       *string             `json:"tax_rate_code"`
	Tracking          map[string]int64    `json:"tracking"`
	Conditions        []bankRuleCondition `json:"conditions"`
}

// bankRuleSuggestion is what a matching rule offers for one line. It is a proposal, not a posting.
type bankRuleSuggestion struct {
	RuleID      int64            `json:"rule_id"`
	Name        string           `json:"name"`
	Direction   string           `json:"direction"`
	AccountID   int64            `json:"account_id"`
	ContactID   *int64           `json:"contact_id"`
	TaxRateCode *string          `json:"tax_rate_code"`
	Tracking    map[string]int64 `json:"tracking"`
}

type bankRuleInput struct {
	OrgID             int64               `json:"org_id"`
	Name              string              `json:"name"`
	Direction         string              `json:"direction"`
	ContainsText      string              `json:"contains_text"`
	AccountID         int64               `json:"account_id"`
	AmountMinCents    *int64              `json:"amount_min_cents"`
	AmountMaxCents    *int64              `json:"amount_max_cents"`
	ReferenceContains *string             `json:"reference_contains"`
	Priority          *int                `json:"priority"`
	ConditionMatch    string              `json:"condition_match"`
	ContactID         *int64              `json:"contact_id"`
	TaxRateCode       *string             `json:"tax_rate_code"`
	Tracking          map[string]int64    `json:"tracking"`
	Conditions        []bankRuleCondition `json:"conditions"`
}

// validBankRule trims text and checks every optional condition. Priority defaults to 100 so an
// older client that never sends one keeps the previous "lowest id first" behaviour.
func validBankRule(in *bankRuleInput) bool {
	in.Name = strings.TrimSpace(in.Name)
	in.ContainsText = strings.TrimSpace(in.ContainsText)
	in.ConditionMatch = strings.TrimSpace(in.ConditionMatch)
	if in.ConditionMatch == "" {
		in.ConditionMatch = "all"
	}
	if in.Priority == nil {
		d := 100
		in.Priority = &d
	} else if *in.Priority < 1 || *in.Priority > 999 {
		return false
	}
	if in.ReferenceContains != nil {
		v := strings.TrimSpace(*in.ReferenceContains)
		if v == "" {
			in.ReferenceContains = nil
		} else if !validTaxText(v, 100) {
			return false
		} else {
			in.ReferenceContains = &v
		}
	}
	if in.TaxRateCode != nil {
		v := strings.ToUpper(strings.TrimSpace(*in.TaxRateCode))
		if v == "" {
			in.TaxRateCode = nil
		} else {
			in.TaxRateCode = &v
		}
	}
	if in.Name == "" || in.ContainsText == "" || !validTaxText(in.Name, 100) || !validTaxText(in.ContainsText, 100) ||
		!validStage(in.Direction, "receive", "spend", "transfer") || in.AccountID <= 0 ||
		!validStage(in.ConditionMatch, "all", "any") {
		return false
	}
	if in.AmountMinCents != nil && *in.AmountMinCents < 0 || in.AmountMaxCents != nil && *in.AmountMaxCents < 0 {
		return false
	}
	if in.AmountMinCents != nil && in.AmountMaxCents != nil && *in.AmountMinCents > *in.AmountMaxCents {
		return false
	}
	if in.ContactID != nil && *in.ContactID <= 0 {
		return false
	}
	if len(in.Conditions) > 10 || len(in.Tracking) > 2 {
		return false
	}
	out := make([]bankRuleCondition, 0, len(in.Conditions))
	for _, c := range in.Conditions {
		c.Value = strings.TrimSpace(c.Value)
		if c.Value == "" {
			continue // a blank row in the editor is not a condition
		}
		if !validStage(c.Field, "description", "reference") || !validTaxText(c.Value, 100) {
			return false
		}
		out = append(out, c)
	}
	in.Conditions = out
	return true
}

// validateRuleActions checks the database-backed choices: the account belongs to the company (and is a
// bank account when the rule suggests a transfer), the contact belongs to the company, the tax code
// exists in the catalogue and every tracking option is usable.
func (a *app) validateRuleActions(ctx context.Context, q rowQuerier, orgID int64, in *bankRuleInput) (string, error) {
	var isBank bool
	if err := q.QueryRow(ctx, `SELECT COALESCE(is_bank_account,false) FROM accounts WHERE org_id=$1 AND id=$2`, orgID, in.AccountID).Scan(&isBank); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "invalid_account", nil
		}
		return "", err
	}
	if in.Direction == "transfer" && !isBank {
		return "invalid_bank_account", nil
	}
	if in.ContactID != nil {
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contacts WHERE org_id=$1 AND id=$2)`, orgID, *in.ContactID).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return "invalid_contact", nil
		}
	}
	if in.TaxRateCode != nil {
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tax_rates WHERE code=$1 AND is_active)`, *in.TaxRateCode).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return "invalid_tax_rate", nil
		}
	}
	ok, err := validTracking(ctx, q, orgID, in.Tracking)
	if err != nil {
		return "", err
	}
	if !ok {
		return "invalid_tracking", nil
	}
	return "", nil
}

func (a *app) createBankRule(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in bankRuleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validBankRule(&in) {
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
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if code, err := a.validateRuleActions(ctx, tx, access.ID, &in); err != nil {
		serverError(w, err)
		return
	} else if code != "" {
		writeError(w, http.StatusBadRequest, code)
		return
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO bank_rules (org_id,name,direction,contains_text,account_id,
		amount_min_cents,amount_max_cents,reference_contains,priority,condition_match,contact_id,tax_rate_code,tracking,created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::jsonb,$14) RETURNING id`,
		access.ID, in.Name, in.Direction, in.ContainsText, in.AccountID, in.AmountMinCents, in.AmountMaxCents,
		in.ReferenceContains, *in.Priority, in.ConditionMatch, in.ContactID, in.TaxRateCode,
		trackingText(in.Tracking), u.ID).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	if err := saveRuleConditions(ctx, tx, access.ID, id, in.Conditions); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// updateBankRule edits a rule in place. The org check happens before the row check, so another
// company's rule can never be touched or probed.
func (a *app) updateBankRule(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	var in bankRuleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validBankRule(&in) {
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
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if code, err := a.validateRuleActions(ctx, tx, access.ID, &in); err != nil {
		serverError(w, err)
		return
	} else if code != "" {
		writeError(w, http.StatusBadRequest, code)
		return
	}
	tag, err := tx.Exec(ctx, `UPDATE bank_rules SET name=$3,direction=$4,contains_text=$5,account_id=$6,
		amount_min_cents=$7,amount_max_cents=$8,reference_contains=$9,priority=$10,condition_match=$11,
		contact_id=$12,tax_rate_code=$13,tracking=$14::jsonb WHERE org_id=$1 AND id=$2`,
		access.ID, id, in.Name, in.Direction, in.ContainsText, in.AccountID, in.AmountMinCents, in.AmountMaxCents,
		in.ReferenceContains, *in.Priority, in.ConditionMatch, in.ContactID, in.TaxRateCode, trackingText(in.Tracking))
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err := saveRuleConditions(ctx, tx, access.ID, id, in.Conditions); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (a *app) deleteBankRule(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, true)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" && access.Role != "accountant" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	tag, err := a.db.Exec(r.Context(), `DELETE FROM bank_rules WHERE org_id=$1 AND id=$2`, access.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) listBankRules(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rules, err := a.loadBankRules(r.Context(), access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

// loadBankRules returns every rule for a company in priority order, with its conditions and tracking.
func (a *app) loadBankRules(ctx context.Context, orgID int64) ([]bankRule, error) {
	rows, err := a.db.Query(ctx, `SELECT r.id,r.name,r.direction,r.contains_text,r.account_id,r.active,
		r.amount_min_cents,r.amount_max_cents,r.reference_contains,r.priority,r.condition_match,
		r.contact_id,r.tax_rate_code,r.tracking::text,
		COALESCE((SELECT json_agg(json_build_object('field',c.field,'value',c.value) ORDER BY c.id)
			FROM bank_rule_conditions c WHERE c.org_id=r.org_id AND c.rule_id=r.id), '[]'::json)
		FROM bank_rules r WHERE r.org_id=$1 ORDER BY r.priority, r.id LIMIT 500`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []bankRule{}
	for rows.Next() {
		var x bankRule
		var conds []byte
		var tracking string
		if err := rows.Scan(&x.ID, &x.Name, &x.Direction, &x.ContainsText, &x.AccountID, &x.Active,
			&x.AmountMinCents, &x.AmountMaxCents, &x.ReferenceContains, &x.Priority, &x.ConditionMatch,
			&x.ContactID, &x.TaxRateCode, &tracking, &conds); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(conds, &x.Conditions); err != nil {
			return nil, err
		}
		if x.Conditions == nil {
			x.Conditions = []bankRuleCondition{}
		}
		if err := json.Unmarshal([]byte(tracking), &x.Tracking); err != nil {
			return nil, err
		}
		if x.Tracking == nil {
			x.Tracking = map[string]int64{}
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (a *app) setBankRuleActive(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID  int64 `json:"org_id"`
		ID     int64 `json:"id"`
		Active bool  `json:"active"`
	}
	if !decodeJSON(w, r, &in) || in.ID <= 0 {
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
	tag, err := a.db.Exec(r.Context(), `UPDATE bank_rules SET active=$3 WHERE org_id=$1 AND id=$2`, access.ID, in.ID, in.Active)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": in.ID, "active": in.Active})
}

// trackingText stores a rule's suggested tracking values; empty stores {}.
func trackingText(tracking map[string]int64) string {
	if len(tracking) == 0 {
		return "{}"
	}
	raw, _ := json.Marshal(tracking)
	return string(raw)
}

// matchBankRule returns the first active rule (in priority order) whose conditions the line meets.
func matchBankRule(rules []bankRule, line bankLine) *bankRuleSuggestion {
	for _, r := range rules {
		if !r.Active || !bankRuleDirection(r.Direction, line.AmountCents) {
			continue
		}
		if r.AmountMinCents != nil && absInt64(line.AmountCents) < *r.AmountMinCents {
			continue
		}
		if r.AmountMaxCents != nil && absInt64(line.AmountCents) > *r.AmountMaxCents {
			continue
		}
		if r.ReferenceContains != nil && !strings.Contains(strings.ToLower(line.Reference), strings.ToLower(*r.ReferenceContains)) {
			continue
		}
		// The rule text and the extra conditions are tests on the line; all/any groups them.
		tests := []bool{strings.Contains(strings.ToLower(line.Description), strings.ToLower(r.ContainsText))}
		for _, c := range r.Conditions {
			text := line.Description
			if c.Field == "reference" {
				text = line.Reference
			}
			tests = append(tests, strings.Contains(strings.ToLower(text), strings.ToLower(c.Value)))
		}
		matched := false
		for _, t := range tests {
			if t {
				matched = true
			} else if r.ConditionMatch == "all" {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		return &bankRuleSuggestion{
			RuleID: r.ID, Name: r.Name, Direction: r.Direction, AccountID: r.AccountID,
			ContactID: r.ContactID, TaxRateCode: r.TaxRateCode, Tracking: r.Tracking,
		}
	}
	return nil
}

// bankRuleDirection: receive is money in, spend is money out, transfer can be either side of a
// movement between two accounts.
func bankRuleDirection(direction string, amount int64) bool {
	switch direction {
	case "receive":
		return amount > 0
	case "spend":
		return amount < 0
	case "transfer":
		return amount != 0
	}
	return false
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// saveRuleConditions replaces a rule's extra conditions with exactly the ones supplied.
func saveRuleConditions(ctx context.Context, tx pgx.Tx, orgID, ruleID int64, conditions []bankRuleCondition) error {
	if _, err := tx.Exec(ctx, `DELETE FROM bank_rule_conditions WHERE org_id=$1 AND rule_id=$2`, orgID, ruleID); err != nil {
		return err
	}
	for _, c := range conditions {
		if _, err := tx.Exec(ctx, `INSERT INTO bank_rule_conditions (org_id, rule_id, field, value) VALUES ($1,$2,$3,$4)`, orgID, ruleID, c.Field, c.Value); err != nil {
			return err
		}
	}
	return nil
}
