package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const (
	acctAP = "2100"
	// acctGST is the single GST control liability: output tax is credited, input tax debited, so the
	// balance sheet carries one GST line like Xero's.
	acctGST = "2300"
)

type billLineIn struct {
	ItemID         int64  `json:"item_id"`
	AccountID      int64  `json:"account_id"`
	Description    string `json:"description"`
	Quantity       int64  `json:"quantity"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	GSTCode        string `json:"gst_code"`
	// DiscountBasisPoints (percent x100) wins over DiscountCents when non-zero; the server stores the resulting cents.
	DiscountCents       int64  `json:"discount_cents"`
	DiscountBasisPoints int64  `json:"discount_basis_points"`
	CustomerID          *int64 `json:"customer_id"`
	DiscountAccountID   *int64 `json:"discount_account_id"`
	// Tracking is one option per tracking category for this line: {"<category_id>": <option_id>}.
	Tracking map[string]int64 `json:"tracking"`
}
type billInput struct {
	OrgID             int64        `json:"org_id"`
	DocumentNumber    string       `json:"document_number"`
	ContactID         int64        `json:"contact_id"`
	SupplierReference string       `json:"supplier_reference"`
	BillDate          string       `json:"bill_date"`
	DueDate           string       `json:"due_date"`
	Notes             string       `json:"notes"`
	OrderNumber       string       `json:"order_number"`
	Subject           string       `json:"subject"`
	PaymentTermsDays  int          `json:"payment_terms_days"`
	DiscountAccountID *int64       `json:"discount_account_id"`
	PermitNumber      string       `json:"permit_number"`
	PlannedDate       string       `json:"planned_date"`
	Lines             []billLineIn `json:"lines"`
	Currency          string       `json:"currency"`
	FXRateMicro       int64        `json:"fx_rate_micro"`
}
type billLineOut struct {
	invoiceLineOut
	GrossCents          int64  `json:"gross_cents"`
	DiscountCents       int64  `json:"discount_cents"`
	DiscountBasisPoints int64  `json:"discount_basis_points"`
	CustomerID          *int64 `json:"customer_id"`
	DiscountAccountID   *int64 `json:"discount_account_id"`
	AccountID           int64  `json:"account_id"`
	AccountCode         string `json:"account_code"`
	AccountName         string `json:"account_name"`
}
type billOut struct {
	ID                int64           `json:"id"`
	DocumentNumber    string          `json:"document_number"`
	LifecycleStatus   string          `json:"lifecycle_status"`
	ContactID         int64           `json:"contact_id"`
	SupplierName      string          `json:"supplier_name"`
	SupplierReference string          `json:"supplier_reference"`
	BillDate          string          `json:"bill_date"`
	DueDate           string          `json:"due_date"`
	Notes             string          `json:"notes"`
	OrderNumber       string          `json:"order_number"`
	Subject           string          `json:"subject"`
	PaymentTermsDays  int             `json:"payment_terms_days"`
	DiscountAccountID *int64          `json:"discount_account_id"`
	PermitNumber      string          `json:"permit_number"`
	PlannedDate       string          `json:"planned_date"`
	Paid              bool            `json:"paid"`
	Status            string          `json:"status"`
	TotalCents        int64           `json:"total_cents"`
	GSTCents          int64           `json:"gst_cents"`
	BalanceCents      int64           `json:"balance_cents"`
	SupplierSnapshot  json.RawMessage `json:"supplier_snapshot"`
	Currency          string          `json:"currency"`
	FXRateMicro       int64           `json:"fx_rate_micro"`
}

// validBill returns the net line amounts, the net total, the GST total and an error code.
// GST is charged on the discounted net of each line, so a percentage discount never taxes money the
// supplier is not charging.
func validBill(in *billInput, gstRegistered bool, rateBP int64) ([]invoiceLineOut, int64, int64, string) {
	lines, total, gst, _, code := validBillRates(in, gstRegistered, rateBP, nil)
	return lines, total, gst, code
}

// validBillRates prices a bill against the active catalogue. catalogue may be nil for callers that
// only have the statutory standard rate; self-assessed codes then use their statutory rate.
func validBillRates(in *billInput, gstRegistered bool, rateBP int64, catalogue map[string]taxRate) ([]invoiceLineOut, int64, int64, int64, string) {
	var gst, selfGST int64
	in.SupplierReference = strings.TrimSpace(in.SupplierReference)
	in.DocumentNumber = strings.TrimSpace(in.DocumentNumber)
	if !validTaxText(in.PermitNumber, 50) {
		return nil, 0, 0, 0, "invalid_input"
	}
	if in.ContactID <= 0 || in.SupplierReference == "" || !utf8.ValidString(in.SupplierReference) || strings.ContainsRune(in.SupplierReference, 0) || utf8.RuneCountInString(in.SupplierReference) > 100 || !utf8.ValidString(in.Notes) || strings.ContainsRune(in.Notes, 0) || utf8.RuneCountInString(in.Notes) > 4000 || !validTaxText(in.OrderNumber, 100) || !validTaxText(in.Subject, 200) || !validTaxText(in.DocumentNumber, 50) || in.PaymentTermsDays < 0 || in.PaymentTermsDays > 365 {
		return nil, 0, 0, 0, "invalid_input"
	}
	issued, ok := parseDay(in.BillDate)
	due, ok2 := parseDay(in.DueDate)
	if !ok || !ok2 || due.Before(issued) {
		return nil, 0, 0, 0, "invalid_input"
	}
	if in.PlannedDate != "" {
		if _, okPlan := parseDay(in.PlannedDate); !okPlan {
			return nil, 0, 0, 0, "invalid_input"
		}
	}
	lines := make([]invoiceLineIn, len(in.Lines))
	for i, l := range in.Lines {
		if l.AccountID <= 0 && l.ItemID == 0 {
			return nil, 0, 0, 0, "invalid_account"
		}
		if l.DiscountCents < 0 || l.DiscountBasisPoints < 0 || l.DiscountBasisPoints > 10000 || (l.CustomerID != nil && *l.CustomerID <= 0) || (l.DiscountAccountID != nil && *l.DiscountAccountID <= 0) {
			return nil, 0, 0, 0, "invalid_input"
		}
		lines[i] = invoiceLineIn{Description: l.Description, Quantity: l.Quantity, UnitPriceCents: l.UnitPriceCents, Tracking: l.Tracking}
	}
	out, _, code := buildInvoiceLines(lines)
	if code != "" {
		return nil, 0, 0, 0, code
	}
	var total int64
	for i := range out {
		gross := out[i].AmountCents
		d := in.Lines[i].DiscountCents
		if bp := in.Lines[i].DiscountBasisPoints; bp > 0 {
			d = (gross*bp + 5000) / 10000
		}
		if d > gross {
			return nil, 0, 0, 0, "invalid_input"
		}
		in.Lines[i].DiscountCents = d
		out[i].AmountCents = gross - d
		code := strings.ToUpper(strings.TrimSpace(in.Lines[i].GSTCode))
		if code == "" {
			code = "NONE"
		}
		rate, kind, found := rateBP, "standard", code == "SR"
		if catalogue != nil {
			if row, exists := catalogue[code]; exists {
				rate, kind, found = row.RateBP, row.Kind, true
			}
		} else {
			switch code {
			case "ZR", "ES", "OS", "PE", "BD":
				rate, kind, found = 0, map[string]string{"ZR": "zero", "ES": "exempt", "OS": "out_of_scope", "PE": "exempt", "BD": "standard"}[code], true
			case "NONE":
				rate, kind, found = 0, "no_tax", true
				// IM, LVG and RC have no fixed rate here: they are priced only from the dated catalogue.
				// ponytail: without a catalogue (recurring-bill check) they are rejected, not guessed at 9%.
			}
		}
		if !found || !taxKindsSupportedOnDocuments[kind] {
			return nil, 0, 0, 0, "unsupported_gst"
		}
		out[i].GSTCode = code
		out[i].GSTRateBP = rate
		switch kind {
		case "no_tax":
			out[i].GSTCents = 0
		case "standard":
			if !gstRegistered {
				return nil, 0, 0, 0, "unsupported_gst"
			}
			out[i].GSTCents = (out[i].AmountCents*rate + 5000) / 10000
			gst += out[i].GSTCents
		case "import", "reverse_charge":
			if !gstRegistered {
				return nil, 0, 0, 0, "unsupported_gst"
			}
			out[i].GSTCents = (out[i].AmountCents*rate + 5000) / 10000
			selfGST += out[i].GSTCents
		default:
			if !gstRegistered {
				return nil, 0, 0, 0, "unsupported_gst"
			}
		}
		total += out[i].AmountCents
	}
	if total <= 0 || total+gst > maxMoneyCents || selfGST > maxMoneyCents {
		return nil, 0, 0, 0, "invalid_input"
	}
	return out, total, gst, selfGST, ""
}

// inventoryAccountID is the company's inventory control account, seeding reserved accounts when a bill is
// the first write of the company's life.
func inventoryAccountID(ctx context.Context, tx pgx.Tx, orgID int64) int64 {
	ids, err := accountIDs(ctx, tx, orgID)
	if err != nil {
		return 0
	}
	return ids[acctInventory]
}

// checkBillRefs confirms the supplier, accounts and customers on a bill belong to the company and have the right type.
// It returns an HTTP status and error code when a check fails, or an error for a database failure.
func checkBillRefs(ctx context.Context, tx pgx.Tx, orgID int64, in *billInput) (int, string, error) {
	var kind string
	err := tx.QueryRow(ctx, `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2`, orgID, in.ContactID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && kind == "customer") {
		return 400, "invalid_supplier", nil
	}
	if err != nil {
		return 0, "", err
	}
	expense := []int64{}
	if in.DiscountAccountID != nil {
		expense = append(expense, *in.DiscountAccountID)
	}
	for _, l := range in.Lines {
		if l.DiscountAccountID != nil {
			expense = append(expense, *l.DiscountAccountID)
		}
	}
	for i := range in.Lines {
		l := &in.Lines[i]
		if l.ItemID != 0 && l.AccountID != 0 {
			return 400, "invalid_item_account_combination", nil
		}
		if l.ItemID == 0 && l.AccountID <= 0 {
			return 400, "invalid_expense_account", nil
		}
		accountID := l.AccountID
		if l.ItemID != 0 {
			var typ string
			var purchaseAccount *int64
			var tracked bool
			err := tx.QueryRow(ctx, `SELECT item_type,purchase_account_id,track_inventory FROM items WHERE org_id=$1 AND id=$2`, orgID, l.ItemID).Scan(&typ, &purchaseAccount, &tracked)
			if errors.Is(err, pgx.ErrNoRows) {
				return 422, "invalid_item", nil
			}
			if err != nil {
				return 0, "", err
			}
			if typ != "service" && typ != "goods" {
				return 422, "invalid_item", nil
			}
			if tracked {
				// Tracked stock goes to the inventory control account, or the item's own override (D3).
				accountID = inventoryAccountID(ctx, tx, orgID)
				if purchaseAccount != nil {
					// The item may name its own inventory account; an expense account cannot hold stock value.
					var overrideType string
					if err := tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, orgID, *purchaseAccount).Scan(&overrideType); err == nil && overrideType == "asset" {
						accountID = *purchaseAccount
					}
				}
				if accountID == 0 {
					return 0, "", fmt.Errorf("inventory account missing for org %d", orgID)
				}
				l.AccountID = accountID
				continue
			}
			if purchaseAccount == nil {
				return 422, "item_purchase_account_required", nil
			}
			accountID = *purchaseAccount
			l.AccountID = accountID
		}
		var accountType string
		err := tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, orgID, accountID).Scan(&accountType)
		// Asset lines capitalise into the register (R10); everything else must be an expense.
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && accountType != "expense" && accountType != "asset") {
			return 400, "invalid_expense_account", nil
		}
		if err != nil {
			return 0, "", err
		}
	}
	// Discount accounts must be expense accounts; line accounts were checked above and may be assets.
	for _, id := range expense {
		var typ string
		err := tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, orgID, id).Scan(&typ)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && typ != "expense") {
			return 400, "invalid_expense_account", nil
		}
		if err != nil {
			return 0, "", err
		}
	}
	for _, l := range in.Lines {
		if l.CustomerID == nil {
			continue
		}
		var ck string
		err := tx.QueryRow(ctx, `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2`, orgID, *l.CustomerID).Scan(&ck)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && ck == "supplier") {
			return 422, "invalid_customer", nil
		}
		if err != nil {
			return 0, "", err
		}
	}
	return 0, "", nil
}

func (a *app) saveBill(w http.ResponseWriter, r *http.Request, u currentUser) {
	var id int64
	update := r.Method == http.MethodPut
	if update {
		var ok bool
		id, ok = pathID(w, r)
		if !ok {
			return
		}
	}
	var in billInput
	if !decodeJSON(w, r, &in) {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if !a.requireFeature(w, r.Context(), access.ID, u.ID, "purchases") {
		return
	}
	ctx := r.Context()
	issued, _ := parseDay(in.BillDate)
	var gstRegistered bool
	if err := a.db.QueryRow(ctx, `SELECT CASE WHEN EXISTS(SELECT 1 FROM gst_registrations WHERE org_id=$1) THEN EXISTS(SELECT 1 FROM gst_registrations WHERE org_id=$1 AND effective_from<=$2 AND (effective_to IS NULL OR effective_to>=$2)) ELSE gst_registered END FROM organizations WHERE id=$1`, access.ID, issued).Scan(&gstRegistered); err != nil {
		serverError(w, err)
		return
	}
	// A line without a tax code takes the default of the expense account it posts to.
	defaults, err := accountTaxDefaults(ctx, a.db, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	var contactTax string
	_ = a.db.QueryRow(ctx, `SELECT purchase_tax_code FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, in.ContactID).Scan(&contactTax)
	for i := range in.Lines {
		if strings.TrimSpace(in.Lines[i].GSTCode) == "" && contactTax != "" {
			in.Lines[i].GSTCode = contactTax
		}
		if strings.TrimSpace(in.Lines[i].GSTCode) == "" && in.Lines[i].AccountID > 0 {
			in.Lines[i].GSTCode = defaults[in.Lines[i].AccountID]
		}
	}
	rateBP, err := taxRateBPOn(ctx, a.db, "SR", "purchase", day(issued), func() int64 {
		rate, _ := gstRateBPOn(ctx, a.db, day(issued))
		return rate
	})
	if err != nil {
		serverError(w, err)
		return
	}
	catalogue, err := taxCatalogueOn(ctx, a.db, "purchase", in.BillDate)
	if err != nil {
		serverError(w, err)
		return
	}
	lines, net, gst, selfGST, code := validBillRates(&in, gstRegistered, rateBP, catalogue)
	if code != "" {
		status := 400
		if code == "unsupported_gst" {
			status = 422
		}
		writeError(w, status, code)
		return
	}
	total := net + gst
	currency, fxRate, ok := resolveDocumentCurrency(ctx, a.db, access.ID, in.Currency, in.FXRateMicro, in.BillDate)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_currency")
		return
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if st, code, err := checkBillRefs(ctx, tx, access.ID, &in); err != nil {
		serverError(w, err)
		return
	} else if code != "" {
		writeError(w, st, code)
		return
	}
	status, action := 201, "bill.created"
	if update {
		var state string
		err := tx.QueryRow(ctx, `SELECT status FROM bills WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&state)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if state != "draft" {
			writeError(w, 409, "bill_not_draft")
			return
		}
		if _, err := tx.Exec(ctx, `DELETE FROM bill_lines WHERE org_id=$1 AND bill_id=$2`, access.ID, id); err != nil {
			serverError(w, err)
			return
		}
		_, err = tx.Exec(ctx, `UPDATE bills SET contact_id=$3,supplier_reference=$4,bill_date=$5,due_date=$6,notes=$7,total_cents=$8,gst_cents=$9,self_gst_cents=$10,order_number=$11,subject=$12,payment_terms_days=$13,discount_account_id=$14,currency=$15,fx_rate_micro=$16,document_number=COALESCE(NULLIF($17,''),document_number),permit_number=$18,planned_date=NULLIF($19,'')::date WHERE org_id=$1 AND id=$2`, access.ID, id, in.ContactID, in.SupplierReference, in.BillDate, in.DueDate, in.Notes, total, gst, selfGST, in.OrderNumber, in.Subject, in.PaymentTermsDays, in.DiscountAccountID, currency, fxRate, in.DocumentNumber, in.PermitNumber, in.PlannedDate)
		status, action = 200, "bill.updated"
	} else {
		err = tx.QueryRow(ctx, `INSERT INTO bills(org_id,contact_id,supplier_reference,bill_date,due_date,notes,total_cents,gst_cents,self_gst_cents,created_by,order_number,subject,payment_terms_days,discount_account_id,currency,fx_rate_micro,document_number,permit_number,planned_date) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,NULLIF($17,''),$18,NULLIF($19,'')::date) RETURNING id`, access.ID, in.ContactID, in.SupplierReference, in.BillDate, in.DueDate, in.Notes, total, gst, selfGST, u.ID, in.OrderNumber, in.Subject, in.PaymentTermsDays, in.DiscountAccountID, currency, fxRate, in.DocumentNumber, in.PermitNumber, in.PlannedDate).Scan(&id)
	}
	if err != nil {
		if isUniqueViolation(err) {
			if strings.Contains(err.Error(), "bills_document_number_uq") {
				writeError(w, 409, "duplicate_number")
			} else {
				writeError(w, 409, "bill_reference_exists")
			}
		} else {
			serverError(w, err)
		}
		return
	}
	for i, l := range lines {
		// A line cannot name an archived option or another company's option.
		if okTrack, terr := validTracking(ctx, tx, access.ID, l.Tracking); terr != nil {
			serverError(w, terr)
			return
		} else if !okTrack {
			writeError(w, 422, "invalid_tracking")
			return
		}
		if _, err := tx.Exec(ctx, `INSERT INTO bill_lines(org_id,bill_id,line_no,account_id,description,quantity,unit_price_cents,amount_cents,discount_cents,discount_basis_points,customer_id,discount_account_id,item_id,gst_code,gst_rate_bp,gst_cents,tracking) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULLIF($13::bigint,0),$14,$15,$16,$17)`, access.ID, id, l.LineNo, in.Lines[i].AccountID, l.Description, l.Quantity, l.UnitPriceCents, l.AmountCents, in.Lines[i].DiscountCents, in.Lines[i].DiscountBasisPoints, in.Lines[i].CustomerID, in.Lines[i].DiscountAccountID, in.Lines[i].ItemID, l.GSTCode, l.GSTRateBP, l.GSTCents, trackingJSON(l.Tracking)); err != nil {
			serverError(w, err)
			return
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,$3)`, access.ID, u.ID, action); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, status, map[string]any{"id": id, "status": "draft", "total_cents": total, "gst_cents": gst, "self_gst_cents": selfGST})
}

const billSelect = `SELECT b.id,b.document_number,b.contact_id,CASE WHEN b.status='posted' THEN COALESCE(b.supplier_snapshot->>'name','') ELSE c.name END,b.supplier_reference,b.bill_date,b.due_date,b.notes,b.order_number,b.subject,b.payment_terms_days,b.discount_account_id,b.permit_number,COALESCE(b.planned_date::text,''),b.status,b.total_cents,b.gst_cents,CASE WHEN EXISTS (SELECT 1 FROM document_reversals r WHERE r.org_id=b.org_id AND r.doc_kind='bill' AND r.doc_id=b.id) THEN 0 ELSE b.total_cents-COALESCE((SELECT sum(p.amount_cents) FROM bill_payments p WHERE p.org_id=b.org_id AND p.bill_id=b.id AND NOT EXISTS (SELECT 1 FROM document_reversals r WHERE r.org_id=p.org_id AND r.doc_kind='bill_payment' AND r.doc_id=p.id)),0)-COALESCE((SELECT sum(sc.total_cents) FROM supplier_credits sc WHERE sc.org_id=b.org_id AND sc.bill_id=b.id),0) END AS balance_cents,b.supplier_snapshot,b.currency,b.fx_rate_micro FROM bills b JOIN contacts c ON c.org_id=b.org_id AND c.id=b.contact_id`

func scanBill(s scanner) (billOut, error) {
	var b billOut
	var issued, due time.Time
	err := s.Scan(&b.ID, &b.DocumentNumber, &b.ContactID, &b.SupplierName, &b.SupplierReference, &issued, &due, &b.Notes, &b.OrderNumber, &b.Subject, &b.PaymentTermsDays, &b.DiscountAccountID, &b.PermitNumber, &b.PlannedDate, &b.Status, &b.TotalCents, &b.GSTCents, &b.BalanceCents, &b.SupplierSnapshot, &b.Currency, &b.FXRateMicro, &b.LifecycleStatus)
	b.BillDate, b.DueDate = day(issued), day(due)
	b.Paid = b.Status == "posted" && b.BalanceCents <= 0
	return b, err
}
func (a *app) listBills(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if !validLifecycle(status) {
		writeError(w, 400, "invalid_input")
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT * FROM (SELECT d.*, `+lifecycleCase+` FROM (`+billSelect+` WHERE b.org_id=$1) d) x
		WHERE ($2 = '' OR x.lifecycle = $2 OR ($2 = 'posted' AND x.status = 'posted') OR ($2 IN ('draft','awaiting_approval','voided') AND x.status = $2))
		ORDER BY x.id DESC LIMIT 200`, access.ID, status)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []billOut{}
	for rows.Next() {
		b, err := scanBill(rows)
		if err != nil {
			serverError(w, err)
			return
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	counts, err := lifecycleCounts(r.Context(), a.db, `SELECT d.*, `+lifecycleCase+` FROM (`+billSelect+` WHERE b.org_id=$1) d`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"bills": out, "counts": counts, "forwarding_address": billsForwardingAddress(access.ID)})
}
func (a *app) getBill(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	ctx := r.Context()
	b, err := scanBill(a.db.QueryRow(ctx, `SELECT d.*, `+lifecycleCase+` FROM (`+billSelect+` WHERE b.org_id=$1 AND b.id=$2) d`, access.ID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	rows, err := a.db.Query(ctx, `SELECT l.line_no,l.description,l.quantity,l.unit_price_cents,l.amount_cents,l.account_id,a.code,a.name,l.discount_cents,l.discount_basis_points,l.customer_id,l.discount_account_id,l.item_id,l.gst_code,l.gst_rate_bp,l.gst_cents,l.tracking FROM bill_lines l JOIN accounts a ON a.org_id=l.org_id AND a.id=l.account_id WHERE l.org_id=$1 AND l.bill_id=$2 ORDER BY l.line_no`, access.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	lines := []billLineOut{}
	for rows.Next() {
		var l billLineOut
		var itemID *int64
		var tracking json.RawMessage
		if err := rows.Scan(&l.LineNo, &l.Description, &l.Quantity, &l.UnitPriceCents, &l.AmountCents, &l.AccountID, &l.AccountCode, &l.AccountName, &l.DiscountCents, &l.DiscountBasisPoints, &l.CustomerID, &l.DiscountAccountID, &itemID, &l.GSTCode, &l.GSTRateBP, &l.GSTCents, &tracking); err != nil {
			rows.Close()
			serverError(w, err)
			return
		}
		l.Tracking = map[string]int64{}
		_ = json.Unmarshal(tracking, &l.Tracking)
		if itemID != nil {
			l.ItemID = *itemID
		}
		l.GrossCents = l.AmountCents + l.DiscountCents
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	rows, err = a.db.Query(ctx, `SELECT id,amount_cents,payment_date,reference,journal_id FROM bill_payments WHERE org_id=$1 AND bill_id=$2 ORDER BY payment_date,id`, access.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	payments := []map[string]any{}
	for rows.Next() {
		var pid, amount, journal int64
		var date time.Time
		var ref string
		if err := rows.Scan(&pid, &amount, &date, &ref, &journal); err != nil {
			serverError(w, err)
			return
		}
		payments = append(payments, map[string]any{"id": pid, "amount_cents": amount, "payment_date": day(date), "reference": ref, "journal_id": journal})
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"bill": b, "lines": lines, "payments": payments})
}
func (a *app) postBill(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, true)
	if !ok {
		return
	}
	if allowed, err := canPostDocuments(r.Context(), a.db, access.ID, u.ID); err != nil {
		serverError(w, err)
		return
	} else if !allowed {
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
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	// The importer calls the same function, so a bill posted either way goes through one path.
	result, code, err := postBillTx(ctx, tx, access.ID, u.ID, id, nil, false)
	if err != nil {
		serverError(w, err)
		return
	}
	if code != "" {
		writeError(w, statusForCode(code), code)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "journal_id": result.Journal, "status": "posted"})
}

type billPaymentInput struct {
	paymentIn
	Reference              string           `json:"reference"`
	Overpayment            bool             `json:"overpayment"`
	OverpaymentDescription string           `json:"overpayment_description"`
	Tracking               map[string]int64 `json:"tracking"`
}

func (a *app) payBill(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in billPaymentInput
	if !decodeJSON(w, r, &in) {
		return
	}
	date, ok := parseDay(in.PaymentDate)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	in.OverpaymentDescription = strings.TrimSpace(in.OverpaymentDescription)
	if !ok || in.AmountCents <= 0 || in.AmountCents > maxMoneyCents || in.IdempotencyKey == "" || len(in.IdempotencyKey) > 90 || strings.ContainsRune(in.IdempotencyKey, 0) || !utf8.ValidString(in.Reference) || strings.ContainsRune(in.Reference, 0) || utf8.RuneCountInString(in.Reference) > 100 || !validTaxText(in.OverpaymentDescription, 500) {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if !a.requireFeature(w, r.Context(), access.ID, u.ID, "purchases") {
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, strconv.FormatInt(access.ID, 10)+":bill:"+in.IdempotencyKey); err != nil {
		serverError(w, err)
		return
	}
	// Keep company lock before bill lock, as in allocated payments.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	var state string
	var total, paid int64
	var issued time.Time
	err = tx.QueryRow(ctx, `SELECT status,total_cents,bill_date FROM bills WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&state, &total, &issued)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if state != "posted" {
		writeError(w, 422, "bill_not_posted")
		return
	}
	var credited int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(total_cents),0) FROM supplier_credits WHERE org_id=$1 AND bill_id=$2`, access.ID, id).Scan(&credited); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount_cents),0) FROM bill_payments p WHERE p.org_id=$1 AND p.bill_id=$2
		AND NOT EXISTS (SELECT 1 FROM document_reversals r WHERE r.org_id=p.org_id AND r.doc_kind='bill_payment' AND r.doc_id=p.id)`, access.ID, id).Scan(&paid); err != nil {
		serverError(w, err)
		return
	}
	// A credit already reduced the bill, so payments can only cover the rest.
	paid += credited
	// An amount above the balance is refused unless the payer explicitly confirms the overpayment
	// and says what it is for; nothing silently becomes a supplier credit.
	remaining := total - paid
	if in.AmountCents > remaining {
		if !in.Overpayment {
			writeError(w, 422, "overpayment_requires_confirmation")
			return
		}
		if in.OverpaymentDescription == "" {
			writeError(w, 400, "invalid_input")
			return
		}
	}
	var over *billOverpayment
	if in.AmountCents > remaining {
		if okTrack, terr := validTracking(ctx, tx, access.ID, in.Tracking); terr != nil {
			serverError(w, terr)
			return
		} else if !okTrack {
			writeError(w, 422, "invalid_tracking")
			return
		}
		over = &billOverpayment{Description: in.OverpaymentDescription, Tracking: in.Tracking}
	}
	canonical, _ := json.Marshal([]any{id, in.AmountCents, in.PaymentDate, in.Reference, in.Overpayment, in.OverpaymentDescription, in.Tracking})
	hash := sha256.Sum256(canonical)
	var pid, jid int64
	var savedHash []byte
	err = tx.QueryRow(ctx, `SELECT id,journal_id,request_hash FROM bill_payments WHERE org_id=$1 AND idempotency_key=$2`, access.ID, in.IdempotencyKey).Scan(&pid, &jid, &savedHash)
	if err == nil {
		if !bytes.Equal(hash[:], savedHash) {
			writeError(w, 409, "idempotency_conflict")
			return
		}
		writeJSON(w, 200, map[string]any{"payment_id": pid, "journal_id": jid, "balance_cents": total - paid})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	// The importer calls the same function, so a supplier payment posted either way goes through one path.
	var code string
	pid, jid, code, err = payBillTx(ctx, tx, access.ID, u.ID, id, in.AmountCents, date, in.Reference, in.IdempotencyKey, hash, in.SettlementRateMicro, nil, false, over)
	if err != nil {
		serverError(w, err)
		return
	}
	if code != "" {
		writeError(w, statusForCode(code), code)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"payment_id": pid, "journal_id": jid, "balance_cents": total - paid - in.AmountCents})
}
