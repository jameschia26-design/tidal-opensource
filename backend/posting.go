package main

// Shared document posting. The HTTP routes and the Peachtree importer both call these functions, so
// an imported document goes through the same numbering, status change, snapshots, audit trail and GL
// rules as one typed into the app.
//
// The importer passes reuseJournal because the source journals are already posted: posting them a
// second time would double the ledger. It also passes historical because live-only guards (credit
// limit, stock on hand) would wrongly refuse a faithful copy of an old document.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// postResult is what posting a document returns: its number and the journal that carries it.
type postResult struct {
	Number  int64
	Journal int64
}

// postInvoiceTx posts one draft invoice. A non-nil reuseJournal reuses a journal that already exists
// instead of creating a new one, which is how an imported document avoids a second GL entry.
func postInvoiceTx(ctx context.Context, tx pgx.Tx, orgID, userID, invID int64, reuseJournal *int64, historical bool) (postResult, string, error) {
	var status string
	var total int64
	var idate time.Time
	var number, journalID *int64
	var contactID int64
	err := tx.QueryRow(ctx,
		`SELECT status, total_cents, invoice_date, number, journal_id, contact_id FROM invoices
		 WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, invID).Scan(&status, &total, &idate, &number, &journalID, &contactID)
	if err == pgx.ErrNoRows {
		return postResult{}, "not_found", nil
	}
	if err != nil {
		return postResult{}, "", err
	}
	if status == "posted" {
		return postResult{Number: *number, Journal: *journalID}, "", nil
	}
	if status != "draft" && status != "awaiting_approval" {
		return postResult{}, "invoice_not_draft", nil
	}
	// A contact can block new invoices once this one would take the balance past the credit limit.
	// Historical documents skip this: the invoice already happened.
	if !historical {
		var limitCents int64
		var blockOver bool
		if err := tx.QueryRow(ctx, `SELECT credit_limit_cents, block_over_limit FROM contacts WHERE org_id=$1 AND id=$2`, orgID, contactID).Scan(&limitCents, &blockOver); err != nil {
			return postResult{}, "", err
		}
		if blockOver && limitCents > 0 {
			var outstanding int64
			if err := tx.QueryRow(ctx, `SELECT
				COALESCE((SELECT SUM(i.total_cents) FROM invoices i WHERE i.org_id=$1 AND i.contact_id=$2 AND i.status='posted'),0)
				- COALESCE((SELECT SUM(p.amount_cents) FROM payments p JOIN invoices i ON i.org_id=p.org_id AND i.id=p.invoice_id WHERE i.org_id=$1 AND i.contact_id=$2),0)
				- COALESCE((SELECT SUM(c.total_cents) FROM customer_credit_notes c WHERE c.org_id=$1 AND c.contact_id=$2 AND c.status='open'),0)`,
				orgID, contactID).Scan(&outstanding); err != nil {
				return postResult{}, "", err
			}
			if outstanding+total > limitCents {
				return postResult{}, "credit_limit_exceeded", nil
			}
		}
	}
	// Counter row lock keeps numbers gapless: a rolled-back post also rolls back its number.
	if _, err := tx.Exec(ctx, `INSERT INTO core_counters (org_id) VALUES ($1) ON CONFLICT (org_id) DO NOTHING`, orgID); err != nil {
		return postResult{}, "", err
	}
	var next int64
	if err := tx.QueryRow(ctx, `SELECT invoice_next FROM core_counters WHERE org_id=$1 FOR UPDATE`, orgID).Scan(&next); err != nil {
		return postResult{}, "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE core_counters SET invoice_next = invoice_next + 1 WHERE org_id=$1`, orgID); err != nil {
		return postResult{}, "", err
	}
	jid := int64(0)
	if reuseJournal != nil {
		jid = *reuseJournal
	} else {
		acct, err := accountIDs(ctx, tx, orgID)
		if err != nil {
			return postResult{}, "", err
		}
		// Only stock-tracked items need stock on hand; services and untracked goods invoice freely.
		var itemsTracked []struct{ id, qty int64 }
		rows, qerr := tx.Query(ctx, `SELECT il.item_id, SUM(il.quantity)::bigint FROM invoice_lines il
			JOIN items i ON i.org_id=il.org_id AND i.id=il.item_id
			WHERE il.org_id=$1 AND il.invoice_id=$2 AND i.track_inventory GROUP BY il.item_id`, orgID, invID)
		if qerr != nil {
			return postResult{}, "", qerr
		}
		for rows.Next() {
			var iid, qty int64
			if serr := rows.Scan(&iid, &qty); serr != nil {
				rows.Close()
				return postResult{}, "", serr
			}
			itemsTracked = append(itemsTracked, struct{ id, qty int64 }{iid, qty})
		}
		rows.Close()
		if rerr := rows.Err(); rerr != nil {
			return postResult{}, "", rerr
		}
		// Stock shortfalls are recorded as backorders after the movement is written; the invoice still
		// posts in full because the customer has been promised the goods.
		hash := sha256.Sum256([]byte(fmt.Sprintf("invoice-post|%d|%d", invID, total)))
		credits, err := invoiceCredits(ctx, tx, orgID, invID, acct[acctSales], acct["2300"], total)
		if err != nil {
			return postResult{}, "", err
		}
		// Cost of goods sold leaves stock at the moving-average cost; the same cents credit Inventory.
		moves, merr := documentStockMoves(ctx, tx, orgID, "invoice", invID, day(idate))
		if merr != nil {
			return postResult{}, "", merr
		}
		debits := []accountAmount{{acct[acctAR], total, ""}}
		var cogs int64
		for _, m := range moves {
			if value := -m.qty * m.cost; value > 0 {
				cogs += value
				debits = append(debits, accountAmount{acct[acctCOGS], value, ""})
			}
		}
		if cogs > 0 {
			credits = append(credits, accountAmount{acct[acctInventory], cogs, ""})
		}
		jid, err = postJournalLegs(ctx, tx, orgID, userID, idate,
			"Invoice "+strconv.FormatInt(next, 10), "invoice-post:"+strconv.FormatInt(invID, 10), hash,
			debits, credits)
		if err != nil {
			return postResult{}, "", err
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE invoices i SET status='posted', number=$3, journal_id=$4, posted_at=now(), document_number=COALESCE(document_number, $5::text),
         seller_snapshot=(SELECT jsonb_build_object('name',o.name,'legal_name',o.legal_name,'uen',o.uen,'contact_email',o.contact_email,'phone',o.phone,'address',o.address) FROM organizations o WHERE o.id=i.org_id),
         customer_snapshot=(SELECT jsonb_build_object('name',c.name,'company_name',c.company_name,'uen',c.uen,'email',c.email,'phone',c.phone,'billing_address',c.billing_address,'shipping_address',c.shipping_address) FROM contacts c WHERE c.org_id=i.org_id AND c.id=i.contact_id)
		 WHERE i.org_id=$1 AND i.id=$2`, orgID, invID, next, jid, strconv.FormatInt(next, 10)); err != nil {
		return postResult{}, "", err
	}
	if err := recordAudit(ctx, tx, orgID, userID, "invoice", invID, "invoice.posted", nil); err != nil {
		return postResult{}, "", err
	}
	if !historical {
		// Backorders need the stock position before this invoice moves it.
		if err := recordBackorders(ctx, tx, orgID, invID, day(idate)); err != nil {
			return postResult{}, "", err
		}
		if err := postStockMovements(ctx, tx, orgID, "invoice", invID, idate); err != nil {
			return postResult{}, "", err
		}
	}
	return postResult{Number: next, Journal: jid}, "", nil
}

// postBillTx posts one draft supplier bill. A non-nil reuseJournal reuses an existing journal.
func postBillTx(ctx context.Context, tx pgx.Tx, orgID, userID, billID int64, reuseJournal *int64, historical bool) (postResult, string, error) {
	var state, ref string
	var total, gst, selfGST int64
	var date time.Time
	var jid *int64
	err := tx.QueryRow(ctx, `SELECT status,supplier_reference,total_cents,bill_date,journal_id,gst_cents,self_gst_cents FROM bills WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, billID).Scan(&state, &ref, &total, &date, &jid, &gst, &selfGST)
	if err == pgx.ErrNoRows {
		return postResult{}, "not_found", nil
	}
	if err != nil {
		return postResult{}, "", err
	}
	if state == "posted" {
		return postResult{Journal: *jid}, "", nil
	}
	if state != "draft" && state != "awaiting_approval" {
		return postResult{}, "bill_not_draft", nil
	}
	journal := int64(0)
	if reuseJournal != nil {
		journal = *reuseJournal
	} else {
		accounts, err := accountIDs(ctx, tx, orgID)
		if err != nil {
			return postResult{}, "", err
		}
		hash := sha256.Sum256([]byte("bill-post:" + strconv.FormatInt(billID, 10)))
		err = tx.QueryRow(ctx, `INSERT INTO journals(org_id,entry_date,memo,idempotency_key,request_hash,created_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, orgID, date, "Supplier bill "+ref, "bill-post:"+strconv.FormatInt(billID, 10), hash[:], userID).Scan(&journal)
		if err != nil {
			return postResult{}, "", err
		}
		if err := postBillLedgerLines(ctx, tx, orgID, billID, journal, total, gst, selfGST, date, accounts); err != nil {
			return postResult{}, "", err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE bills b SET status='posted',journal_id=$3,posted_at=now(),supplier_snapshot=(SELECT jsonb_build_object('name',c.name,'company_name',c.company_name,'uen',c.uen,'email',c.email,'phone',c.phone,'billing_address',c.billing_address) FROM contacts c WHERE c.org_id=b.org_id AND c.id=b.contact_id) WHERE org_id=$1 AND id=$2`, orgID, billID, journal); err != nil {
		return postResult{}, "", err
	}
	// Asset-account lines become register entries: the bill is the purchase document. Below the company
	// threshold an entry is kept but marked low value, so it is not depreciated (D4).
	var threshold int64
	if err := tx.QueryRow(ctx, `SELECT capitalisation_threshold_cents FROM organizations WHERE id=$1`, orgID).Scan(&threshold); err != nil {
		return postResult{}, "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO fixed_assets(org_id,name,acquired_on,cost_cents,useful_life_months,created_by,cost_account_id,acquisition_journal_id,low_value)
		SELECT $1, COALESCE(NULLIF(trim(l.description),''), 'Asset'), $3::date, l.amount_cents, 60, $4, l.account_id, $5, l.amount_cents < $6
		FROM bill_lines l JOIN accounts a ON a.org_id=l.org_id AND a.id=l.account_id
		WHERE l.org_id=$1 AND l.bill_id=$2 AND a.type='asset' AND (l.item_id IS NULL OR l.item_id=0) AND l.amount_cents > 0`,
		orgID, billID, date, userID, journal, threshold); err != nil {
		return postResult{}, "", err
	}
	if err := recordAudit(ctx, tx, orgID, userID, "bill", billID, "bill.posted", nil); err != nil {
		return postResult{}, "", err
	}
	if !historical {
		if err := postStockMovements(ctx, tx, orgID, "bill", billID, date); err != nil {
			return postResult{}, "", err
		}
		if err := fulfilBackordersForBill(ctx, tx, orgID, billID, day(date)); err != nil {
			return postResult{}, "", err
		}
	}
	return postResult{Journal: journal}, "", nil
}

// postBillLedgerLines writes the GL entry for a supplier bill in the shape postBill has always used.
func postBillLedgerLines(ctx context.Context, tx pgx.Tx, orgID, billID, journal, total, gst, selfGST int64, date time.Time, accounts map[string]int64) error {
	// Tracked stock debits Inventory at the same cents the stock ledger stores (D2: stock counts at receipt).
	moves, merr := documentStockMoves(ctx, tx, orgID, "bill", billID, day(date))
	if merr != nil {
		return merr
	}
	for i, m := range moves {
		value := m.qty * m.cost
		if value <= 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents) VALUES($1,$2,$3,$4,$5,0)`, orgID, journal, 900+i, m.account, value); err != nil {
			return err
		}
	}
	// Any rounding left between the bill's tracked line total and the stock value it carries goes to cost of sales.
	var trackedGross, trackedValue int64
	for _, m := range moves {
		trackedValue += m.qty * m.cost
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(CASE WHEN COALESCE(l.discount_account_id,b.discount_account_id) IS NULL THEN l.amount_cents ELSE l.amount_cents+l.discount_cents END),0)
		FROM bill_lines l JOIN bills b ON b.org_id=l.org_id AND b.id=l.bill_id
		JOIN items i ON i.org_id=l.org_id AND i.id=l.item_id
		WHERE l.org_id=$1 AND l.bill_id=$2 AND i.track_inventory`, orgID, billID).Scan(&trackedGross); err != nil {
		return err
	}
	if residual := trackedGross - trackedValue; residual != 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents) VALUES($1,$2,950,$3,$4,0)`, orgID, journal, accounts[acctCOGS], residual); err != nil {
			return err
		}
	}
	// A line with a discount account (its own, else the bill's) posts gross; the discounts are credited to that account.
	// Each bill line posts its own tracking values, so the tracking summary reads the option the line
	// was tagged with instead of the whole bill's total.
	if _, err := tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents,tracking) SELECT l.org_id,$3,l.line_no,l.account_id,CASE WHEN COALESCE(l.discount_account_id,b.discount_account_id) IS NULL THEN l.amount_cents ELSE l.amount_cents+l.discount_cents END,0,l.tracking FROM bill_lines l JOIN bills b ON b.org_id=l.org_id AND b.id=l.bill_id WHERE l.org_id=$1 AND l.bill_id=$2 AND (l.item_id IS NULL OR NOT EXISTS (SELECT 1 FROM items i WHERE i.org_id=l.org_id AND i.id=l.item_id AND i.track_inventory)) AND (CASE WHEN COALESCE(l.discount_account_id,b.discount_account_id) IS NULL THEN l.amount_cents ELSE l.amount_cents+l.discount_cents END)>0`, orgID, billID, journal); err != nil {
		return err
	}
	if gst > 0 {
		// Input tax debits the single GST control and is tagged input, so the F5 keeps box 7 apart.
		if _, err := tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents,tax_direction) VALUES($1,$2,202,$3,$4,0,'input')`, orgID, journal, accounts[acctGST], gst); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents) SELECT $1,$3,203+row_number() OVER (ORDER BY acct),acct,0,total FROM (SELECT COALESCE(l.discount_account_id,b.discount_account_id) acct,sum(l.discount_cents) total FROM bills b JOIN bill_lines l ON l.org_id=b.org_id AND b.id=l.bill_id WHERE b.org_id=$1 AND b.id=$2 GROUP BY 1) g WHERE acct IS NOT NULL AND total>0`, orgID, billID, journal); err != nil {
		return err
	}
	if selfGST > 0 {
		// Imported services and reverse charge: the supplier is paid the net only. The company
		// self-assesses GST, so input tax and output tax move onto the same control account together.
		if _, err := tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents,tax_direction) VALUES($1,$2,204,$3,$4,0,'input')`, orgID, journal, accounts[acctGST], selfGST); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents,tax_direction) VALUES($1,$2,205,$3,0,$4,'output')`, orgID, journal, accounts[acctGST], selfGST); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents) VALUES($1,$2,201,$3,0,$4)`, orgID, journal, accounts[acctAP], total); err != nil {
		return err
	}
	return nil
}

// payInvoiceTx records a payment against a posted invoice. A non-nil reuseJournal reuses a journal
// that already carries the entry, which is how an imported receipt avoids a second GL posting.
func payInvoiceTx(ctx context.Context, tx pgx.Tx, orgID, userID, invID, amount int64, pdate time.Time, key string, hash [32]byte, settlementRate int64, reuseJournal *int64, historical bool) (paymentID, journalID int64, code string, err error) {
	var status, currency string
	var total, docRate int64
	var idate time.Time
	err = tx.QueryRow(ctx, `SELECT status, total_cents, invoice_date, currency, fx_rate_micro FROM invoices WHERE org_id=$1 AND id=$2 FOR UPDATE`,
		orgID, invID).Scan(&status, &total, &idate, &currency, &docRate)
	if err == pgx.ErrNoRows {
		return 0, 0, "not_found", nil
	}
	if err != nil {
		return 0, 0, "", err
	}
	if status != "posted" {
		return 0, 0, "invoice_not_posted", nil
	}
	// Payments before the invoice date would create negative receivables in earlier as-of reports.
	// Peachtree allows them, so an imported receipt keeps its source date instead of being refused.
	if !historical && pdate.Before(idate) {
		return 0, 0, "payment_before_invoice", nil
	}
	var paid, credited int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT SUM(amount_cents) FROM payments WHERE org_id=$1 AND invoice_id=$2),0),
		COALESCE((SELECT SUM(amount_cents) FROM credit_notes WHERE org_id=$1 AND invoice_id=$2),0) + COALESCE((SELECT SUM(amount_cents) FROM customer_credit_applications WHERE org_id=$1 AND invoice_id=$2),0)`,
		orgID, invID).Scan(&paid, &credited); err != nil {
		return 0, 0, "", err
	}
	if paid+credited+amount > total {
		return 0, 0, "overpayment", nil
	}
	if reuseJournal != nil {
		journalID = *reuseJournal
	} else if currency != "SGD" && settlementRate != 0 && settlementRate != docRate {
		// The invoice is carried at its document rate. Clearing it at a different rate puts the
		// difference on the realised FX account instead of leaving it in receivables.
		acct, err := fxAccountIDs(ctx, tx, orgID)
		if err != nil {
			return 0, 0, "", err
		}
		bank := fxMulDivRound(amount, settlementRate, docRate)
		journalID, err = postFXJournal(ctx, tx, orgID, userID, pdate, "Payment received", "payment:"+key, hash, []fxLeg{
			{acct[acctBank], bank, currency},
			realisedFXLeg(acct, amount-bank, currency),
			{acct[acctAR], -amount, currency},
		})
		if err != nil {
			return 0, 0, "", err
		}
	} else {
		acct, err := accountIDs(ctx, tx, orgID)
		if err != nil {
			return 0, 0, "", err
		}
		journalID, err = postJournal(ctx, tx, orgID, userID, pdate, "Payment received", "payment:"+key, hash,
			acct[acctBank], acct[acctAR], amount)
		if err != nil {
			return 0, 0, "", err
		}
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO payments (org_id, invoice_id, amount_cents, payment_date, idempotency_key, request_hash, journal_id, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		orgID, invID, amount, pdate, key, hash[:], journalID, userID).Scan(&paymentID); err != nil {
		return 0, 0, "", err
	}
	if err := recordAudit(ctx, tx, orgID, userID, "payment", paymentID, "invoice.payment_recorded", map[string]any{"invoice_id": invID}); err != nil {
		return 0, 0, "", err
	}
	return paymentID, journalID, "", nil
}

// payBillTx records a payment against a posted supplier bill. A non-nil reuseJournal reuses an
// existing journal instead of posting a second GL entry.
// billOverpayment carries the explicit confirmation recorded when a payer pays more than the bill owes.
type billOverpayment struct {
	Description string
	Tracking    map[string]int64
}

func payBillTx(ctx context.Context, tx pgx.Tx, orgID, userID, billID, amount int64, date time.Time, reference, key string, hash [32]byte, settlementRate int64, reuseJournal *int64, historical bool, over *billOverpayment) (paymentID, journalID int64, code string, err error) {
	var state, currency string
	var total, docRate int64
	var issued time.Time
	err = tx.QueryRow(ctx, `SELECT status,total_cents,bill_date,currency,fx_rate_micro FROM bills WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, billID).Scan(&state, &total, &issued, &currency, &docRate)
	if err == pgx.ErrNoRows {
		return 0, 0, "not_found", nil
	}
	if err != nil {
		return 0, 0, "", err
	}
	if state != "posted" {
		return 0, 0, "bill_not_posted", nil
	}
	var credited, paid int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(total_cents),0) FROM supplier_credits WHERE org_id=$1 AND bill_id=$2`, orgID, billID).Scan(&credited); err != nil {
		return 0, 0, "", err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount_cents),0) FROM bill_payments p WHERE p.org_id=$1 AND p.bill_id=$2
		AND NOT EXISTS (SELECT 1 FROM document_reversals r WHERE r.org_id=p.org_id AND r.doc_kind='bill_payment' AND r.doc_id=p.id)`, orgID, billID).Scan(&paid); err != nil {
		return 0, 0, "", err
	}
	// A credit already reduced the bill, so payments can only cover the rest.
	paid += credited
	if !historical && date.Before(issued) {
		return 0, 0, "payment_before_bill", nil
	}
	if amount > total-paid && over == nil {
		return 0, 0, "overpayment", nil
	}
	if reuseJournal != nil {
		journalID = *reuseJournal
	} else if currency != "SGD" && settlementRate != 0 && settlementRate != docRate {
		accounts, err := fxAccountIDs(ctx, tx, orgID)
		if err != nil {
			return 0, 0, "", err
		}
		bank := fxMulDivRound(amount, settlementRate, docRate)
		journalID, err = postFXJournal(ctx, tx, orgID, userID, date, "Payment for supplier bill "+strconv.FormatInt(billID, 10), "bill-pay:"+key, hash, []fxLeg{
			{accounts[acctAP], amount, currency},
			realisedFXLeg(accounts, bank-amount, currency),
			{accounts[acctBank], -bank, currency},
		})
		if err != nil {
			return 0, 0, "", err
		}
	} else {
		accounts, err := accountIDs(ctx, tx, orgID)
		if err != nil {
			return 0, 0, "", err
		}
		journalID, err = postJournal(ctx, tx, orgID, userID, date, "Payment for supplier bill "+strconv.FormatInt(billID, 10), "bill-pay:"+key, hash, accounts[acctAP], accounts[acctBank], amount)
		if err != nil {
			return 0, 0, "", err
		}
	}
	overFlag, overDescription := false, ""
	var tracking []byte = []byte("{}")
	if over != nil {
		overFlag, overDescription = true, over.Description
		tracking = trackingJSON(over.Tracking)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO bill_payments(org_id,bill_id,amount_cents,payment_date,reference,idempotency_key,request_hash,journal_id,created_by,overpayment,overpayment_description,tracking)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
		orgID, billID, amount, date, reference, key, hash[:], journalID, userID, overFlag, overDescription, tracking).Scan(&paymentID); err != nil {
		return 0, 0, "", err
	}
	if err := recordAudit(ctx, tx, orgID, userID, "bill_payment", paymentID, "bill.payment_recorded", map[string]any{"bill_id": billID}); err != nil {
		return 0, 0, "", err
	}
	return paymentID, journalID, "", nil
}

// postCreditNoteTx creates a credit note against a posted invoice. A non-nil reuseJournal reuses the
// journal that already carries the entry, which is how an imported credit avoids a second GL posting.
func postCreditNoteTx(ctx context.Context, tx pgx.Tx, orgID, userID, invID, amount int64, date time.Time, reason string, reuseJournal *int64, historical bool) (creditID, journalID int64, code string, err error) {
	var status string
	var contactID int64
	var idate time.Time
	err = tx.QueryRow(ctx, `SELECT status, contact_id, invoice_date FROM invoices WHERE org_id=$1 AND id=$2 FOR UPDATE`, orgID, invID).Scan(&status, &contactID, &idate)
	if err == pgx.ErrNoRows {
		return 0, 0, "not_found", nil
	}
	if err != nil {
		return 0, 0, "", err
	}
	if status != "posted" {
		return 0, 0, "invoice_not_posted", nil
	}
	// A credit dated before its invoice would move receivables before they existed. Peachtree allows
	// it, so an imported credit keeps its source date instead of being refused.
	if !historical && date.Before(idate) {
		return 0, 0, "credit_before_invoice", nil
	}
	var already, total int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT sum(amount_cents) FROM credit_notes WHERE org_id=$1 AND invoice_id=$2),0) + COALESCE((SELECT sum(amount_cents) FROM payments WHERE org_id=$1 AND invoice_id=$2),0) + COALESCE((SELECT sum(amount_cents) FROM customer_credit_applications WHERE org_id=$1 AND invoice_id=$2),0), total_cents FROM invoices WHERE org_id=$1 AND id=$2`, orgID, invID).Scan(&already, &total); err != nil {
		return 0, 0, "", err
	}
	if already+amount > total {
		return 0, 0, "credit_exceeds_invoice", nil
	}
	if reuseJournal != nil {
		journalID = *reuseJournal
	} else {
		ids, err := accountIDs(ctx, tx, orgID)
		if err != nil {
			return 0, 0, "", err
		}
		key := "credit-note:" + strconv.FormatInt(invID, 10) + ":" + strconv.FormatInt(amount, 10) + ":" + day(date) + ":" + reason
		hash := sha256.Sum256([]byte(key))
		// Reverse what the invoice posted: its revenue accounts and GST Output, in proportion to the
		// credit, then clear Accounts Receivable. A partial credit therefore reverses tax pro rata.
		legs, err := invoiceCredits(ctx, tx, orgID, invID, ids[acctSales], ids["2300"], total)
		if err != nil {
			return 0, 0, "", err
		}
		journalID, err = postJournalLegs(ctx, tx, orgID, userID, date, "Credit note against invoice "+strconv.FormatInt(invID, 10)+": "+reason, key, hash,
			scaleLegs(legs, amount, total), []accountAmount{{ids[acctAR], amount, ""}})
		if err != nil {
			return 0, 0, "", err
		}
	}
	if err := tx.QueryRow(ctx, `INSERT INTO credit_notes (org_id, invoice_id, contact_id, credit_date, reason, amount_cents, journal_id, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, orgID, invID, contactID, date, reason, amount, journalID, userID).Scan(&creditID); err != nil {
		// Database trigger rejects over-credit even under concurrency; map that to the same error.
		if strings.Contains(err.Error(), "credit notes exceed invoice total") {
			return 0, 0, "credit_exceeds_invoice", nil
		}
		return 0, 0, "", err
	}
	if err := recordAudit(ctx, tx, orgID, userID, "credit_note", creditID, "credit_note.created", map[string]any{"invoice_id": invID}); err != nil {
		return 0, 0, "", err
	}
	return creditID, journalID, "", nil
}

// postSupplierCreditTx creates a supplier credit against a posted bill, reusing an existing journal
// when the caller supplies one.
func postSupplierCreditTx(ctx context.Context, tx pgx.Tx, orgID, userID, billID, netCents, gstCents int64, date time.Time, reason string, reuseJournal *int64, historical bool, cutoff string) (creditID, journalID int64, code string, err error) {
	var status, billDate string
	var total, billGST, contactID int64
	err = tx.QueryRow(ctx, `SELECT status, bill_date::text, total_cents, gst_cents, contact_id FROM bills WHERE org_id=$1 AND id=$2 FOR UPDATE`,
		orgID, billID).Scan(&status, &billDate, &total, &billGST, &contactID)
	if err == pgx.ErrNoRows {
		return 0, 0, "not_found", nil
	}
	if err != nil {
		return 0, 0, "", err
	}
	if status != "posted" {
		return 0, 0, "bill_not_posted", nil
	}
	if !historical && day(date) < billDate {
		return 0, 0, "credit_before_bill", nil
	}
	var credited, creditedGST, paid int64
	// A reconciliation cutoff counts only what existed at that date, so a later payment cannot eat the
	// headroom of a credit that was true on the report date.
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(total_cents),0), COALESCE(SUM(gst_cents),0) FROM supplier_credits
		WHERE org_id=$1 AND bill_id=$2 AND credit_date <= COALESCE(NULLIF($3,'')::date, '9999-12-31')`, orgID, billID, cutoff).Scan(&credited, &creditedGST); err != nil {
		return 0, 0, "", err
	}
	// Reversed payments no longer reduce the bill, so they must not count against its credit headroom.
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents),0) FROM bill_payments p WHERE p.org_id=$1 AND p.bill_id=$2
		AND p.payment_date <= COALESCE(NULLIF($3,'')::date, '9999-12-31')
		AND NOT EXISTS (SELECT 1 FROM document_reversals r WHERE r.org_id=p.org_id AND r.doc_kind='bill_payment' AND r.doc_id=p.id)`, orgID, billID, cutoff).Scan(&paid); err != nil {
		return 0, 0, "", err
	}
	credit := netCents + gstCents
	if credited+paid+credit > total {
		return 0, 0, "credit_exceeds_bill", nil
	}
	// Only a credit that carries GST can over-claim the bill's tax; a net-only credit leaves it alone,
	// even when earlier credits already reduced the GST below zero.
	if gstCents > 0 && gstCents > billGST-creditedGST {
		return 0, 0, "credit_exceeds_bill", nil
	}
	if reuseJournal != nil {
		journalID = *reuseJournal
	} else {
		ids, err := accountIDs(ctx, tx, orgID)
		if err != nil {
			return 0, 0, "", err
		}
		legs, net, gst, err := billCreditLegs(ctx, tx, orgID, billID)
		if err != nil {
			return 0, 0, "", err
		}
		if gst > 0 {
			legs = append(legs, accountAmount{ids[acctGST], gst, ""})
		}
		totalLegs := net + gst
		if totalLegs <= 0 {
			return 0, 0, "credit_exceeds_bill", nil
		}
		key := "supplier-credit:" + strconv.FormatInt(billID, 10) + ":" + day(date) + ":" + strconv.FormatInt(credit, 10)
		hash := sha256.Sum256([]byte(key))
		// The reverse of input tax sits on the same GST control account; the tag keeps it in box 7.
		journalID, err = postJournalLegs(ctx, tx, orgID, userID, date, "Supplier credit against bill "+strconv.FormatInt(billID, 10)+": "+reason, key, hash,
			[]accountAmount{{ids[acctAP], credit, ""}}, scaleLegs(legs, credit, totalLegs), map[int64]string{ids[acctGST]: "input"})
		if err != nil {
			return 0, 0, "", err
		}
	}
	if err := tx.QueryRow(ctx, `INSERT INTO supplier_credits(org_id,bill_id,contact_id,credit_date,reason,net_cents,gst_cents,total_cents,journal_id,created_by)
		VALUES($1,$2,$3,$4::date,$5,$6,$7,$8,$9,$10) RETURNING id`,
		orgID, billID, contactID, day(date), reason, netCents, gstCents, credit, journalID, userID).Scan(&creditID); err != nil {
		return 0, 0, "", err
	}
	if err := recordAudit(ctx, tx, orgID, userID, "supplier_credit", creditID, "supplier_credit.created", map[string]any{"bill_id": billID}); err != nil {
		return 0, 0, "", err
	}
	return creditID, journalID, "", nil
}
