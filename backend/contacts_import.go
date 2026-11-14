package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Import contacts from CSV. Header row required; ContactName is required, other columns optional:
// Salutation, FirstName, LastName, EmailAddress, PhoneNumber, MobileNumber, Website, UEN, GSTNumber,
// AccountNumber, Notes, Role (customer|supplier|both, default customer), FaxNumber, DirectDial, Country,
// BankAccountName, BankAccountNumber, Particulars, Billing*/Delivery* address columns, and
// Person1..Person5{Salutation,FirstName,LastName,Email,WorkPhone,Mobile}.
// A row whose name matches an existing contact (case-insensitive) updates only the non-empty cells
// (Xero's "empty fields will be ignored" mode) unless ?delete=1 clears them first.
// Address and person columns are only written when the file has at least one such column, so a
// file that never mentions them cannot wipe them.

const maxContactImportRows = 1000

// contactImportColumns is the canonical header set shared by import and export.
func contactImportColumns() []string {
	cols := []string{"ContactName", "Salutation", "FirstName", "LastName", "EmailAddress", "PhoneNumber",
		"MobileNumber", "Website", "Role", "UEN", "GSTNumber", "AccountNumber", "Notes",
		"FaxNumber", "DirectDial", "Country", "BankAccountName", "BankAccountNumber", "Particulars"}
	for _, prefix := range []string{"Billing", "Delivery"} {
		for _, f := range []string{"Attention", "Line1", "Line2", "City", "Region", "PostalCode", "Country", "Phone", "Fax"} {
			cols = append(cols, prefix+f)
		}
	}
	for i := 1; i <= 5; i++ {
		for _, f := range []string{"Salutation", "FirstName", "LastName", "Email", "WorkPhone", "Mobile"} {
			cols = append(cols, fmt.Sprintf("Person%d%s", i, f))
		}
	}
	return cols
}

type contactImportRow struct {
	name, email, first, last, phone, mobile, site, salutation, uen, gst, account, notes, role, fax, dial, country, bankName, bankNumber, bankParticulars string
	billing, shipping                                                                                                                                    contactAddress
	persons                                                                                                                                              []contactPerson
}

// newContactImportRow builds and validates one row before the import writes.
func newContactImportRow(cell func(string) string) (contactImportRow, error) {
	x := contactImportRow{name: strings.TrimSpace(cell("contactname")), email: strings.ToLower(cell("emailaddress")), first: cell("firstname"),
		last: cell("lastname"), phone: cell("phonenumber"), mobile: cell("mobilenumber"), site: cell("website"),
		salutation: cell("salutation"), uen: cell("uen"), gst: cell("gstnumber"), account: cell("accountnumber"),
		notes: cell("notes"), role: strings.ToLower(cell("role")), fax: cell("faxnumber"), dial: cell("directdial"),
		country: cell("country"), bankName: cell("bankaccountname"), bankNumber: cell("bankaccountnumber"),
		bankParticulars: cell("particulars")}
	x.billing = contactAddress{Attention: cell("billingattention"), Line1: cell("billingline1"), Line2: cell("billingline2"),
		City: cell("billingcity"), Region: cell("billingregion"), PostalCode: cell("billingpostalcode"),
		Country: strings.ToUpper(cell("billingcountry")), Phone: cell("billingphone"), Fax: cell("billingfax")}
	x.shipping = contactAddress{Attention: cell("deliveryattention"), Line1: cell("deliveryline1"), Line2: cell("deliveryline2"),
		City: cell("deliverycity"), Region: cell("deliveryregion"), PostalCode: cell("deliverypostalcode"),
		Country: strings.ToUpper(cell("deliverycountry")), Phone: cell("deliveryphone"), Fax: cell("deliveryfax")}
	for i := 1; i <= 5; i++ {
		p := contactPerson{Salutation: cell(fmt.Sprintf("person%dsalutation", i)), FirstName: cell(fmt.Sprintf("person%dfirstname", i)),
			LastName: cell(fmt.Sprintf("person%dlastname", i)), Email: cell(fmt.Sprintf("person%demail", i)),
			WorkPhone: cell(fmt.Sprintf("person%dworkphone", i)), Mobile: cell(fmt.Sprintf("person%dmobile", i))}
		if p.Salutation != "" || p.FirstName != "" || p.LastName != "" || p.Email != "" || p.WorkPhone != "" || p.Mobile != "" {
			x.persons = append(x.persons, p)
		}
	}
	if x.role == "" {
		x.role = "customer"
	}
	if !validAddress(x.billing) || !validAddress(x.shipping) || len(x.persons) > 5 {
		return contactImportRow{}, errors.New("invalid contact row")
	}
	for _, p := range x.persons {
		if p.Email != "" {
			if _, ok := normalizeEmail(p.Email); !ok {
				return contactImportRow{}, errors.New("invalid contact person email")
			}
		}
		for _, f := range []struct {
			value string
			limit int
		}{{p.Salutation, 20}, {p.FirstName, 100}, {p.LastName, 100}, {p.Email, 254}, {p.WorkPhone, 40}, {p.Mobile, 40}} {
			if !validTaxText(f.value, f.limit) {
				return contactImportRow{}, errors.New("invalid contact person field")
			}
		}
	}
	emailOK := x.email == ""
	if !emailOK {
		_, emailOK = normalizeEmail(x.email)
	}
	if x.name == "" || !validTaxText(x.name, maxNameLen) || !emailOK || !validTaxText(x.first, 100) || !validTaxText(x.last, 100) ||
		!validTaxText(x.phone, 40) || !validTaxText(x.mobile, 40) || !validTaxText(x.site, 300) || !validWebsite(x.site) ||
		!validTaxText(x.salutation, 20) || !validTaxText(x.gst, 30) || !validTaxText(x.account, 50) || !validTaxText(x.notes, 4000) ||
		!validTaxText(x.fax, 40) || !validTaxText(x.dial, 40) || !validTaxText(x.country, 60) ||
		!validTaxText(x.bankName, 100) || !validTaxText(x.bankNumber, 60) || !validTaxText(x.bankParticulars, 100) ||
		(x.uen != "" && !uenRe.MatchString(x.uen)) ||
		(x.role != "customer" && x.role != "supplier" && x.role != "both") {
		return contactImportRow{}, errors.New("invalid contact row")
	}
	return x, nil
}

func validWebsite(s string) bool {
	if s == "" {
		return true
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil
}

func (a *app) importContacts(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, true)
	if !ok {
		return
	}
	if !writerRoles[access.Role] {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	rd := csv.NewReader(r.Body)
	rd.FieldsPerRecord = -1
	rd.TrimLeadingSpace = true
	records, err := rd.ReadAll()
	if err != nil || len(records) < 2 {
		writeError(w, http.StatusBadRequest, "invalid_import")
		return
	}
	if len(records)-1 > maxContactImportRows {
		writeError(w, http.StatusBadRequest, "import_too_large")
		return
	}
	col := map[string]int{}
	allowed := map[string]bool{}
	for _, h := range contactImportColumns() {
		allowed[strings.ToLower(h)] = true
	}
	for i, h := range records[0] {
		h = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
		if _, dup := col[h]; dup || !allowed[h] {
			writeError(w, http.StatusBadRequest, "invalid_import")
			return
		}
		col[h] = i
	}
	nameCol, ok := col["contactname"]
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_import")
		return
	}
	cell := func(rec []string, h string) string {
		if i, ok := col[h]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	rows := make([]contactImportRow, 0, len(records)-1)
	for _, rec := range records[1:] {
		if len(rec) != len(records[0]) || nameCol >= len(rec) {
			writeError(w, http.StatusBadRequest, "invalid_import")
			return
		}
		x, err := newContactImportRow(func(h string) string { return cell(rec, h) })
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_import")
			return
		}
		rows = append(rows, x)
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
	// Step 2 of the import: report what would change without writing anything.
	if r.URL.Query().Get("preview") == "1" {
		created, updated := 0, 0
		for _, x := range rows {
			var matches int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM contacts WHERE org_id=$1 AND lower(name)=lower($2)`, access.ID, x.name).Scan(&matches); err != nil {
				serverError(w, err)
				return
			}
			if matches > 1 {
				writeError(w, http.StatusConflict, "ambiguous_contact_name")
				return
			}
			if matches == 1 {
				updated++
			} else {
				created++
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"preview": true, "created": created, "updated": updated, "rows": len(rows)})
		return
	}
	// Xero's "delete existing information" mode clears every field the file mentions before writing.
	// The contact name and role are never cleared, since they identify the row.
	wipe := r.URL.Query().Get("delete") == "1"
	// Address and person columns only touch the database when the file carries them.
	addrCols, personCols := false, false
	for h := range col {
		if strings.HasPrefix(h, "billing") || strings.HasPrefix(h, "delivery") {
			addrCols = true
		}
		if strings.HasPrefix(h, "person") {
			personCols = true
		}
	}
	fields := []string{"email", "first_name", "last_name", "phone", "website", "mobile", "salutation", "uen", "gst_number", "account_number", "notes",
		"fax", "direct_dial", "country", "bank_account_name", "bank_account_number", "bank_particulars"}
	assign := make([]string, len(fields))
	for i, f := range fields {
		if wipe {
			assign[i] = fmt.Sprintf("%s=$%d", f, i+3)
		} else {
			assign[i] = fmt.Sprintf("%s=COALESCE(NULLIF($%d,''),%s)", f, i+3, f)
		}
	}
	if addrCols {
		assign = append(assign, fmt.Sprintf("billing_address=$%d", len(assign)+3), fmt.Sprintf("shipping_address=$%d", len(assign)+4))
	}
	updateSQL := `UPDATE contacts SET ` + strings.Join(assign, ", ") + `, updated_at=now()
		WHERE id=(SELECT id FROM contacts WHERE org_id=$1 AND lower(name)=lower($2) ORDER BY id LIMIT 1) AND org_id=$1 RETURNING id`
	created, updated := 0, 0
	for _, x := range rows {
		var matches int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM contacts WHERE org_id=$1 AND lower(name)=lower($2)`, access.ID, x.name).Scan(&matches); err != nil {
			serverError(w, err)
			return
		}
		if matches > 1 {
			writeError(w, http.StatusConflict, "ambiguous_contact_name")
			return
		}
		billing, _ := json.Marshal(x.billing)
		shipping, _ := json.Marshal(x.shipping)
		args := []any{access.ID, x.name, x.email, x.first, x.last, x.phone, x.site, x.mobile, x.salutation, x.uen, x.gst, x.account, x.notes,
			x.fax, x.dial, x.country, x.bankName, x.bankNumber, x.bankParticulars}
		if addrCols {
			args = append(args, billing, shipping)
		}
		var id int64
		err := tx.QueryRow(ctx, updateSQL, args...).Scan(&id)
		if err == nil {
			updated++
			if personCols {
				if err := replaceImportedPersons(ctx, tx, access.ID, id, x.persons); err != nil {
					serverError(w, err)
					return
				}
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			serverError(w, err)
			return
		}
		if err := tx.QueryRow(ctx, `INSERT INTO contacts (org_id,name,email,kind,created_by,first_name,last_name,phone,website,mobile,salutation,uen,gst_number,account_number,notes,
				fax,direct_dial,country,bank_account_name,bank_account_number,bank_particulars,billing_address,shipping_address)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23) RETURNING id`,
			access.ID, x.name, x.email, x.role, u.ID, x.first, x.last, x.phone, x.site, x.mobile, x.salutation, x.uen, x.gst, x.account, x.notes,
			x.fax, x.dial, x.country, x.bankName, x.bankNumber, x.bankParticulars, billing, shipping).Scan(&id); err != nil {
			serverError(w, err)
			return
		}
		created++
		if personCols {
			if err := replaceImportedPersons(ctx, tx, access.ID, id, x.persons); err != nil {
				serverError(w, err)
				return
			}
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'contacts.imported',jsonb_build_object('created',$3::int,'updated',$4::int))`, access.ID, u.ID, created, updated); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": created, "updated": updated})
}

// replaceImportedPersons sets a contact's people to exactly the rows the file carries, in file order.
func replaceImportedPersons(ctx context.Context, tx pgx.Tx, orgID, contactID int64, persons []contactPerson) error {
	if _, err := tx.Exec(ctx, `DELETE FROM contact_persons WHERE org_id=$1 AND contact_id=$2`, orgID, contactID); err != nil {
		return err
	}
	for i, p := range persons {
		if _, err := tx.Exec(ctx, `INSERT INTO contact_persons (org_id, contact_id, salutation, first_name, last_name, email, work_phone, mobile, position)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, orgID, contactID, p.Salutation, p.FirstName, p.LastName, p.Email, p.WorkPhone, p.Mobile, i); err != nil {
			return err
		}
	}
	return nil
}

// exportContacts writes the company's contacts as CSV using the same column names the importer accepts,
// including billing/delivery addresses and up to five people per contact, so an export re-imports cleanly.
func (a *app) exportContacts(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id, name, salutation, first_name, last_name, email, phone, mobile, website,
		kind, uen, gst_number, account_number, notes, fax, direct_dial, country,
		bank_account_name, bank_account_number, bank_particulars, billing_address, shipping_address
		FROM contacts WHERE org_id=$1 ORDER BY lower(name)`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type record struct {
		id                             int64
		name, salutation, first, last  string
		email, phone, mobile, website  string
		kind, uen, gst, account, notes string
		fax, dial, country             string
		bankName, bankNumber, bankPart string
		billing, shipping              contactAddress
	}
	out := []record{}
	for rows.Next() {
		var rec record
		var billing, shipping []byte
		if err := rows.Scan(&rec.id, &rec.name, &rec.salutation, &rec.first, &rec.last, &rec.email, &rec.phone, &rec.mobile, &rec.website,
			&rec.kind, &rec.uen, &rec.gst, &rec.account, &rec.notes, &rec.fax, &rec.dial, &rec.country,
			&rec.bankName, &rec.bankNumber, &rec.bankPart, &billing, &shipping); err != nil {
			serverError(w, err)
			return
		}
		_ = json.Unmarshal(billing, &rec.billing)
		_ = json.Unmarshal(shipping, &rec.shipping)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	persons := map[int64][]contactPerson{}
	if len(out) > 0 {
		ids := make([]int64, len(out))
		for i, rec := range out {
			ids[i] = rec.id
		}
		prows, err := a.db.Query(r.Context(), `SELECT contact_id, salutation, first_name, last_name, email, work_phone, mobile
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
			persons[cid] = append(persons[cid], p)
		}
		if err := prows.Err(); err != nil {
			serverError(w, err)
			return
		}
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="contacts.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	header := contactImportColumns()
	_ = cw.Write(header)
	for _, rec := range out {
		line := []string{rec.name, rec.salutation, rec.first, rec.last, rec.email, rec.phone, rec.mobile, rec.website,
			rec.kind, rec.uen, rec.gst, rec.account, rec.notes, rec.fax, rec.dial, rec.country,
			rec.bankName, rec.bankNumber, rec.bankPart}
		for _, addr := range []contactAddress{rec.billing, rec.shipping} {
			line = append(line, addr.Attention, addr.Line1, addr.Line2, addr.City, addr.Region, addr.PostalCode, addr.Country, addr.Phone, addr.Fax)
		}
		people := persons[rec.id]
		for i := 0; i < 5; i++ {
			if i < len(people) {
				p := people[i]
				line = append(line, p.Salutation, p.FirstName, p.LastName, p.Email, p.WorkPhone, p.Mobile)
			} else {
				line = append(line, "", "", "", "", "", "")
			}
		}
		for i := range line {
			line[i] = csvSafeCell(line[i])
		}
		_ = cw.Write(line)
	}
}

// csvSafeCell stops a spreadsheet from running exported text as a formula (R20). Plain numbers,
// including negative amounts, are left untouched so numeric columns stay numeric.
func csvSafeCell(v string) string {
	if v == "" {
		return v
	}
	if strings.ContainsRune("=+@\t\r", rune(v[0])) {
		return "'" + v
	}
	if v[0] == '-' && len(v) > 1 && (v[1] < '0' || v[1] > '9') && v[1] != '.' {
		return "'" + v
	}
	return v
}
