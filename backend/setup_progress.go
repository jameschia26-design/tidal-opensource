package main

import "net/http"

// setupProgress: GET /api/v1/setup-progress?org_id
// Each step answers a question the books can answer, so the guide never asks the person to tick a box
// they already did. Reporting is not a stored action, so the last step reflects posted entries instead.
func (a *app) setupProgress(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	ctx := r.Context()
	var company, accounts, contacts, items, invoices, payments, bills, journals bool
	err := a.db.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM organizations WHERE id=$1 AND name<>'' AND (uen<>'' OR contact_email<>'')),
		EXISTS(SELECT 1 FROM accounts WHERE org_id=$1),
		EXISTS(SELECT 1 FROM contacts WHERE org_id=$1),
		EXISTS(SELECT 1 FROM items WHERE org_id=$1),
		EXISTS(SELECT 1 FROM invoices WHERE org_id=$1 AND status='posted'),
		EXISTS(SELECT 1 FROM payments WHERE org_id=$1),
		EXISTS(SELECT 1 FROM bills WHERE org_id=$1 AND status='posted'),
		EXISTS(SELECT 1 FROM journals WHERE org_id=$1)`, access.ID).
		Scan(&company, &accounts, &contacts, &items, &invoices, &payments, &bills, &journals)
	if err != nil {
		serverError(w, err)
		return
	}
	steps := []struct {
		Key  string `json:"key"`
		Done bool   `json:"done"`
	}{{"company", company}, {"accounts", accounts}, {"contacts", contacts}, {"items", items}, {"invoice", invoices}, {"payment", payments}, {"bill", bills}, {"reports", journals}}
	done := 0
	for _, step := range steps {
		if step.Done {
			done++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"done": done, "total": len(steps), "steps": steps})
}
