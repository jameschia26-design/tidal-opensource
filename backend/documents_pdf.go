package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// documentPDF renders invoices, quotes and bills as a plain PDF for printing or manual sending.
// It reads through the same company-scoped queries as the detail screens, so access rules are unchanged.
// Drafts are labelled DRAFT; posted or sent documents show their frozen party details.
func (a *app) documentPDF(w http.ResponseWriter, r *http.Request, u currentUser) {
	kind := r.PathValue("kind")
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	ctx := r.Context()
	var title, number, status, party, dateLine, notes, terms, deliveryAddress string
	var gstTotal int64
	var total int64
	var lines [][4]string // description, quantity, unit price, amount
	var commercial commercialFields
	var themeID int64
	var company string
	var logo []byte
	if err := a.db.QueryRow(ctx, `SELECT COALESCE(NULLIF(legal_name,''),name), logo_bytes FROM organizations WHERE id=$1`, access.ID).Scan(&company, &logo); err != nil {
		serverError(w, err)
		return
	}
	switch kind {
	case "invoices":
		inv, err := scanInvoice(a.db.QueryRow(ctx, `SELECT d.*, `+lifecycleCase+` FROM (`+invoiceSelect+` WHERE i.org_id=$1 AND i.id=$2) d`, access.ID, id))
		if err != nil {
			pdfQueryError(w, err)
			return
		}
		title = "Invoice"
		number = "Draft"
		if inv.Number != nil {
			number = strconv.FormatInt(*inv.Number, 10)
		}
		status, total = inv.Status, inv.TotalCents
		themeID = inv.BrandingThemeID
		party = partyFromSnapshot(inv.CustomerSnapshot, "name", "company_name")
		dateLine = "Invoice date " + inv.InvoiceDate + "   Due " + inv.DueDate
		notes, terms = inv.Notes, inv.Terms
		if err := json.Unmarshal(inv.Commercial, &commercial); err != nil {
			serverError(w, err)
			return
		}
		notes = documentNotes(notes, commercial.Subject)
		rows, err := a.db.Query(ctx, `SELECT description,quantity,unit_price_cents,amount_cents FROM invoice_lines WHERE org_id=$1 AND invoice_id=$2 ORDER BY line_no`, access.ID, id)
		if err != nil {
			serverError(w, err)
			return
		}
		lines, err = pdfLinesFrom(rows)
		if err != nil {
			serverError(w, err)
			return
		}
	case "quotes":
		q, err := scanQuote(a.db.QueryRow(ctx, quoteSelect+` WHERE q.org_id=$1 AND q.id=$2`, access.ID, id))
		if err != nil {
			pdfQueryError(w, err)
			return
		}
		title = "Quotation"
		number = strconv.FormatInt(q.ID, 10)
		status, total = q.Status, q.TotalCents
		party = partyFromSnapshot(q.CustomerSnapshot, "name", "company_name")
		if party == "" {
			party = q.CustomerName
		}
		dateLine = "Quote date " + q.QuoteDate + "   Valid until " + q.ExpiryDate
		notes, terms = q.Notes, q.Terms
		if err := json.Unmarshal(q.Commercial, &commercial); err != nil {
			serverError(w, err)
			return
		}
		notes = documentNotes(notes, commercial.Subject)
		if lines, err = storedLines(q.Lines); err != nil {
			serverError(w, err)
			return
		}
		if gstTotal, err = storedGST(q.Lines); err != nil {
			serverError(w, err)
			return
		}
	case "bills":
		b, err := scanBill(a.db.QueryRow(ctx, `SELECT d.*, `+lifecycleCase+` FROM (`+billSelect+` WHERE b.org_id=$1 AND b.id=$2) d`, access.ID, id))
		if err != nil {
			pdfQueryError(w, err)
			return
		}
		title = "Supplier bill"
		number = b.SupplierReference
		status, total = b.Status, b.TotalCents
		party = partyFromSnapshot(b.SupplierSnapshot, "name", "company_name")
		if party == "" {
			party = b.SupplierName
		}
		dateLine = "Bill date " + b.BillDate + "   Due " + b.DueDate
		rows, err := a.db.Query(ctx, `SELECT l.description,l.quantity,l.unit_price_cents,l.amount_cents FROM bill_lines l WHERE l.org_id=$1 AND l.bill_id=$2 ORDER BY l.line_no`, access.ID, id)
		if err != nil {
			serverError(w, err)
			return
		}
		lines, err = pdfLinesFrom(rows)
		if err != nil {
			serverError(w, err)
			return
		}
	case "purchase-orders", "sales-receipts", "customer-credit-notes":
		var raw, rawCommercial []byte
		var date, extra string
		var err error
		switch kind {
		case "purchase-orders":
			title = "Purchase order"
			err = a.db.QueryRow(ctx, `SELECT p.po_number,p.po_date::text,'Delivery '||p.delivery_date::text,c.name,p.lines,p.commercial,p.total_cents,p.notes,p.terms,p.status,p.delivery_address FROM purchase_orders p JOIN contacts c ON c.org_id=p.org_id AND c.id=p.contact_id WHERE p.org_id=$1 AND p.id=$2`, access.ID, id).Scan(&number, &date, &extra, &party, &raw, &rawCommercial, &total, &notes, &terms, &status, &deliveryAddress)
			dateLine = "Order date " + date + "   " + extra
		case "sales-receipts":
			title = "Sales receipt"
			err = a.db.QueryRow(ctx, `SELECT r.receipt_number,r.receipt_date::text,'Paid by '||replace(r.payment_mode,'_',' '),c.name,r.lines,r.commercial,r.total_cents,r.notes,r.terms,'posted' FROM sales_receipts r JOIN contacts c ON c.org_id=r.org_id AND c.id=r.contact_id WHERE r.org_id=$1 AND r.id=$2`, access.ID, id).Scan(&number, &date, &extra, &party, &raw, &rawCommercial, &total, &notes, &terms, &status)
			dateLine = "Receipt date " + date + "   " + extra
		default:
			title = "Credit note"
			err = a.db.QueryRow(ctx, `SELECT n.credit_number,n.credit_date::text,'',c.name,n.lines,n.commercial,n.total_cents,n.notes,n.terms,n.status FROM customer_credit_notes n JOIN contacts c ON c.org_id=n.org_id AND c.id=n.contact_id WHERE n.org_id=$1 AND n.id=$2`, access.ID, id).Scan(&number, &date, &extra, &party, &raw, &rawCommercial, &total, &notes, &terms, &status)
			dateLine = "Credit date " + date
		}
		if err != nil {
			pdfQueryError(w, err)
			return
		}
		if err := json.Unmarshal(rawCommercial, &commercial); err != nil {
			serverError(w, err)
			return
		}
		if lines, err = storedLines(raw); err != nil {
			serverError(w, err)
			return
		}
		if gstTotal, err = storedGST(raw); err != nil {
			serverError(w, err)
			return
		}
		notes = documentNotes(notes, commercial.Subject)
	case "credit-notes":
		var cn struct {
			ID         int64
			InvoiceID  int64
			CreditDate string
			Reason     string
			Amount     int64
			Customer   string
		}
		err := a.db.QueryRow(ctx, `SELECT n.id, n.invoice_id, n.credit_date::text, n.reason, n.amount_cents, COALESCE(NULLIF(i.customer_snapshot->>'company_name',''), c.name)
			FROM credit_notes n JOIN invoices i ON i.org_id=n.org_id AND i.id=n.invoice_id JOIN contacts c ON c.org_id=n.org_id AND c.id=n.contact_id
			WHERE n.org_id=$1 AND n.id=$2`, access.ID, id).Scan(&cn.ID, &cn.InvoiceID, &cn.CreditDate, &cn.Reason, &cn.Amount, &cn.Customer)
		if err != nil {
			pdfQueryError(w, err)
			return
		}
		title = "Credit note"
		number = strconv.FormatInt(cn.ID, 10)
		status, total = "posted", cn.Amount
		party = cn.Customer
		dateLine = "Credit date " + cn.CreditDate + "   Against invoice " + strconv.FormatInt(cn.InvoiceID, 10)
		notes = cn.Reason
		lines = [][4]string{{"Credit for " + cn.Reason, "1", pdfMoney(cn.Amount), pdfMoney(cn.Amount)}}
	default:
		writeError(w, http.StatusNotFound, "not_found")
		return
	}

	template := r.URL.Query().Get("template")
	if template == "" {
		template = "standard"
	}
	if template != "standard" && template != "detailed" {
		writeError(w, 400, "invalid_input")
		return
	}
	d := newPDF()
	if len(logo) > 0 {
		if img, err := decodeLogo(logo); err == nil {
			d.logo = img
		}
	}
	d.line(18, true, title+" "+number)
	if status == "draft" {
		d.line(11, true, "DRAFT - not final. Review before sending.")
	}
	d.gap(6)
	d.line(11, true, company)
	d.line(10, false, dateLine)
	d.line(10, false, "Customer / supplier: "+party)
	if kind == "purchase-orders" {
		for _, line := range strings.Split(deliveryAddress, "\n") {
			if line != "" {
				d.line(10, false, "Delivery address: "+line)
			}
		}
		if commercial.Attention != "" {
			d.line(10, false, "Attention: "+commercial.Attention)
		}
		if commercial.Telephone != "" {
			d.line(10, false, "Telephone: "+commercial.Telephone)
		}
		if commercial.DeliveryInstructions != "" {
			d.line(10, false, "Delivery instructions: "+commercial.DeliveryInstructions)
		}
	}
	d.gap(10)
	d.line(10, true, fmt.Sprintf("%-60s %8s %12s %12s", "Description", "Qty", "Unit (SGD)", "Amount (SGD)"))
	for _, l := range lines {
		d.line(10, false, fmt.Sprintf("%-60.60s %8s %12s %12s", l[0], l[1], l[2], l[3]))
	}
	d.gap(8)
	if template == "detailed" && kind == "bills" {
		d.line(10, false, "Status: "+status+"   Supplier reference: "+dash(number))
	} else if template == "detailed" {
		d.line(10, false, "Salesperson: "+dash(commercial.Salesperson)+"   Order number: "+dash(commercial.OrderNumber)+"   Payment terms: "+termsText(commercial.PaymentTermsDays))
		if commercial.DiscountType == "percent" {
			d.line(10, false, fmt.Sprintf("Discount: %.2f%%", float64(commercial.DiscountBasisPoints)/100))
		} else if commercial.DiscountCents != 0 {
			d.line(10, false, "Discount: SGD "+pdfMoney(commercial.DiscountCents))
		}
		if commercial.ShippingCents != 0 {
			d.line(10, false, "Shipping: SGD "+pdfMoney(commercial.ShippingCents))
		}
		if commercial.AdjustmentCents != 0 {
			d.line(10, false, "Adjustment ("+dash(commercial.AdjustmentDescription)+"): SGD "+pdfMoney(commercial.AdjustmentCents))
		}
	}
	if gstTotal > 0 {
		d.line(10, false, "GST SGD "+pdfMoney(gstTotal))
	}
	d.line(11, true, "Total SGD "+pdfMoney(total))
	if gstTotal > 0 {
		d.line(9, false, "All amounts in Singapore dollars. Total includes GST. This document is not a GST tax invoice.")
	} else {
		d.line(9, false, "All amounts in Singapore dollars. No GST shown: this document is not a GST tax invoice.")
	}
	if notes != "" {
		d.gap(8)
		d.line(10, true, "Notes")
		for _, part := range strings.Split(notes, "\n") {
			d.line(10, false, part)
		}
	}
	if terms != "" {
		d.gap(8)
		d.line(10, true, "Terms")
		for _, part := range strings.Split(terms, "\n") {
			d.line(10, false, part)
		}
	}
	var footer string
	footerSQL := `SELECT footer FROM branding_themes WHERE org_id=$1 AND is_default`
	if themeID > 0 {
		footerSQL = `SELECT footer FROM branding_themes WHERE org_id=$1 AND id=$2`
	}
	var footerArgs []any
	if themeID > 0 {
		footerArgs = []any{access.ID, themeID}
	} else {
		footerArgs = []any{access.ID}
	}
	if err := a.db.QueryRow(ctx, footerSQL, footerArgs...).Scan(&footer); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	if footer != "" {
		d.gap(10)
		for _, part := range strings.Split(footer, "\n") {
			d.line(9, false, part)
		}
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.pdf"`, kind, pdfFileToken(number)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(d.Bytes())
}

// pdfLinesFrom reads (description, quantity, unit, amount) rows into printable strings.
func pdfLinesFrom(rows pgx.Rows) ([][4]string, error) {
	defer rows.Close()
	out := [][4]string{}
	for rows.Next() {
		var desc string
		var qty, unit, amt int64
		if err := rows.Scan(&desc, &qty, &unit, &amt); err != nil {
			return nil, err
		}
		out = append(out, [4]string{desc, strconv.FormatInt(qty, 10), pdfMoney(unit), pdfMoney(amt)})
	}
	return out, rows.Err()
}

func pdfQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	serverError(w, err)
}

func pdfFileToken(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "document"
	}
	return b.String()
}

func partyFromSnapshot(raw []byte, nameKey, companyKey string) string {
	if len(raw) == 0 || string(raw) == "{}" || string(raw) == "null" {
		return ""
	}
	type snap struct {
		Name    string `json:"name"`
		Company string `json:"company_name"`
	}
	var s snap
	_ = json.Unmarshal(raw, &s)
	if s.Company != "" {
		return s.Company
	}
	return s.Name
}

func pdfMoney(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func termsText(days int) string {
	if days == 0 {
		return "Due on receipt"
	}
	return strconv.Itoa(days) + " days"
}

// storedLines turns saved line JSON (quotes, receipts, credit notes, orders) into printable rows.
func storedLines(raw []byte) ([][4]string, error) {
	var stored []struct {
		Description    string `json:"description"`
		Quantity       int64  `json:"quantity"`
		UnitPriceCents int64  `json:"unit_price_cents"`
		AmountCents    int64  `json:"amount_cents"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, err
	}
	out := make([][4]string, 0, len(stored))
	for _, l := range stored {
		out = append(out, [4]string{l.Description, strconv.FormatInt(l.Quantity, 10), pdfMoney(l.UnitPriceCents), pdfMoney(l.AmountCents)})
	}
	return out, nil
}

// storedGST sums per-line GST kept in stored document lines; older rows have none.
func storedGST(raw []byte) (int64, error) {
	var stored []struct {
		GSTCents int64 `json:"gst_cents"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return 0, err
	}
	var sum int64
	for _, l := range stored {
		sum += l.GSTCents
	}
	return sum, nil
}
