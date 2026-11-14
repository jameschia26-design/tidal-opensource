package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const (
	maxNameLen  = 200
	maxContacts = 500
)

var (
	uenRe     = regexp.MustCompile(`^[A-Z0-9]{8,10}$`)
	fyeRe     = regexp.MustCompile(`^(0[1-9]|1[0-2])-(0[1-9]|[12][0-9]|3[01])$`)
	countryRe = regexp.MustCompile(`^[A-Z]{2}$`)
	postalRe  = regexp.MustCompile(`^[0-9]{6}$`)

	// writerRoles may create records in an organization. viewers may only read.
	writerRoles = map[string]bool{"owner": true, "admin": true, "accountant": true, "member": true}
)

type orgInput struct {
	Name             string `json:"name"`
	UEN              string `json:"uen"`
	FiscalYearEnd    string `json:"fiscal_year_end"`
	GSTRegistered    bool   `json:"gst_registered"`
	GSTEffectiveFrom string `json:"gst_effective_from"`
	// BankMatchToleranceDays allows a statement line to match a document dated up to this many days away.
	BankMatchToleranceDays int `json:"bank_match_tolerance_days"`
	companyProfile
}

type contactAddress struct {
	Attention  string `json:"attention"`
	Line1      string `json:"line1"`
	Line2      string `json:"line2"`
	City       string `json:"city"`
	Region     string `json:"region"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
	Phone      string `json:"phone"`
	Fax        string `json:"fax"`
}

type contactProfile struct {
	ContactType       string          `json:"contact_type"`
	CompanyName       string          `json:"company_name"`
	FirstName         string          `json:"first_name"`
	LastName          string          `json:"last_name"`
	Phone             string          `json:"phone"`
	Mobile            string          `json:"mobile"`
	Website           string          `json:"website"`
	UEN               string          `json:"uen"`
	GSTNumber         string          `json:"gst_number"`
	AccountNumber     string          `json:"account_number"`
	PaymentTermsDays  int             `json:"payment_terms_days"`
	BillingAddress    contactAddress  `json:"billing_address"`
	ShippingAddress   contactAddress  `json:"shipping_address"`
	Notes             string          `json:"notes"`
	Salutation        string          `json:"salutation"`
	Language          string          `json:"language"`
	Currency          string          `json:"currency"`
	PortalEnabled     bool            `json:"portal_enabled"`
	OwnerUserID       int64           `json:"owner_user_id"`
	OpeningBalance    int64           `json:"opening_balance_cents"`
	OpeningDate       string          `json:"opening_balance_date"`
	InternalRemarks   string          `json:"internal_remarks"`
	Fax               string          `json:"fax"`
	DirectDial        string          `json:"direct_dial"`
	Country           string          `json:"country"`
	BankAccountName   string          `json:"bank_account_name"`
	BankAccountNumber string          `json:"bank_account_number"`
	BankParticulars   string          `json:"bank_particulars"`
	SalesAccountID    int64           `json:"sales_account_id"`
	PurchaseAccountID int64           `json:"purchase_account_id"`
	CreditLimitCents  int64           `json:"credit_limit_cents"`
	BlockOverLimit    bool            `json:"block_over_limit"`
	DiscountBP        int             `json:"discount_basis_points"`
	BillTermsDays     *int            `json:"bill_payment_terms_days"`
	SalesTreatment    string          `json:"sales_treatment"`
	SalesTaxCode      string          `json:"sales_tax_code"`
	PurchaseTreatment string          `json:"purchase_treatment"`
	PurchaseTaxCode   string          `json:"purchase_tax_code"`
	Archived          bool            `json:"archived"`
	Persons           []contactPerson `json:"contact_persons"`
}

func validTaxCodeShape(code string) bool {
	if len(code) > 20 {
		return false
	}
	for _, r := range code {
		if !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' {
			return false
		}
	}
	return true
}

type contactPerson struct {
	Salutation string `json:"salutation"`
	FirstName  string `json:"first_name"`
	LastName   string `json:"last_name"`
	Email      string `json:"email"`
	WorkPhone  string `json:"work_phone"`
	Mobile     string `json:"mobile"`
}

type contactInput struct {
	OrgID int64  `json:"org_id"`
	Role  string `json:"role"`
	Name  string `json:"name"`
	Email string `json:"email"`
	contactProfile
}

func validContactProfile(in *contactInput) bool {
	if in.Role != "customer" && in.Role != "supplier" && in.Role != "both" {
		return false
	}
	if in.ContactType == "" {
		in.ContactType = "business"
	}
	if in.ContactType != "business" && in.ContactType != "individual" {
		return false
	}
	in.UEN = strings.ToUpper(strings.TrimSpace(in.UEN))
	if in.UEN != "" && !uenRe.MatchString(in.UEN) {
		return false
	}
	if in.PaymentTermsDays < 0 || in.PaymentTermsDays > 365 {
		return false
	}
	if in.Language == "" {
		in.Language = "en"
	}
	if !map[string]bool{"en": true, "zh": true, "ms": true, "ta": true}[in.Language] {
		return false
	}
	// Opening balance is what the other party owes on the start date. Zero means none.
	if in.OpeningBalance < 0 || in.OpeningBalance > 100000000000000 {
		return false
	}
	if in.Currency == "" {
		in.Currency = "SGD"
	}
	if in.Currency != "SGD" || in.OwnerUserID < 0 {
		return false
	}
	if in.SalesTreatment != "" && in.SalesTreatment != "exclusive" && in.SalesTreatment != "inclusive" {
		return false
	}
	if in.PurchaseTreatment != "" && in.PurchaseTreatment != "exclusive" && in.PurchaseTreatment != "inclusive" {
		return false
	}
	in.SalesTaxCode = strings.ToUpper(strings.TrimSpace(in.SalesTaxCode))
	in.PurchaseTaxCode = strings.ToUpper(strings.TrimSpace(in.PurchaseTaxCode))
	if !validTaxCodeShape(in.SalesTaxCode) || !validTaxCodeShape(in.PurchaseTaxCode) {
		return false
	}
	if in.OpeningDate != "" {
		if _, ok := parseDay(in.OpeningDate); !ok {
			return false
		}
	} else if in.OpeningBalance > 0 {
		return false
	}
	// A credit limit is a money ceiling; blocking only makes sense with a limit set.
	if in.CreditLimitCents < 0 || in.CreditLimitCents > 100000000000000 || in.SalesAccountID < 0 || in.PurchaseAccountID < 0 {
		return false
	}
	if in.BlockOverLimit && in.CreditLimitCents == 0 {
		return false
	}
	// Xero's contact discount is a sales default; it is stored as basis points like line discounts.
	if in.DiscountBP < 0 || in.DiscountBP > 10000 {
		return false
	}
	if in.BillTermsDays != nil && (*in.BillTermsDays < 0 || *in.BillTermsDays > 365) {
		return false
	}
	if len(in.Persons) > 20 {
		return false
	}
	for _, p := range in.Persons {
		if p.Email != "" {
			if _, ok := normalizeEmail(p.Email); !ok {
				return false
			}
		}
		for _, field := range []struct {
			value string
			limit int
		}{{p.Salutation, 20}, {p.FirstName, 100}, {p.LastName, 100}, {p.Email, 254}, {p.WorkPhone, 40}, {p.Mobile, 40}} {
			if !utf8.ValidString(field.value) || utf8.RuneCountInString(field.value) > field.limit || strings.ContainsRune(field.value, 0) {
				return false
			}
		}
	}
	for _, field := range []struct {
		value string
		limit int
	}{
		{in.CompanyName, 200}, {in.FirstName, 100}, {in.LastName, 100}, {in.Phone, 40},
		{in.Mobile, 40}, {in.Website, 300}, {in.GSTNumber, 30}, {in.AccountNumber, 50}, {in.Notes, 4000},
		{in.Salutation, 20}, {in.InternalRemarks, 4000}, {in.Fax, 40}, {in.DirectDial, 40},
		{in.Country, 60}, {in.BankAccountName, 100}, {in.BankAccountNumber, 60}, {in.BankParticulars, 100},
	} {
		if !utf8.ValidString(field.value) || utf8.RuneCountInString(field.value) > field.limit || strings.ContainsRune(field.value, 0) {
			return false
		}
	}
	if in.Website != "" {
		u, err := url.Parse(in.Website)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
			return false
		}
	}
	for _, address := range []contactAddress{in.BillingAddress, in.ShippingAddress} {
		if !validAddress(address) {
			return false
		}
	}
	return true
}

// validAddress checks one address block (billing, shipping or an additional address).
func validAddress(address contactAddress) bool {
	for _, value := range []string{address.Attention, address.Line1, address.Line2, address.City, address.Region} {
		if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 200 || strings.ContainsRune(value, 0) {
			return false
		}
	}
	for _, phone := range []string{address.Phone, address.Fax} {
		if !utf8.ValidString(phone) || utf8.RuneCountInString(phone) > 40 || strings.ContainsRune(phone, 0) {
			return false
		}
	}
	if len(address.Country) > 2 || utf8.RuneCountInString(address.PostalCode) > 20 || !utf8.ValidString(address.PostalCode) || strings.ContainsRune(address.PostalCode, 0) {
		return false
	}
	if address.Country != "" && !countryRe.MatchString(address.Country) {
		return false
	}
	if address.Country == "SG" && address.PostalCode != "" && !postalRe.MatchString(address.PostalCode) {
		return false
	}
	return true
}

// validFiscalYearEnd accepts MM-DD and rejects impossible dates such as 02-30.
func validFiscalYearEnd(s string) bool {
	if !fyeRe.MatchString(s) {
		return false
	}
	_, err := time.Parse("2006-01-02", "2000-"+s) // 2000 is a leap year, so 02-29 is accepted
	return err == nil
}

// createOrg enforces the one-company limit for standard tiers. The owner row lock serializes
// concurrent creates by one account, so two requests cannot both pass the count check.
func (a *app) createOrg(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in orgInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validCompany(&in) || (in.GSTRegistered && in.GSTEffectiveFrom == "") || (!in.GSTRegistered && in.GSTEffectiveFrom != "") {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if in.GSTRegistered {
		if _, ok := parseDay(in.GSTEffectiveFrom); !ok {
			writeError(w, 400, "invalid_input")
			return
		}
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)

	var tier string
	if err := tx.QueryRow(ctx, `SELECT tier FROM users WHERE id=$1 FOR UPDATE`, u.ID).Scan(&tier); err != nil {
		serverError(w, err)
		return
	}
	if tier != "premier" {
		var owned int
		// Counts archived companies too, so archiving cannot be used to create a second one.
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM organizations WHERE owner_user_id=$1 AND parent_company_id IS NULL AND NOT is_shared_sample`, u.ID).Scan(&owned); err != nil {
			serverError(w, err)
			return
		}
		if owned > 0 {
			writeError(w, http.StatusConflict, "organization_limit")
			return
		}
	}
	gstStart := time.Now().UTC().Format("2006-01-02")
	if in.GSTRegistered {
		if in.GSTEffectiveFrom != "" {
			if _, ok := parseDay(in.GSTEffectiveFrom); !ok {
				writeError(w, 400, "invalid_input")
				return
			}
			gstStart = in.GSTEffectiveFrom
		}
	} else if in.GSTEffectiveFrom != "" {
		writeError(w, 400, "invalid_input")
		return
	}
	address, _ := json.Marshal(in.Address)
	physical, _ := json.Marshal(in.Physical)
	var id int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO organizations (name, uen, owner_user_id, fiscal_year_end, gst_registered,
		 legal_name,business_type,industry,contact_email,phone,website,gst_number,address,
		 description,mobile,fax,facebook,twitter,linkedin,physical_address)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20) RETURNING id`,
		in.Name, in.UEN, u.ID, in.FiscalYearEnd, in.GSTRegistered, in.LegalName, in.BusinessType,
		in.Industry, in.ContactEmail, in.Phone, in.Website, in.GSTNumber, address,
		in.Description, in.Mobile, in.Fax, in.Facebook, in.Twitter, in.Linkedin, physical).Scan(&id); err != nil {
		serverError(w, err)
		return
	}
	if in.GSTRegistered {
		if _, err := tx.Exec(ctx, `INSERT INTO gst_registrations(org_id,effective_from,registration_number,created_by) VALUES($1,$2,$3,$4)`, id, gstStart, in.GSTNumber, u.ID); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, 'owner')`, id, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (org_id, user_id, action) VALUES ($1, $2, 'organization.created')`, id, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": in.Name, "role": "owner"})
}

func (a *app) listOrgs(w http.ResponseWriter, r *http.Request, u currentUser) {
	type org struct {
		ID             int64  `json:"id"`
		Name           string `json:"name"`
		Role           string `json:"role"`
		CompanyID      int64  `json:"company_id"`
		CompanyName    string `json:"company_name"`
		IsBook         bool   `json:"is_book"`
		IsSharedSample bool   `json:"is_shared_sample"`
		FiscalYearEnd  string `json:"fiscal_year_end"`
	}
	rows, err := a.db.Query(r.Context(),
		`SELECT o.id, o.name, CASE WHEN COALESCE(p.is_shared_sample,o.is_shared_sample) THEN 'viewer' ELSE m.role END, COALESCE(o.parent_company_id,o.id), COALESCE(p.name,o.name), o.parent_company_id IS NOT NULL, COALESCE(p.is_shared_sample,o.is_shared_sample), o.fiscal_year_end
		 FROM organizations o LEFT JOIN organizations p ON p.id=o.parent_company_id
		 LEFT JOIN memberships m ON m.org_id=COALESCE(o.parent_company_id,o.id) AND m.user_id=$1
		 WHERE (m.user_id IS NOT NULL OR COALESCE(p.is_shared_sample,o.is_shared_sample)) AND o.status='active' AND COALESCE(p.status,'active')='active' ORDER BY COALESCE(o.parent_company_id,o.id),o.id`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []org{}
	for rows.Next() {
		var o org
		if err := rows.Scan(&o.ID, &o.Name, &o.Role, &o.CompanyID, &o.CompanyName, &o.IsBook, &o.IsSharedSample, &o.FiscalYearEnd); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"organizations": out})
}

type orgAccess struct {
	ID   int64
	Role string
}

// orgAccessFor returns the caller's membership for one organization. orgID 0 means "not given":
// Prefer non-sample memberships; fall back to the shared sample only if none exist.
func (a *app) orgAccessFor(ctx context.Context, userID, orgID int64) (orgAccess, int, string, error) {
	rows, err := a.db.Query(ctx,
		`SELECT o.id, CASE WHEN COALESCE(p.is_shared_sample,o.is_shared_sample) THEN 'viewer' ELSE m.role END FROM organizations o
		 LEFT JOIN organizations p ON p.id=o.parent_company_id
		 LEFT JOIN memberships m ON m.org_id=COALESCE(o.parent_company_id,o.id) AND m.user_id=$1
		 WHERE (m.user_id IS NOT NULL OR COALESCE(p.is_shared_sample,o.is_shared_sample)) AND o.status='active' AND COALESCE(p.status,'active')='active' AND ($2::bigint = 0 OR o.id = $2)
		 AND ($2::bigint <> 0 OR NOT COALESCE(p.is_shared_sample,o.is_shared_sample) OR NOT EXISTS (
		   SELECT 1 FROM organizations x LEFT JOIN organizations xp ON xp.id=x.parent_company_id
		   JOIN memberships xm ON xm.org_id=COALESCE(x.parent_company_id,x.id) AND xm.user_id=$1
		   WHERE x.status='active' AND COALESCE(xp.status,'active')='active' AND NOT COALESCE(xp.is_shared_sample,x.is_shared_sample)
		 ))
		 ORDER BY o.id LIMIT 2`, userID, orgID)
	if err != nil {
		return orgAccess{}, 0, "", err
	}
	defer rows.Close()
	var found []orgAccess
	for rows.Next() {
		var o orgAccess
		if err := rows.Scan(&o.ID, &o.Role); err != nil {
			return orgAccess{}, 0, "", err
		}
		found = append(found, o)
	}
	if err := rows.Err(); err != nil {
		return orgAccess{}, 0, "", err
	}
	switch len(found) {
	case 0:
		return orgAccess{}, http.StatusForbidden, "forbidden", nil
	case 1:
		return found[0], 0, "", nil
	default:
		return orgAccess{}, http.StatusBadRequest, "org_required", nil
	}
}

func parseOrgID(w http.ResponseWriter, raw string) (int64, bool) {
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return 0, false
	}
	return id, true
}

func (a *app) listContacts(w http.ResponseWriter, r *http.Request, u currentUser) {
	role := r.URL.Query().Get("role")
	if role != "" && role != "customer" && role != "supplier" { // blank = everyone; screens that pick either side filter by role themselves
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	ctx := r.Context()
	access, status, code, err := a.orgAccessFor(ctx, u.ID, orgID)
	if err != nil {
		serverError(w, err)
		return
	}
	if code != "" {
		writeError(w, status, code)
		return
	}
	// Archived contacts are hidden from every picker by default; the contacts screen asks for them.
	archived := r.URL.Query().Get("archived") == "1" || r.URL.Query().Get("archived") == "true"
	rows, err := a.db.Query(ctx,
		`SELECT c.id, c.name, c.email, c.kind, c.contact_type, c.company_name, c.first_name, c.last_name,
		 c.phone, c.mobile, c.website, c.uen, c.gst_number, c.account_number, c.payment_terms_days, c.billing_address, c.shipping_address, c.notes,
		 c.salutation, c.language, c.currency, c.portal_enabled, COALESCE(c.owner_user_id,0), c.opening_balance_cents,
		 COALESCE(to_char(c.opening_balance_date,'YYYY-MM-DD'),''), c.internal_remarks,
		 c.fax, c.direct_dial, c.country, c.bank_account_name, c.bank_account_number, c.bank_particulars,
		 COALESCE(c.sales_account_id,0), COALESCE(c.purchase_account_id,0), c.credit_limit_cents, c.block_over_limit,
		 c.archived, c.discount_basis_points, c.bill_payment_terms_days,
		 c.sales_treatment, c.sales_tax_code, c.purchase_treatment, c.purchase_tax_code,
		 COALESCE((SELECT SUM(x.total - COALESCE(x.paid,0) - COALESCE(x.credited,0)) FROM (
		   SELECT i.total_cents total,
		     (SELECT SUM(p.amount_cents) FROM payments p WHERE p.org_id=i.org_id AND p.invoice_id=i.id) paid,
		     (SELECT SUM(n.amount_cents) FROM credit_notes n WHERE n.org_id=i.org_id AND n.invoice_id=i.id)
		       + (SELECT SUM(cc.amount_cents) FROM customer_credit_applications cc WHERE cc.org_id=i.org_id AND cc.invoice_id=i.id) credited
		   FROM invoices i WHERE i.org_id=c.org_id AND i.contact_id=c.id AND i.status='posted') x),0) AS they_owe,
		 COALESCE((SELECT SUM(x.total - COALESCE(x.paid,0) - COALESCE(x.credited,0)) FROM (
		   SELECT b.total_cents total,
		     (SELECT SUM(bp.amount_cents) FROM bill_payments bp WHERE bp.org_id=b.org_id AND bp.bill_id=b.id) paid,
		     (SELECT SUM(sc.total_cents) FROM supplier_credits sc WHERE sc.org_id=b.org_id AND sc.bill_id=b.id) credited
		   FROM bills b WHERE b.org_id=c.org_id AND b.contact_id=c.id AND b.status='posted') x),0) AS you_owe
		 FROM contacts c WHERE c.org_id=$1 AND ($2='' OR c.kind=$2 OR c.kind='both') AND c.archived=$3
		 ORDER BY c.name, c.id LIMIT $4`,
		access.ID, role, archived, maxContacts)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type contact struct {
		ID    int64  `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
		Role  string `json:"role"`
		contactProfile
		TheyOwe int64 `json:"they_owe_cents"`
		YouOwe  int64 `json:"you_owe_cents"`
	}
	out := []contact{}
	for rows.Next() {
		var c contact
		if err := rows.Scan(&c.ID, &c.Name, &c.Email, &c.Role, &c.ContactType, &c.CompanyName,
			&c.FirstName, &c.LastName, &c.Phone, &c.Mobile, &c.Website, &c.UEN, &c.GSTNumber, &c.AccountNumber,
			&c.PaymentTermsDays, &c.BillingAddress, &c.ShippingAddress, &c.Notes,
			&c.Salutation, &c.Language, &c.Currency, &c.PortalEnabled, &c.OwnerUserID, &c.OpeningBalance, &c.OpeningDate, &c.InternalRemarks,
			&c.Fax, &c.DirectDial, &c.Country, &c.BankAccountName, &c.BankAccountNumber, &c.BankParticulars,
			&c.SalesAccountID, &c.PurchaseAccountID, &c.CreditLimitCents, &c.BlockOverLimit,
			&c.Archived, &c.DiscountBP, &c.BillTermsDays, &c.SalesTreatment, &c.SalesTaxCode, &c.PurchaseTreatment, &c.PurchaseTaxCode, &c.TheyOwe, &c.YouOwe); err != nil {
			serverError(w, err)
			return
		}
		c.Persons = []contactPerson{}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	// One query for all persons on this page, grouped by contact in position order.
	byID := make(map[int64]int, len(out))
	ids := make([]int64, 0, len(out))
	for i, c := range out {
		byID[c.ID] = i
		ids = append(ids, c.ID)
	}
	if len(ids) > 0 {
		prows, err := a.db.Query(ctx, `SELECT contact_id, salutation, first_name, last_name, email, work_phone, mobile
			FROM contact_persons WHERE org_id=$1 AND contact_id = ANY($2) ORDER BY contact_id, position, id`, access.ID, ids)
		if err != nil {
			serverError(w, err)
			return
		}
		defer prows.Close()
		for prows.Next() {
			var cid int64
			var p contactPerson
			if err := prows.Scan(&cid, &p.Salutation, &p.FirstName, &p.LastName, &p.Email, &p.WorkPhone, &p.Mobile); err != nil {
				serverError(w, err)
				return
			}
			i := byID[cid]
			out[i].Persons = append(out[i].Persons, p)
		}
		if err := prows.Err(); err != nil {
			serverError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": out})
}

// listMembers returns who can own a customer or supplier in this company. Any signed-in member may read it.
func (a *app) listMembers(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, err := strconv.ParseInt(r.URL.Query().Get("org_id"), 10, 64)
	if err != nil || orgID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	ctx := r.Context()
	access, status, code, err := a.orgAccessFor(ctx, u.ID, orgID)
	if err != nil {
		serverError(w, err)
		return
	}
	if code != "" {
		writeError(w, status, code)
		return
	}
	if access.Role == "viewer" {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	rows, err := a.db.Query(ctx, `SELECT u.id, u.email, m.role, m.permissions, (SELECT max(e.occurred_at) FROM login_events e WHERE e.user_id=u.id) FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.org_id=(SELECT COALESCE(parent_company_id,id) FROM organizations WHERE id=$1) ORDER BY u.email`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type member struct {
		ID          int64           `json:"id"`
		Email       string          `json:"email"`
		Role        string          `json:"role"`
		Permissions map[string]bool `json:"permissions"`
		LastLogin   *time.Time      `json:"last_login"`
	}
	out := []member{}
	for rows.Next() {
		var m member
		var raw []byte
		if err := rows.Scan(&m.ID, &m.Email, &m.Role, &raw, &m.LastLogin); err != nil {
			serverError(w, err)
			return
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &m.Permissions)
		}
		if m.Permissions == nil {
			m.Permissions = map[string]bool{}
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

func (a *app) createContact(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.saveContact(w, r, u, false)
}

func (a *app) updateContact(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.saveContact(w, r, u, true)
}

var errOpeningBalanceLocked = errors.New("opening balance already posted")

// postOpeningBalance posts a contact's opening balance once. Customers owe you (Dr AR 1100, Cr 3900);
// suppliers you owe (Dr 3900, Cr AP 2100). A second save with a different amount is refused.
func (a *app) postOpeningBalance(ctx context.Context, tx pgx.Tx, orgID, userID, contactID int64, in contactInput) error {
	var posted int64
	var postedDate string
	err := tx.QueryRow(ctx, `SELECT opening_balance_cents, COALESCE(to_char(opening_balance_date,'YYYY-MM-DD'),'') FROM contacts WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, contactID).Scan(&posted, &postedDate)
	if err != nil {
		return err
	}
	if posted != 0 {
		if posted != in.OpeningBalance || postedDate != in.OpeningDate {
			return errOpeningBalanceLocked
		}
		return nil
	}
	if in.OpeningBalance == 0 {
		return nil
	}
	date, ok := parseDay(in.OpeningDate)
	if !ok {
		return errors.New("invalid opening balance date")
	}
	ids, err := accountIDs(ctx, tx, orgID)
	if err != nil {
		return err
	}
	debit, credit := acctAR, acctOBE
	if in.Role == "supplier" {
		debit, credit = acctOBE, acctAP
	}
	key := fmt.Sprintf("opening-balance-contact-%d", contactID)
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d|%d|%s", contactID, in.OpeningBalance, in.OpeningDate)))
	if _, err := postJournal(ctx, tx, orgID, userID, date, "Opening balance", key, hash, ids[debit], ids[credit], in.OpeningBalance); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE contacts SET opening_balance_cents=$3, opening_balance_date=$4 WHERE org_id=$1 AND id=$2`, orgID, contactID, in.OpeningBalance, date)
	return err
}

func (a *app) saveContact(w http.ResponseWriter, r *http.Request, u currentUser, update bool) {
	var id int64
	if update {
		var err error
		id, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
	}
	in := contactInput{}
	if !decodeJSON(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	email := strings.ToLower(strings.TrimSpace(in.Email))
	emailOK := email == ""
	if !emailOK {
		_, emailOK = normalizeEmail(email)
	}
	if !validContactProfile(&in) || name == "" || !utf8.ValidString(name) || strings.ContainsRune(name, 0) || utf8.RuneCountInString(name) > maxNameLen || !emailOK {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	ctx := r.Context()
	access, status, code, err := a.orgAccessFor(ctx, u.ID, in.OrgID)
	if err != nil {
		serverError(w, err)
		return
	}
	if code != "" {
		writeError(w, status, code)
		return
	}
	if !writerRoles[access.Role] {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if !a.requireFeature(w, ctx, access.ID, u.ID, "contacts") {
		return
	}
	billing, _ := json.Marshal(in.BillingAddress)
	shipping, _ := json.Marshal(in.ShippingAddress)
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	// Opening balances use control accounts; serialize before locking the contact.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	// Default sales/purchase accounts must belong to this company and have the right type.
	for _, account := range []struct {
		id  int64
		typ string
	}{{in.SalesAccountID, "revenue"}, {in.PurchaseAccountID, "expense"}} {
		if account.id == 0 {
			continue
		}
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE org_id=$1 AND id=$2 AND type=$3)`, access.ID, account.id, account.typ).Scan(&valid); err != nil {
			serverError(w, err)
			return
		}
		if !valid {
			writeError(w, http.StatusUnprocessableEntity, "invalid_account")
			return
		}
	}
	// The owner must already be a member of this company. Empty means no owner.
	var owner any
	if in.OwnerUserID > 0 {
		var member bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships WHERE org_id=(SELECT COALESCE(parent_company_id,id) FROM organizations WHERE id=$1) AND user_id=$2)`, access.ID, in.OwnerUserID).Scan(&member); err != nil {
			serverError(w, err)
			return
		}
		if !member {
			writeError(w, http.StatusUnprocessableEntity, "invalid_owner")
			return
		}
		owner = in.OwnerUserID
	}
	values := []any{access.ID, name, email, in.Role, u.ID, in.ContactType, in.CompanyName, in.FirstName,
		in.LastName, in.Phone, in.Mobile, in.Website, in.UEN, in.GSTNumber, in.AccountNumber, in.PaymentTermsDays,
		billing, shipping, in.Notes, in.Salutation, in.Language, in.PortalEnabled, owner, in.InternalRemarks,
		in.Fax, in.DirectDial, in.Country, in.BankAccountName, in.BankAccountNumber, in.BankParticulars,
		in.SalesAccountID, in.PurchaseAccountID, in.CreditLimitCents, in.BlockOverLimit,
		in.DiscountBP, in.BillTermsDays, in.SalesTreatment, in.SalesTaxCode, in.PurchaseTreatment, in.PurchaseTaxCode}
	query := `INSERT INTO contacts (org_id, name, email, kind, created_by, contact_type, company_name,
		 first_name, last_name, phone, mobile, website, uen, gst_number, account_number, payment_terms_days,
		 billing_address, shipping_address, notes, salutation, language, portal_enabled, owner_user_id,
		 internal_remarks, fax, direct_dial, country, bank_account_name, bank_account_number, bank_particulars,
		 sales_account_id, purchase_account_id, credit_limit_cents, block_over_limit,
		 discount_basis_points, bill_payment_terms_days, sales_treatment, sales_tax_code, purchase_treatment, purchase_tax_code)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,
		   $25,$26,$27,$28,$29,$30,NULLIF($31::bigint,0),NULLIF($32::bigint,0),$33,$34,$35,$36,$37,$38,$39,$40) RETURNING id`
	status, action := http.StatusCreated, "contact.created"
	if update {
		values[4] = id
		query = `UPDATE contacts SET name=$2, email=$3, kind=$4, contact_type=$6, company_name=$7,
		 first_name=$8, last_name=$9, phone=$10, mobile=$11, website=$12, uen=$13, gst_number=$14,
		 payment_terms_days=$16, billing_address=$17, shipping_address=$18, notes=$19, salutation=$20,
		 language=$21, portal_enabled=$22, owner_user_id=$23, internal_remarks=$24, account_number=$15,
		 fax=$25, direct_dial=$26, country=$27, bank_account_name=$28, bank_account_number=$29, bank_particulars=$30,
		 sales_account_id=NULLIF($31::bigint,0), purchase_account_id=NULLIF($32::bigint,0),
		 credit_limit_cents=$33, block_over_limit=$34, discount_basis_points=$35,
		 bill_payment_terms_days=$36, sales_treatment=$37, sales_tax_code=$38, purchase_treatment=$39,
		 purchase_tax_code=$40, updated_at=now()
		 WHERE org_id=$1 AND id=$5 RETURNING id`
		status, action = http.StatusOK, "contact.updated"
	}
	if err := tx.QueryRow(ctx, query, values...).Scan(&id); err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found")
		} else {
			serverError(w, err)
		}
		return
	}
	// Persons are replaced as a set. Order in the request is kept as their position.
	if _, err := tx.Exec(ctx, `DELETE FROM contact_persons WHERE org_id=$1 AND contact_id=$2`, access.ID, id); err != nil {
		serverError(w, err)
		return
	}
	for i, p := range in.Persons {
		if _, err := tx.Exec(ctx, `INSERT INTO contact_persons (org_id, contact_id, salutation, first_name, last_name, email, work_phone, mobile, position)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, access.ID, id, p.Salutation, p.FirstName, p.LastName, p.Email, p.WorkPhone, p.Mobile, i); err != nil {
			serverError(w, err)
			return
		}
	}
	// Check zero too: clearing an already posted balance must not rewrite the books.
	if err := a.postOpeningBalance(ctx, tx, access.ID, u.ID, id, in); err != nil {
		if errors.Is(err, errOpeningBalanceLocked) {
			writeError(w, http.StatusConflict, "opening_balance_locked")
			return
		}
		if isPeriodClosed(err) {
			writeError(w, http.StatusUnprocessableEntity, "period_closed")
			return
		}
		serverError(w, err)
		return
	}
	if in.Persons == nil {
		in.Persons = []contactPerson{}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (org_id,user_id,action) VALUES ($1,$2,$3)`, access.ID, u.ID, action); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	in.OrgID, in.Name, in.Email = access.ID, name, email
	writeJSON(w, status, struct {
		ID int64 `json:"id"`
		contactInput
	}{id, in})
}
