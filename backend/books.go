package main

import (
	"encoding/json"
	"net/http"
)

// Books share company membership, not balances, contacts or documents.
func (a *app) createBook(w http.ResponseWriter, r *http.Request, u currentUser) {
	companyID, ok := parseOrgID(w, r.PathValue("id"))
	if !ok || companyID == 0 {
		return
	}
	var in orgInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validCompany(&in) || (in.GSTRegistered && in.GSTEffectiveFrom == "") || (!in.GSTRegistered && in.GSTEffectiveFrom != "") {
		writeError(w, 400, "invalid_input")
		return
	}
	if in.GSTRegistered {
		if _, ok := parseDay(in.GSTEffectiveFrom); !ok {
			writeError(w, 400, "invalid_input")
			return
		}
	}
	access, ok := a.orgFor(w, r, u.ID, companyID, true)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" {
		writeError(w, 403, "forbidden")
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var owner int64
	var isBook bool
	if err := tx.QueryRow(ctx, `SELECT owner_user_id,parent_company_id IS NOT NULL FROM organizations WHERE id=$1 AND status='active' FOR UPDATE`, companyID).Scan(&owner, &isBook); err != nil {
		serverError(w, err)
		return
	}
	if isBook {
		writeError(w, 400, "company_required")
		return
	}
	address, _ := json.Marshal(in.Address)
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO organizations (name,uen,owner_user_id,fiscal_year_end,gst_registered,legal_name,business_type,industry,contact_email,phone,website,gst_number,address,parent_company_id)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id`, in.Name, in.UEN, owner, in.FiscalYearEnd, in.GSTRegistered, in.LegalName, in.BusinessType, in.Industry, in.ContactEmail, in.Phone, in.Website, in.GSTNumber, address, companyID).Scan(&id)
	if err != nil {
		serverError(w, err)
		return
	}
	if in.GSTRegistered {
		if _, err := tx.Exec(ctx, `INSERT INTO gst_registrations(org_id,effective_from,registration_number,created_by) VALUES($1,$2,$3,$4)`, id, in.GSTEffectiveFrom, in.GSTNumber, u.ID); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,'book.created')`, id, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "name": in.Name, "role": access.Role, "company_id": companyID, "is_book": true})
}
