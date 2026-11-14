package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// validSocialURL applies the same rule as contact websites: optional, but when set it must
// be an absolute http(s) URL, so a profile link can never become a javascript: link.
func validSocialURL(s string) bool {
	if s == "" {
		return true
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil
}

type companyProfile struct {
	LegalName    string         `json:"legal_name"`
	BusinessType string         `json:"business_type"`
	Industry     string         `json:"industry"`
	ContactEmail string         `json:"contact_email"`
	Phone        string         `json:"phone"`
	Website      string         `json:"website"`
	GSTNumber    string         `json:"gst_number"`
	Address      contactAddress `json:"address"`
	Description  string         `json:"description"`
	Mobile       string         `json:"mobile"`
	Fax          string         `json:"fax"`
	Facebook     string         `json:"facebook"`
	Twitter      string         `json:"twitter"`
	Linkedin     string         `json:"linkedin"`
	Physical     contactAddress `json:"physical_address"`
}

func validCompany(in *orgInput) bool {
	in.Name = strings.TrimSpace(in.Name)
	in.UEN = strings.ToUpper(strings.TrimSpace(in.UEN))
	in.ContactEmail = strings.ToLower(strings.TrimSpace(in.ContactEmail))
	if in.Name == "" || !utf8.ValidString(in.Name) || strings.ContainsRune(in.Name, 0) || utf8.RuneCountInString(in.Name) > 200 || !validFiscalYearEnd(in.FiscalYearEnd) {
		return false
	}
	switch in.BusinessType {
	case "", "company", "sole_proprietor", "partnership", "llp", "other":
	default:
		return false
	}
	if !utf8.ValidString(in.Industry) || strings.ContainsRune(in.Industry, 0) || utf8.RuneCountInString(in.Industry) > 100 {
		return false
	}
	if in.ContactEmail != "" {
		if _, ok := normalizeEmail(in.ContactEmail); !ok {
			return false
		}
	}
	// Same rules as a contact: bounded text, valid website/email, optional country/postal codes.
	profile := contactInput{Role: "customer", contactProfile: contactProfile{CompanyName: in.LegalName, Phone: in.Phone, Mobile: in.Mobile,
		Website: in.Website, UEN: in.UEN, GSTNumber: in.GSTNumber, BillingAddress: in.Address, ShippingAddress: in.Physical}}
	if !validContactProfile(&profile) {
		return false
	}
	if !validTaxText(in.Description, 500) || !validTaxText(in.Fax, 40) {
		return false
	}
	for _, s := range []string{in.Facebook, in.Twitter, in.Linkedin} {
		if utf8.RuneCountInString(s) > 300 || !validSocialURL(s) {
			return false
		}
	}
	return true
}

func (a *app) getCompany(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := parseOrgID(w, r.PathValue("id"))
	if !ok {
		return
	}
	access, status, code, err := a.orgAccessFor(r.Context(), u.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	if code != "" {
		writeError(w, status, code)
		return
	}
	var company struct {
		ID int64 `json:"id"`
		orgInput
	}
	err = a.db.QueryRow(r.Context(), `SELECT id,name,uen,fiscal_year_end,gst_registered,legal_name,business_type,industry,contact_email,phone,website,gst_number,address,
		description,mobile,fax,facebook,twitter,linkedin,physical_address,
		COALESCE((SELECT effective_from::text FROM gst_registrations WHERE org_id=organizations.id AND effective_to IS NULL ORDER BY effective_from DESC LIMIT 1),'')
		FROM organizations WHERE id=$1`, access.ID).Scan(&company.ID, &company.Name, &company.UEN, &company.FiscalYearEnd, &company.GSTRegistered, &company.LegalName, &company.BusinessType,
		&company.Industry, &company.ContactEmail, &company.Phone, &company.Website, &company.GSTNumber, &company.Address,
		&company.Description, &company.Mobile, &company.Fax, &company.Facebook, &company.Twitter, &company.Linkedin, &company.Physical, &company.GSTEffectiveFrom)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, company)
}

func (a *app) updateCompany(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := parseOrgID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var in orgInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validCompany(&in) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if in.BankMatchToleranceDays < 0 || in.BankMatchToleranceDays > 31 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if in.GSTRegistered && in.GSTEffectiveFrom != "" {
		if _, ok := parseDay(in.GSTEffectiveFrom); !ok {
			writeError(w, 400, "invalid_input")
			return
		}
	}
	ctx := r.Context()
	access, status, code, err := a.orgAccessFor(ctx, u.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	if code != "" {
		writeError(w, status, code)
		return
	}
	if access.Role != "owner" && access.Role != "admin" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var gst, historyMissing bool
	var fye string
	var registrationStart string
	if err := tx.QueryRow(ctx, `SELECT gst_registered,gst_registration_history_missing,fiscal_year_end,COALESCE((SELECT effective_from::text FROM gst_registrations WHERE org_id=organizations.id AND effective_to IS NULL ORDER BY effective_from DESC LIMIT 1),'') FROM organizations WHERE id=$1 FOR UPDATE`, access.ID).Scan(&gst, &historyMissing, &fye, &registrationStart); err != nil {
		serverError(w, err)
		return
	}
	// Year end remains fixed; GST registration changes are append-only dated events.
	if fye != in.FiscalYearEnd {
		writeError(w, http.StatusConflict, "accounting_settings_locked")
		return
	}
	if gst && in.GSTRegistered && in.GSTEffectiveFrom != "" && registrationStart != "" && in.GSTEffectiveFrom != registrationStart {
		writeError(w, http.StatusConflict, "gst_history_locked")
		return
	}
	if gst != in.GSTRegistered {
		if historyMissing || (gst && registrationStart == "") {
			writeError(w, http.StatusConflict, "gst_history_unavailable")
			return
		}
		if !gst && in.GSTEffectiveFrom != "" {
			writeError(w, 400, "invalid_input")
			return
		}
		if gst && registrationStart != "" {
			endDate := in.GSTEffectiveFrom
			if endDate == "" {
				endDate = time.Now().UTC().Format("2006-01-02")
			}
			end, ok := parseDay(endDate)
			if !ok || endDate <= registrationStart {
				writeError(w, 400, "invalid_input")
				return
			}
			if _, err := tx.Exec(ctx, `UPDATE gst_registrations SET effective_to=$2::date-1 WHERE org_id=$1 AND effective_to IS NULL`, access.ID, end); err != nil {
				serverError(w, err)
				return
			}
		}
		if in.GSTRegistered {
			start, ok := parseDay(in.GSTEffectiveFrom)
			if !ok {
				writeError(w, 400, "invalid_input")
				return
			}
			if _, err := tx.Exec(ctx, `INSERT INTO gst_registrations(org_id,effective_from,registration_number,created_by) VALUES($1,$2,$3,$4)`, access.ID, start, in.GSTNumber, u.ID); err != nil {
				serverError(w, err)
				return
			}
		}
	}
	address, _ := json.Marshal(in.Address)
	physical, _ := json.Marshal(in.Physical)
	_, err = tx.Exec(ctx, `UPDATE organizations SET bank_match_tolerance_days=$20,gst_registered=$19,name=$2,uen=$3,legal_name=$4,business_type=$5,industry=$6,contact_email=$7,phone=$8,website=$9,gst_number=$10,
		address=$11,description=$12,mobile=$13,fax=$14,facebook=$15,twitter=$16,linkedin=$17,physical_address=$18,updated_at=now() WHERE id=$1`,
		access.ID, in.Name, in.UEN, in.LegalName, in.BusinessType, in.Industry, in.ContactEmail, in.Phone, in.Website, in.GSTNumber,
		address, in.Description, in.Mobile, in.Fax, in.Facebook, in.Twitter, in.Linkedin, physical, in.GSTRegistered, in.BankMatchToleranceDays)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,entity_kind,entity_id,detail)
		VALUES($1,$2,'organization.updated','organization',$1,jsonb_build_object(
			'name',$3::text,'uen',$4::text,'legal_name',$5::text,'business_type',$6::text,
			'industry',$7::text,'contact_email',$8::text,'phone',$9::text,'website',$10::text,
			'gst_number',$11::text,'gst_registered',$12::boolean,'bank_match_tolerance_days',$13::int))`,
		access.ID, u.ID, in.Name, in.UEN, in.LegalName, in.BusinessType, in.Industry, in.ContactEmail,
		in.Phone, in.Website, in.GSTNumber, in.GSTRegistered, in.BankMatchToleranceDays); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": access.ID, "name": in.Name})
}
