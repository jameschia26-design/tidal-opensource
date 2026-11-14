package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

// emailDocument sends one plain-text document email. It runs after the posting transaction commits, so
// a mail failure cannot roll back a posted document: the reply says the document posted and the email
// did not.
func (a *app) emailDocument(to, subject, body string) error {
	return a.mail.send(to, subject, body)
}

// approveAndSendInvoice: POST /api/v1/invoices/{id}/approve-and-email {org_id,to}
// Approving posts an invoice that is waiting for approval; emailing is a separate outcome and the reply
// reports each one, so a mail problem never hides a posting problem.
func (a *app) approveAndSendInvoice(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.approveAndSend(w, r, u, true)
}

// approveAndSendBill: POST /api/v1/bills/{id}/approve-and-email {org_id,to}
func (a *app) approveAndSendBill(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.approveAndSend(w, r, u, false)
}

func (a *app) approveAndSend(w http.ResponseWriter, r *http.Request, u currentUser, invoice bool) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID int64  `json:"org_id"`
		To    string `json:"to"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	feature := "sales"
	kind := "Invoice"
	table := "invoices"
	if !invoice {
		feature, kind, table = "purchases", "Bill", "bills"
	}
	if !a.requireFeature(w, r.Context(), access.ID, u.ID, feature) {
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var status, contactEmail, orgName string
	var number int64
	var total int64
	var dateField string
	if invoice {
		dateField = "due_date"
	} else {
		dateField = "due_date"
	}
	query := fmt.Sprintf(`SELECT x.status, c.email, o.name, COALESCE(x.number,0), x.total_cents, x.%s::text
		FROM %s x JOIN contacts c ON c.org_id=x.org_id AND c.id=x.contact_id
		JOIN organizations o ON o.id=x.org_id WHERE x.org_id=$1 AND x.id=$2 FOR UPDATE OF x`, dateField, table)
	var due string
	if err := tx.QueryRow(ctx, query, access.ID, id).Scan(&status, &contactEmail, &orgName, &number, &total, &due); err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		serverError(w, err)
		return
	}
	var result postResult
	var code string
	if status == "awaiting_approval" || status == "draft" {
		if invoice {
			result, code, err = postInvoiceTx(ctx, tx, access.ID, u.ID, id, nil, false)
		} else {
			result, code, err = postBillTx(ctx, tx, access.ID, u.ID, id, nil, false)
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if code != "" {
			writeError(w, statusForCode(code), code)
			return
		}
		number = result.Number
	} else if status != "posted" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_status")
		return
	}
	// The number and total are re-read after posting so the email quotes the stored document.
	if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT COALESCE(number,0), total_cents FROM %s WHERE org_id=$1 AND id=$2`, table), access.ID, id).Scan(&number, &total); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}

	to := strings.TrimSpace(in.To)
	if to == "" {
		to = strings.TrimSpace(contactEmail)
	}
	label := fmt.Sprintf("%s %d", kind, number)
	if number == 0 {
		label = kind
	}
	body := fmt.Sprintf("%s from %s\n\nAmount: %.2f\nDue: %s\n\nPlease reply to this message if anything looks wrong.\n",
		label, orgName, float64(total)/100, due)
	emailed := false
	if to != "" {
		if err := a.emailDocument(to, label+" from "+orgName, body); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"status": "posted", "id": id, "number": number, "emailed": false, "email_error": "unavailable"})
			return
		}
		emailed = true
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "posted", "id": id, "number": number, "emailed": emailed, "to": to})
}
