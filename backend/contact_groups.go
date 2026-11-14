package main

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Contact groups and smart lists, plus archiving. Archiving never deletes: documents keep their
// contact and the name still prints on historic records. Groups and smart lists are organisation
// data, so every handler goes through orgAccessFor.

func (a *app) archiveContact(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID    int64 `json:"org_id"`
		Archived bool  `json:"archived"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	tag, err := a.db.Exec(r.Context(), `UPDATE contacts SET archived=$3, updated_at=now() WHERE org_id=$1 AND id=$2`, access.ID, id, in.Archived)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "archived": in.Archived})
}

// contactDocuments is the contact's own activity table: every invoice or bill with the paid and
// sent dates Xero shows. Sent is the approval (posting) date; Tidal does not email documents.
type contactDocumentOut struct {
	ID              int64  `json:"id"`
	DocumentNumber  string `json:"document_number"`
	Reference       string `json:"reference"`
	Date            string `json:"date"`
	DueDate         string `json:"due_date"`
	TotalCents      int64  `json:"total_cents"`
	PaidCents       int64  `json:"paid_cents"`
	PaidDate        string `json:"paid_date"`
	SentDate        string `json:"sent_date"`
	Status          string `json:"status"`
	LifecycleStatus string `json:"lifecycle_status"`
}

func (a *app) contactDocuments(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	ctx := r.Context()
	var exists bool
	if err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contacts WHERE org_id=$1 AND id=$2)`, access.ID, id).Scan(&exists); err != nil {
		serverError(w, err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	// Balance, last payment date and approval date come from the same tables the lists use.
	invoices, err := a.db.Query(ctx, `SELECT i.id, COALESCE(i.document_number,''), i.reference,
		to_char(i.invoice_date,'YYYY-MM-DD'), to_char(i.due_date,'YYYY-MM-DD'), i.total_cents,
		GREATEST(i.total_cents - COALESCE((SELECT SUM(p.amount_cents) FROM payments p WHERE p.org_id=i.org_id AND p.invoice_id=i.id),0)
			- COALESCE((SELECT SUM(n.amount_cents) FROM credit_notes n WHERE n.org_id=i.org_id AND n.invoice_id=i.id),0)
			- COALESCE((SELECT SUM(cc.amount_cents) FROM customer_credit_applications cc WHERE cc.org_id=i.org_id AND cc.invoice_id=i.id),0), 0),
		COALESCE((SELECT to_char(MAX(p.payment_date),'YYYY-MM-DD') FROM payments p WHERE p.org_id=i.org_id AND p.invoice_id=i.id), ''),
		COALESCE(to_char(i.posted_at,'YYYY-MM-DD'),''), i.status, `+lifecycleCase+`
		FROM (SELECT * FROM invoices WHERE org_id=$1 AND contact_id=$2) i ORDER BY i.invoice_date DESC, i.id DESC LIMIT 200`, access.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	out := []contactDocumentOut{}
	for invoices.Next() {
		var d contactDocumentOut
		if err := invoices.Scan(&d.ID, &d.DocumentNumber, &d.Reference, &d.Date, &d.DueDate, &d.TotalCents, &d.PaidCents, &d.PaidDate, &d.SentDate, &d.Status, &d.LifecycleStatus); err != nil {
			invoices.Close()
			serverError(w, err)
			return
		}
		out = append(out, d)
	}
	invoices.Close()
	if err := invoices.Err(); err != nil {
		serverError(w, err)
		return
	}
	bills, err := a.db.Query(ctx, `SELECT b.id, COALESCE(b.document_number,''), b.supplier_reference,
		to_char(b.bill_date,'YYYY-MM-DD'), to_char(b.due_date,'YYYY-MM-DD'), b.total_cents,
		GREATEST(b.total_cents - COALESCE((SELECT SUM(p.amount_cents) FROM bill_payments p WHERE p.org_id=b.org_id AND p.bill_id=b.id),0)
			- COALESCE((SELECT SUM(sc.total_cents) FROM supplier_credits sc WHERE sc.org_id=b.org_id AND sc.bill_id=b.id),0), 0),
		COALESCE((SELECT to_char(MAX(p.payment_date),'YYYY-MM-DD') FROM bill_payments p WHERE p.org_id=b.org_id AND p.bill_id=b.id), ''),
		COALESCE(to_char(b.posted_at,'YYYY-MM-DD'),''), b.status, `+lifecycleCase+`
		FROM (SELECT * FROM bills WHERE org_id=$1 AND contact_id=$2) b ORDER BY b.bill_date DESC, b.id DESC LIMIT 200`, access.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	defer bills.Close()
	billOut := []contactDocumentOut{}
	for bills.Next() {
		var d contactDocumentOut
		if err := bills.Scan(&d.ID, &d.DocumentNumber, &d.Reference, &d.Date, &d.DueDate, &d.TotalCents, &d.PaidCents, &d.PaidDate, &d.SentDate, &d.Status, &d.LifecycleStatus); err != nil {
			serverError(w, err)
			return
		}
		billOut = append(billOut, d)
	}
	if err := bills.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invoices": out, "bills": billOut})
}

type contactGroupOut struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	ContactIDs []int64 `json:"contact_ids"`
}

func (a *app) listContactGroups(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	ctx := r.Context()
	rows, err := a.db.Query(ctx, `SELECT id, name FROM contact_groups WHERE org_id=$1 ORDER BY lower(name), id`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	out := []contactGroupOut{}
	index := map[int64]int{}
	for rows.Next() {
		var g contactGroupOut
		if err := rows.Scan(&g.ID, &g.Name); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		g.ContactIDs = []int64{}
		index[g.ID] = len(out)
		out = append(out, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	mrows, err := a.db.Query(ctx, `SELECT group_id, contact_id FROM contact_group_members WHERE org_id=$1 ORDER BY contact_id`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer mrows.Close()
	for mrows.Next() {
		var gid, cid int64
		if err := mrows.Scan(&gid, &cid); err != nil {
			serverError(w, err)
			return
		}
		if i, found := index[gid]; found {
			out[i].ContactIDs = append(out[i].ContactIDs, cid)
		}
	}
	if err := mrows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}

// groupName validates the one text field a group carries.
func groupName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	return name, validTaxText(name, 100) && name != ""
}

func (a *app) saveContactGroup(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID int64  `json:"org_id"`
		Name  string `json:"name"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	name, ok := groupName(in.Name)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	status := http.StatusCreated
	var id int64
	var err error
	if r.Method == http.MethodPut {
		if id, ok = pathID(w, r); !ok {
			return
		}
		tag, uerr := a.db.Exec(ctx, `UPDATE contact_groups SET name=$3 WHERE org_id=$1 AND id=$2`, access.ID, id, name)
		if uerr != nil {
			if isUniqueViolation(uerr) {
				writeError(w, http.StatusConflict, "duplicate_name")
				return
			}
			serverError(w, uerr)
			return
		}
		if tag.RowsAffected() == 0 {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		status = http.StatusOK
	} else {
		err = a.db.QueryRow(ctx, `INSERT INTO contact_groups(org_id,name) VALUES($1,$2) RETURNING id`, access.ID, name).Scan(&id)
		if err != nil {
			if isUniqueViolation(err) {
				writeError(w, http.StatusConflict, "duplicate_name")
				return
			}
			serverError(w, err)
			return
		}
	}
	writeJSON(w, status, map[string]any{"id": id, "name": name})
}

func (a *app) deleteContactGroup(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, true)
	if !ok {
		return
	}
	if _, err := a.db.Exec(r.Context(), `DELETE FROM contact_groups WHERE org_id=$1 AND id=$2`, access.ID, id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

// addGroupMembers adds contacts to a group; a contact already in the group is left alone.
func (a *app) addGroupMembers(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID      int64   `json:"org_id"`
		ContactIDs []int64 `json:"contact_ids"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if len(in.ContactIDs) == 0 || len(in.ContactIDs) > 500 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	var exists bool
	if err := a.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contact_groups WHERE org_id=$1 AND id=$2)`, access.ID, id).Scan(&exists); err != nil {
		serverError(w, err)
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	for _, cid := range in.ContactIDs {
		var company, kind string
		if err := a.db.QueryRow(ctx, `SELECT org_id::text, kind FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, cid).Scan(&company, &kind); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, http.StatusUnprocessableEntity, "invalid_contact")
				return
			}
			serverError(w, err)
			return
		}
		if _, err := a.db.Exec(ctx, `INSERT INTO contact_group_members(org_id,group_id,contact_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, access.ID, id, cid); err != nil {
			serverError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "added": len(in.ContactIDs)})
}

func (a *app) removeGroupMember(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	contactID, err := strconv.ParseInt(r.PathValue("contactId"), 10, 64)
	if err != nil || contactID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, true)
	if !ok {
		return
	}
	if _, err := a.db.Exec(r.Context(), `DELETE FROM contact_group_members WHERE org_id=$1 AND group_id=$2 AND contact_id=$3`, access.ID, id, contactID); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group_id": id, "contact_id": contactID})
}

type contactSmartListOut struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Query string `json:"query"`
}

func (a *app) listContactSmartLists(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,name,query FROM contact_smart_lists WHERE org_id=$1 ORDER BY lower(name), id`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []contactSmartListOut{}
	for rows.Next() {
		var s contactSmartListOut
		if err := rows.Scan(&s.ID, &s.Name, &s.Query); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"smart_lists": out})
}

func (a *app) saveContactSmartList(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID int64  `json:"org_id"`
		Name  string `json:"name"`
		Query string `json:"query"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	name, ok := groupName(in.Name)
	in.Query = strings.TrimSpace(in.Query)
	if !ok || !validTaxText(in.Query, 200) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	var id int64
	if err := a.db.QueryRow(r.Context(), `INSERT INTO contact_smart_lists(org_id,name,query) VALUES($1,$2,$3) RETURNING id`, access.ID, name, in.Query).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "duplicate_name")
			return
		}
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": name, "query": in.Query})
}

func (a *app) deleteContactSmartList(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, true)
	if !ok {
		return
	}
	if _, err := a.db.Exec(r.Context(), `DELETE FROM contact_smart_lists WHERE org_id=$1 AND id=$2`, access.ID, id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}
