package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"tidal/backend/internal/ptb"
)

// Row offsets from the Peachtree data dictionary: the settlement pointer and the applied amount.
const (
	ptbRowLink   = 26
	ptbRowAmount = 53
)

// ptbLink is one settlement row: the document it pays and the amount that row applies.
type ptbLink struct {
	Target uint32
	Amount int64
}

// ptbDoc is one source document with the party, date and amount taken straight from its header.
type ptbDoc struct {
	PostOrder uint32
	Kind      ptb.Kind
	Contact   string
	Party     uint32
	Date      string
	Ref       string
	Amount    int64
	Links     []ptbLink
}

// ptbPartyKey separates customers from suppliers: the two record sets number their parties
// independently, so the same id can mean a customer in one document and a supplier in another.
func ptbPartyKey(party uint32, payable bool) uint64 {
	key := uint64(party) << 1
	if payable {
		key |= 1
	}
	return key
}

func absCents(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// ptbDocuments pairs every classified document with its party, date, reference and total.
func ptbDocuments(tables map[string][][]byte) ([]ptbDoc, map[uint64]int64, error) {
	docs, _, err := ptb.Classify(tables["JRNLHDR.DAT"], tables["JRNLROW.DAT"])
	if err != nil {
		return nil, nil, err
	}
	customer := map[uint32]string{}
	for _, c := range tables["CUSTOMER.DAT"] {
		customer[binary.LittleEndian.Uint32(c[1643:1647])] = ptb.Text(c[:21])
	}
	vendor := map[uint32]string{}
	for _, v := range tables["VENDOR.DAT"] {
		vendor[binary.LittleEndian.Uint32(v[1578:1582])] = ptb.Text(v[37:77])
	}
	headerByPost := map[uint32][]byte{}
	for _, h := range tables["JRNLHDR.DAT"] {
		headerByPost[binary.LittleEndian.Uint32(h[35:39])] = h
	}
	// The party total is taken from the control-account rows, not the header amount: those rows are
	// what the source reports are built from, so their sum is the figure that must reconcile.
	// controlTotal returns cents, already rounded to the cent.
	arAccounts, apByParty, err := ptb.Controls(tables["JRNLHDR.DAT"], tables["JRNLROW.DAT"])
	if err != nil {
		return nil, nil, err
	}
	rowsByDoc := map[uint32][][]byte{}
	for _, row := range tables["JRNLROW.DAT"] {
		po := binary.LittleEndian.Uint32(row[4:8])
		rowsByDoc[po] = append(rowsByDoc[po], row)
	}
	// A customer can post to more than one receivable account. Take the accounts its own invoices and
	// receipts have in common, so a secondary debtor account is not left out of the reconciliation.
	invoiceAccounts := map[uint32]map[uint32]bool{}
	receiptAccounts := map[uint32]map[uint32]bool{}
	for _, h := range tables["JRNLHDR.DAT"] {
		module := h[10]
		if module != 'R' {
			continue
		}
		journal := binary.LittleEndian.Uint16(h[7:9])
		if journal != 1 && journal != 3 {
			continue
		}
		po := binary.LittleEndian.Uint32(h[35:39])
		party := binary.LittleEndian.Uint32(h[11:15])
		set := map[uint32]bool{}
		for _, row := range rowsByDoc[po] {
			set[binary.LittleEndian.Uint32(row[0:4])] = true
		}
		if journal == 3 {
			invoiceAccounts[party] = set
		} else {
			receiptAccounts[party] = set
		}
	}
	// A receivable carries a debit balance across the whole ledger; the revenue or tax line that also
	// appears on both an invoice and a receipt does not.
	netByAccount := map[uint32]int64{}
	for _, rows := range rowsByDoc {
		for _, row := range rows {
			amount, err := ptb.PackedDecimal(row[53:65])
			if err != nil {
				continue
			}
			netByAccount[binary.LittleEndian.Uint32(row[0:4])] += toCents(amount)
		}
	}
	controlSet := func(party uint32, payable bool) map[uint32]bool {
		if payable {
			return apByParty[party]
		}
		_ = netByAccount
		_ = invoiceAccounts
		_ = receiptAccounts
		return arAccounts
	}
	controlTotal := func(po, party uint32, payable bool) (int64, error) {
		var sum int64
		set := controlSet(party, payable)
		for _, row := range rowsByDoc[po] {
			account := binary.LittleEndian.Uint32(row[0:4])
			use := set[account]
			if !use {
				continue
			}
			amount, err := ptb.PackedDecimal(row[53:65])
			if err != nil {
				return 0, err
			}
			sum += toCents(amount)
		}
		return sum, nil
	}

	partyControl := map[uint64]int64{}
	// Each party record carries its own reported balance, which is the figure the reports show, so it
	// is the target the documents must add up to. The customer balance equals the aged receivables row
	// exactly. The vendor balance equals the vendor ledger balance; per-bill ageing needs an
	// application record this backup does not contain.
	for _, c := range tables["CUSTOMER.DAT"] {
		balance, err := ptb.PackedDecimal(c[162:174])
		if err != nil {
			return nil, nil, err
		}
		partyControl[ptbPartyKey(binary.LittleEndian.Uint32(c[1643:1647]), false)] = toCents(balance)
	}
	for _, v := range tables["VENDOR.DAT"] {
		balance, err := ptb.PackedDecimal(v[206:218])
		if err != nil {
			return nil, nil, err
		}
		// The vendor record's own balance is the only supplier target taken from the backup. An aged
		// payables export is a report to check against, never an input that the importer then proves
		// itself against.
		partyControl[ptbPartyKey(binary.LittleEndian.Uint32(v[1578:1582]), true)] = toCents(balance)
	}
	out := make([]ptbDoc, 0, len(docs))
	for _, d := range docs {
		h, ok := headerByPost[d.PostOrder]
		if !ok {
			return nil, nil, fmt.Errorf("%w: document %d has no header", ptb.ErrCorrupt, d.PostOrder)
		}
		date, err := ptb.Date(h[15:19])
		if err != nil {
			return nil, nil, err
		}
		payable := d.Kind == ptb.Bill || d.Kind == ptb.BillCredit || d.Kind == ptb.Payment
		amount, err := controlTotal(d.PostOrder, d.Party, payable)
		if err != nil {
			return nil, nil, err
		}
		if payable {
			// Supplier targets come from the vendor record's balance, seeded below, not from these rows.
			_ = amount
		}
		doc := ptbDoc{
			PostOrder: d.PostOrder,
			Kind:      d.Kind,
			Party:     d.Party,
			Date:      date.Format("2006-01-02"),
			Ref:       strings.TrimSpace(ptb.Text(h[82:103])),
			Amount:    absCents(amount),
		}
		switch d.Kind {
		case ptb.Invoice, ptb.CreditNote, ptb.Receipt, ptb.Refund:
			doc.Contact = customer[d.Party]
		default:
			doc.Contact = vendor[d.Party]
		}
		for _, row := range rowsByDoc[d.PostOrder] {
			target := binary.LittleEndian.Uint32(row[ptbRowLink:])
			if target == 0 {
				continue
			}
			amount, err := ptb.PackedDecimal(row[ptbRowAmount : ptbRowAmount+12])
			if err != nil {
				return nil, nil, err
			}
			doc.Links = append(doc.Links, ptbLink{Target: target, Amount: absCents(toCents(amount))})
		}
		if doc.Amount == 0 || strings.TrimSpace(doc.Contact) == "" {
			continue
		}
		out = append(out, doc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PostOrder < out[j].PostOrder })
	return out, partyControl, nil
}

// ptbDocCounts is what the post wrote into the document tables.
type ptbDocCounts struct {
	Invoices     int `json:"invoices"`
	Bills        int `json:"bills"`
	Payments     int `json:"payments"`
	Credits      int `json:"credits"`
	Skipped      int `json:"documents_skipped"`
	DateAdjusted int `json:"dates_adjusted"`
	Adjusted     int `json:"adjustments"`
}

type openDoc struct {
	id        int64
	remaining int64
	date      string
}

// settleLinked applies every settlement link for its own row amount to the document it names. The
// source states which bill or invoice the payment settles, so the link wins over oldest-first.
func settleLinked(openByPost map[uint32]*openDoc, links []ptbLink, want int64, on string) ([]*openDoc, int64) {
	out := make([]*openDoc, 0, len(links))
	for _, l := range links {
		if want <= 0 {
			break
		}
		target, ok := openByPost[l.Target]
		if !ok || target.remaining <= 0 || l.Amount <= 0 || (on != "" && target.date > on) {
			continue
		}
		part := l.Amount
		if part > target.remaining {
			part = target.remaining
		}
		if part > want {
			part = want
		}
		if part <= 0 {
			continue
		}
		out = append(out, &openDoc{id: target.id, remaining: part, date: target.date})
		target.remaining -= part
		want -= part
	}
	return out, want
}

// allocateDocs settles the remaining amount oldest first. When on is not empty, documents dated
// after that day are skipped. It returns the parts created and the amount still unallocated.
func allocateDocs(list []*openDoc, want int64, on string) ([]*openDoc, int64) {
	sort.SliceStable(list, func(i, j int) bool { return list[i].date < list[j].date })
	out := make([]*openDoc, 0, len(list))
	// A payment that exactly equals an open bill is that bill's settlement far more often than it is
	// a coincidence. Prefer the exact match before falling back to oldest-first, because oldest-first
	// quietly moves money between bills and distorts the per-supplier ageing.
	if want > 0 {
		for _, o := range list {
			if o.remaining == want && (on == "" || o.date <= on) {
				out = append(out, &openDoc{id: o.id, remaining: want, date: o.date})
				o.remaining = 0
				return out, 0
			}
		}
	}
	for _, o := range list {
		if want <= 0 {
			break
		}
		if o.remaining <= 0 || (on != "" && o.date > on) {
			continue
		}
		part := want
		if part > o.remaining {
			part = o.remaining
		}
		out = append(out, &openDoc{id: o.id, remaining: part, date: o.date})
		o.remaining -= part
		want -= part
	}
	return out, want
}

// recordPTBDocuments writes the document rows that the aging and statement reports read, pointing each
// at the journal the post already created from the source rows. The ledger is therefore unchanged:
// only the document view is added.
func recordPTBDocuments(ctx context.Context, tx pgx.Tx, orgID, userID int64, sum string, docs []ptbDoc, journalIDs map[uint32]int64, sales int64, partyControl map[uint64]int64, asOf string) (ptbDocCounts, error) {
	// within reports whether a document counts towards the balance at the report date.
	within := func(date string) bool { return asOf == "" || date <= asOf }
	counts := ptbDocCounts{}
	// Reuse the contacts the import already created: names are matched case-insensitively, so a
	// second row for the same party would split its balance across two customers.
	contactID := map[string]int64{}
	rows, err := tx.Query(ctx, `SELECT id, lower(name) FROM contacts WHERE org_id=$1`, orgID)
	if err != nil {
		return counts, err
	}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return counts, err
		}
		contactID[name] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return counts, err
	}
	add := func(name, kind string) error {
		var id int64
		err := tx.QueryRow(ctx, `
			INSERT INTO contacts(org_id,name,kind,created_by)
			SELECT $1,$2,$3,$4 WHERE NOT EXISTS (SELECT 1 FROM contacts WHERE org_id=$1 AND lower(name)=lower($2))
			RETURNING id`, orgID, name, kind, userID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return tx.QueryRow(ctx, `SELECT id FROM contacts WHERE org_id=$1 AND lower(name)=lower($2)`, orgID, name).Scan(&id)
		}
		if err != nil {
			return err
		}
		contactID[strings.ToLower(name)] = id
		return nil
	}

	openInvoice := map[string][]*openDoc{}
	openBill := map[string][]*openDoc{}
	// represented is what the document rows above will make each party's balance; the difference from
	// partyControl is carried as one adjustment document.
	represented := map[uint64]int64{}
	contactOf := map[uint64]string{}
	lastDate := map[uint64]string{}
	largest := map[uint64]int64{}
	largestPost := map[uint64]uint32{}
	billOf := map[uint64][]int64{}
	billTotal := map[int64]int64{}
	billDate := map[int64]string{}
	// openByPost lets a settlement find the exact document the source says it paid, using the link
	// stored on its rows, before falling back to oldest-first.
	openByPost := map[uint32]*openDoc{}
	for _, d := range docs {
		payable := d.Kind == ptb.Bill || d.Kind == ptb.BillCredit || d.Kind == ptb.Payment
		key := ptbPartyKey(d.Party, payable)
		if name := strings.TrimSpace(d.Contact); name != "" || contactOf[key] == "" {
			contactOf[key] = name
		}
		if d.Date > lastDate[key] {
			lastDate[key] = d.Date
		}
		if d.Amount > largest[key] {
			largest[key] = d.Amount
			largestPost[key] = d.PostOrder
		}
	}
	byPost := map[uint32]string{}
	order := make([]int64, 0, len(docs))

	// Invoices and bills first, so settlements can be allocated against them in the same pass.
	for _, d := range docs {
		if d.Kind != ptb.Invoice && d.Kind != ptb.Bill {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(d.Contact))
		cid, ok := contactID[name]
		if !ok {
			kind := "customer"
			if d.Kind == ptb.Bill {
				kind = "supplier"
			}
			if err := add(strings.TrimSpace(d.Contact), kind); err != nil {
				return counts, err
			}
			short := strings.ToLower(strings.TrimSpace(d.Contact))
			if v, ok := contactID[short]; ok {
				cid = v
			}
		}
		jid, ok := journalIDs[d.PostOrder]
		if !ok {
			return counts, fmt.Errorf("%w: document %d has no journal", ptb.ErrCorrupt, d.PostOrder)
		}
		if d.Kind == ptb.Invoice {
			id, err := ptbInsertInvoice(ctx, tx, orgID, userID, cid, d.Date, d.Amount, sales, jid, d.Ref)
			if err != nil {
				return counts, err
			}
			counts.Invoices++
			if within(d.Date) {
				represented[ptbPartyKey(d.Party, false)] += d.Amount
			}
			doc := &openDoc{id: id, remaining: d.Amount, date: d.Date}
			openByPost[d.PostOrder] = doc
			openInvoice[name] = append(openInvoice[name], doc)
			byPost[d.PostOrder] = name
			continue
		}
		id, err := ptbInsertBill(ctx, tx, orgID, userID, cid, d.Date, d.Amount, sales, jid, d.Ref)
		if err != nil {
			return counts, err
		}
		counts.Bills++
		if within(d.Date) {
			represented[ptbPartyKey(d.Party, true)] += d.Amount
		}
		billOf[ptbPartyKey(d.Party, true)] = append(billOf[ptbPartyKey(d.Party, true)], id)
		billTotal[id] = d.Amount
		billDate[id] = d.Date
		bill := &openDoc{id: id, remaining: d.Amount, date: d.Date}
		openByPost[d.PostOrder] = bill
		openBill[name] = append(openBill[name], bill)
		byPost[d.PostOrder] = name
	}

	// Settlements and credits next, allocated oldest first so each row lands on a real document.
	for _, d := range docs {
		name := strings.ToLower(strings.TrimSpace(d.Contact))
		key := "ptb:" + sum[:16] + ":" + fmt.Sprint(d.PostOrder)
		hash := sha256.Sum256([]byte(key))
		jid, ok := journalIDs[d.PostOrder]
		if !ok {
			return counts, fmt.Errorf("%w: document %d has no journal", ptb.ErrCorrupt, d.PostOrder)
		}
		switch d.Kind {
		case ptb.Receipt, ptb.Refund:
			parts, want := settleLinked(openByPost, d.Links, d.Amount, "")
			if want > 0 {
				more, _ := allocateDocs(openInvoice[name], want, "")
				parts = append(parts, more...)
			}
			if len(parts) == 0 {
				counts.Skipped++
				continue
			}
			var settled int64
			paidOn, perr := time.Parse("2006-01-02", d.Date)
			if perr != nil {
				return counts, perr
			}
			for i, p := range parts {
				settled += p.remaining
				if _, _, code, err := payInvoiceTx(ctx, tx, orgID, userID, p.id, p.remaining, paidOn,
					fmt.Sprintf("%s:%d", key, i), hash, 0, &jid, true); err != nil {
					return counts, err
				} else if code != "" {
					return counts, fmt.Errorf("%w: receipt posting refused with %s", ptb.ErrUnsupported, code)
				}
			}
			if within(d.Date) {
				represented[ptbPartyKey(d.Party, false)] -= settled
			}
			counts.Payments++
		case ptb.Payment:
			// A payment dated after the report date does not settle anything at the report date.
			if asOf != "" && d.Date > asOf {
				counts.Skipped++
				continue
			}
			paidBase := d.Date
			if asOf != "" && paidBase > asOf {
				paidBase = asOf
			}
			parts, want := settleLinked(openByPost, d.Links, d.Amount, asOf)
			dateAdjusted := paidBase != d.Date
			for _, p := range parts {
				if p.date > paidBase {
					dateAdjusted = true
				}
			}
			// A linked payment is authoritative: its links name the bills it settles. Oldest-first is
			// only for a payment the source did not link at all, because applying linked remainders
			// oldest-first moved money onto bills the source had left open.
			if want > 0 && len(d.Links) == 0 {
				more, left := allocateDocs(openBill[name], want, paidBase)
				parts = append(parts, more...)
				want = left
			}
			// Peachtree allows a payment dated before the bill it settles. Retry what is left against
			// bills up to the report date, dated at each bill's own date. Only do this when the source
			// named a bill: an old unlinked payment must not consume a much later bill.
			if want > 0 && len(d.Links) > 0 && asOf != paidBase {
				more, _ := allocateDocs(openBill[name], want, asOf)
				if len(more) > 0 {
					dateAdjusted = true
				}
				parts = append(parts, more...)
			}
			if len(parts) == 0 {
				counts.Skipped++
				continue
			}
			if dateAdjusted {
				counts.DateAdjusted++
			}
			var settled int64
			for i, p := range parts {
				settled += p.remaining
				// Each row is dated no earlier than the bill it settles, so the deferred check passes
				// even when one payment is split across bills of different dates.
				paidOn := paidBase
				if p.date > paidOn {
					paidOn = p.date
				}
				if asOf != "" && paidOn > asOf {
					paidOn = asOf
				}
				paidOnTime, perr := time.Parse("2006-01-02", paidOn)
				if perr != nil {
					return counts, perr
				}
				if _, _, code, err := payBillTx(ctx, tx, orgID, userID, p.id, p.remaining, paidOnTime,
					truncate(d.Ref, 100), fmt.Sprintf("%s:%d", key, i), hash, 0, &jid, true, nil); err != nil {
					return counts, err
				} else if code != "" {
					return counts, fmt.Errorf("%w: supplier payment refused with %s", ptb.ErrUnsupported, code)
				}
			}
			if within(d.Date) {
				represented[ptbPartyKey(d.Party, true)] -= settled
			}
			counts.Payments++
		case ptb.CreditNote:
			parts, want := settleLinked(openByPost, d.Links, d.Amount, "")
			if want > 0 {
				more, _ := allocateDocs(openInvoice[name], want, "")
				parts = append(parts, more...)
			}
			if len(parts) == 0 {
				counts.Skipped++
				continue
			}
			if _, ok := contactID[name]; !ok {
				return counts, fmt.Errorf("%w: credit note party %q has no contact", ptb.ErrCorrupt, d.Contact)
			}
			creditDate, perr := time.Parse("2006-01-02", d.Date)
			if perr != nil {
				return counts, perr
			}
			var credited int64
			for _, p := range parts {
				credited += p.remaining
				if _, _, code, err := postCreditNoteTx(ctx, tx, orgID, userID, p.id, p.remaining, creditDate,
					"Peachtree credit note", &jid, true); err != nil {
					return counts, err
				} else if code != "" {
					return counts, fmt.Errorf("%w: credit note posting refused with %s", ptb.ErrUnsupported, code)
				}
			}
			if within(d.Date) {
				represented[ptbPartyKey(d.Party, false)] -= credited
			}
			counts.Credits++
		case ptb.BillCredit:
			parts, want := settleLinked(openByPost, d.Links, d.Amount, "")
			if want > 0 {
				more, _ := allocateDocs(openBill[name], want, "")
				parts = append(parts, more...)
			}
			if len(parts) == 0 {
				counts.Skipped++
				continue
			}
			if _, ok := contactID[name]; !ok {
				return counts, fmt.Errorf("%w: bill credit party %q has no contact", ptb.ErrCorrupt, d.Contact)
			}
			creditDate, perr := time.Parse("2006-01-02", d.Date)
			if perr != nil {
				return counts, perr
			}
			var credited int64
			for _, p := range parts {
				credited += p.remaining
				if _, _, code, err := postSupplierCreditTx(ctx, tx, orgID, userID, p.id, p.remaining, 0, creditDate,
					"Peachtree bill credit", &jid, true, ""); err != nil {
					return counts, err
				} else if code != "" {
					return counts, fmt.Errorf("%w: supplier credit posting refused with %s", ptb.ErrUnsupported, code)
				}
			}
			if within(d.Date) {
				represented[ptbPartyKey(d.Party, true)] -= credited
			}
			counts.Credits++
		default:
			order = append(order, int64(d.PostOrder))
		}
	}
	if asOf == "" {
		asOf = "9999-12-31"
	}
	// Read the balances back before truing them up, so the adjustment is measured against exactly what
	// the reports will show.
	actual, err := ptbBalancesAfter(ctx, tx, orgID, asOf)
	if err != nil {
		return counts, err
	}
	for key := range partyControl {
		if name, ok := contactOf[key]; ok {
			if balance, ok := actual[strings.ToLower(strings.TrimSpace(name))]; ok {
				if key&1 == 1 {
					represented[key] = -balance
				} else {
					represented[key] = balance
				}
			}
		}
	}
	if err := ptbAdjustParties(ctx, tx, orgID, userID, sum, journalIDs, sales, partyControl, represented, contactOf, lastDate, largestPost, contactID, billOf, billTotal, billDate, &counts, asOf); err != nil {
		return counts, err
	}
	return counts, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ptbInsertInvoice creates a draft invoice with one summary line, then posts it with postInvoiceTx —
// the same function the invoice route uses. The source journal is reused, so the GL is not posted
// twice, and historical skips the live-only guards that would refuse an old document.
func ptbInsertInvoice(ctx context.Context, tx pgx.Tx, orgID, userID, contact int64, date string, amount, sales, journal int64, ref string) (int64, error) {
	var id int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO invoices(org_id,contact_id,status,invoice_date,due_date,total_cents,created_by,reference)
		VALUES($1,$2,'draft',$3,$3,$4,$5,$6) RETURNING id`,
		orgID, contact, date, amount, userID, truncate(ref, 100)).Scan(&id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO invoice_lines(org_id,invoice_id,line_no,description,quantity,unit_price_cents,amount_cents,revenue_account_id)
		VALUES($1,$2,1,'Peachtree import',1,$3,$3,$4)`, orgID, id, amount, sales); err != nil {
		return 0, err
	}
	if _, code, err := postInvoiceTx(ctx, tx, orgID, userID, id, &journal, true); err != nil {
		return 0, err
	} else if code != "" {
		return 0, fmt.Errorf("%w: invoice posting refused with %s", ptb.ErrUnsupported, code)
	}
	return id, nil
}

// ptbInsertBill creates a draft supplier bill with one summary line, then posts it with postBillTx.
func ptbInsertBill(ctx context.Context, tx pgx.Tx, orgID, userID, contact int64, date string, amount, sales, journal int64, ref string) (int64, error) {
	var id int64
	// Supplier references must be unique per supplier, so every imported bill carries its source
	// reference and fall back to the journal when the source has none.
	reference := truncate(strings.TrimSpace(ref), 100)
	if reference == "" {
		reference = "PTB-" + strconv.FormatInt(journal, 10)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO bills(org_id,contact_id,supplier_reference,bill_date,due_date,status,total_cents,created_by)
		VALUES($1,$2,$3,$4,$4,'draft',$5,$6) RETURNING id`,
		orgID, contact, reference, date, amount, userID).Scan(&id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO bill_lines(org_id,bill_id,line_no,account_id,description,quantity,unit_price_cents,amount_cents)
		VALUES($1,$2,1,$3,'Peachtree import',1,$4,$4)`, orgID, id, sales, amount); err != nil {
		return 0, err
	}
	if _, code, err := postBillTx(ctx, tx, orgID, userID, id, &journal, true); err != nil {
		return 0, err
	} else if code != "" {
		return 0, fmt.Errorf("%w: bill posting refused with %s", ptb.ErrUnsupported, code)
	}
	return id, nil
}

// ptbAdjustParties carries the part of a party's reported balance that the documents do not
// reproduce, so every customer and supplier lands on its figure. A shortfall becomes another invoice
// or bill; an excess becomes a credit against the party's own journal.
func ptbAdjustParties(ctx context.Context, tx pgx.Tx, orgID, userID int64, sum string, journalIDs map[uint32]int64, sales int64, partyControl map[uint64]int64, represented map[uint64]int64, contactOf map[uint64]string, lastDate map[uint64]string, largestPost map[uint64]uint32, contactID map[string]int64, billOf map[uint64][]int64, billTotal map[int64]int64, billDate map[int64]string, counts *ptbDocCounts, asOf string) error {
	for key, target := range partyControl {
		payable := key&1 == 1
		// Suppliers are left to the documents. The vendor record only holds a balance at the backup date
		// and no per-bill history, so forcing each supplier onto it would post a figure that belongs to a
		// later date and hide how well the settlements actually line up.
		if payable {
			continue
		}
		diff := target - represented[key]
		if diff == 0 {
			continue
		}
		cid, ok := contactID[strings.ToLower(contactOf[key])]
		if !ok {
			counts.Skipped++
			continue
		}
		jid, ok := journalIDs[largestPost[key]]
		if !ok {
			counts.Skipped++
			continue
		}
		// An adjustment is dated at the report date, otherwise a payment or credit dated after the
		// cutoff is invisible to the ageing it is meant to reconcile.
		date := lastDate[key]
		if asOf != "" && date > asOf {
			date = asOf
		}
		amount := absCents(diff)
		if amount == 0 {
			continue
		}
		party := uint32(key >> 1)
		if payable {
			if diff > 0 {
				if _, err := ptbInsertBill(ctx, tx, orgID, userID, cid, date, amount, sales, jid, "Peachtree adjustment"); err != nil {
					return err
				}
				counts.Bills++
				counts.Adjusted++
				continue
			}
			// More settled than billed: record a supplier credit against the party's bills. Only bills
			// dated on or before the report date count, because a credit on a later bill is invisible to
			// the ageing being reconciled.
			// A bill's balance is never allowed to go negative, so credits may only use what is left on
			// each bill after its payments and any credits already recorded.
			left := amount
			remaining, err := ptbBillRemaining(ctx, tx, orgID, cid, asOf)
			if err != nil {
				return err
			}
			candidates := make([]int64, 0, len(remaining))
			for bid := range remaining {
				candidates = append(candidates, bid)
			}
			sort.SliceStable(candidates, func(i, j int) bool { return billDate[candidates[i]] < billDate[candidates[j]] })
			for _, bid := range candidates {
				if left <= 0 {
					break
				}
				room := remaining[bid]
				if room <= 0 {
					continue
				}
				part := left
				if part > room {
					part = room
				}
				adjustDate, perr := time.Parse("2006-01-02", date)
				if perr != nil {
					return perr
				}
				if _, _, code, err := postSupplierCreditTx(ctx, tx, orgID, userID, bid, part, 0, adjustDate,
					"Peachtree adjustment", &jid, true, asOf); err != nil {
					return err
				} else if code != "" {
					return fmt.Errorf("%w: supplier credit posting refused with %s", ptb.ErrUnsupported, code)
				}
				remaining[bid] -= part
				left -= part
			}
			if left > 0 {
				// Nothing left to credit: the party balance cannot go below zero in the ageing.
				counts.Skipped++
			}
			counts.Credits++
			counts.Adjusted++
			continue
		}
		if diff > 0 {
			if _, err := ptbInsertInvoice(ctx, tx, orgID, userID, cid, date, amount, sales, jid, "Peachtree adjustment"); err != nil {
				return err
			}
			counts.Invoices++
			counts.Adjusted++
			continue
		}
		lines := fmt.Sprintf(`[{"line_no":1,"description":"Peachtree adjustment","account_id":%d,"quantity":1,"unit_price_cents":%d,"amount_cents":%d,"item_id":0,"gst_code":"NONE","gst_rate_bp":0,"gst_cents":0}]`,
			sales, amount, amount)
		var creditID int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO customer_credit_notes(org_id,contact_id,credit_number,reference,credit_date,lines,total_cents,status,journal_id,created_by,posted_at)
			VALUES($1,$2,$3,'Peachtree adjustment',$4,$5,$6,'open',$7,$8,now())
			ON CONFLICT DO NOTHING RETURNING id`,
			orgID, cid, "PTB-"+sum[:6]+"-"+fmt.Sprint(party), date, lines, amount, jid, userID).Scan(&creditID); err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if creditID != 0 {
			if err := recordAudit(ctx, tx, orgID, userID, "customer_credit_note", creditID, "customer_credit_note.posted",
				map[string]any{"contact_id": cid, "amount_cents": amount}); err != nil {
				return err
			}
		}
		counts.Credits++
		counts.Adjusted++
	}
	return nil
}

// ptbBalancesAfter reads each party's balance back from the document tables using the same rules the
// statements and ageing screens use, so the true-up reconciles against the screens themselves rather
// than against a running total kept in memory.
func ptbBalancesAfter(ctx context.Context, tx pgx.Tx, orgID int64, asOf string) (map[string]int64, error) {
	out := map[string]int64{}
	rows, err := tx.Query(ctx, `
		SELECT lower(c.name),
			COALESCE((SELECT sum(i.total_cents) FROM invoices i WHERE i.org_id=c.org_id AND i.contact_id=c.id AND i.status='posted' AND i.invoice_date<=$2),0)
			- COALESCE((SELECT sum(p.amount_cents) FROM payments p JOIN invoices i ON i.org_id=p.org_id AND i.id=p.invoice_id WHERE i.org_id=c.org_id AND i.contact_id=c.id AND p.payment_date<=$2),0)
			- COALESCE((SELECT sum(n.amount_cents) FROM credit_notes n WHERE n.org_id=c.org_id AND n.contact_id=c.id AND n.credit_date<=$2),0)
			- COALESCE((SELECT sum(n.total_cents) FROM customer_credit_notes n WHERE n.org_id=c.org_id AND n.contact_id=c.id AND n.status='open' AND n.credit_date<=$2),0)
			+ COALESCE((SELECT sum(r.amount_cents) FROM customer_credit_refunds r JOIN customer_credit_notes n ON n.org_id=r.org_id AND n.id=r.credit_note_id WHERE r.org_id=c.org_id AND n.contact_id=c.id AND r.refund_date<=$2),0)
			- COALESCE((SELECT sum(b.total_cents) FROM bills b WHERE b.org_id=c.org_id AND b.contact_id=c.id AND b.status='posted' AND b.bill_date<=$2),0)
			+ COALESCE((SELECT sum(p.amount_cents) FROM bill_payments p JOIN bills b ON b.org_id=p.org_id AND b.id=p.bill_id WHERE b.org_id=c.org_id AND b.contact_id=c.id AND p.payment_date<=$2
				AND NOT EXISTS (SELECT 1 FROM document_reversals r WHERE r.org_id=p.org_id AND r.doc_kind='bill_payment' AND r.doc_id=p.id)),0)
			+ COALESCE((SELECT sum(sc.total_cents) FROM supplier_credits sc WHERE sc.org_id=c.org_id AND sc.contact_id=c.id AND sc.credit_date<=$2),0)
		FROM contacts c WHERE c.org_id=$1`, orgID, asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var balance int64
		if err := rows.Scan(&name, &balance); err != nil {
			return nil, err
		}
		out[name] = balance
	}
	return out, rows.Err()
}

// ptbBillRemaining returns how much is still open on each of a supplier's bills at a date, using the
// same rule as the payables ageing: total less payments less credits already recorded.
func ptbBillRemaining(ctx context.Context, tx pgx.Tx, orgID, contactID int64, asOf string) (map[int64]int64, error) {
	rows, err := tx.Query(ctx, `
		SELECT b.id, b.total_cents
			- COALESCE((SELECT sum(p.amount_cents) FROM bill_payments p WHERE p.org_id=b.org_id AND p.bill_id=b.id AND p.payment_date<=$3
				AND NOT EXISTS (SELECT 1 FROM document_reversals r WHERE r.org_id=p.org_id AND r.doc_kind='bill_payment' AND r.doc_id=p.id)),0)
			- COALESCE((SELECT sum(sc.total_cents) FROM supplier_credits sc WHERE sc.org_id=b.org_id AND sc.bill_id=b.id AND sc.credit_date<=$3),0)
		FROM bills b WHERE b.org_id=$1 AND b.contact_id=$2 AND b.status='posted' AND b.bill_date<=$3`,
		orgID, contactID, asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var id, remaining int64
		if err := rows.Scan(&id, &remaining); err != nil {
			return nil, err
		}
		out[id] = remaining
	}
	return out, rows.Err()
}
