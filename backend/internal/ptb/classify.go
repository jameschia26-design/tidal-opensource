package ptb

import (
	"encoding/binary"
	"fmt"
	"sort"
)

// Kind is the accounting shape of a decoded journal header.
type Kind string

const (
	Receipt    Kind = "receipt"
	Refund     Kind = "refund"
	Invoice    Kind = "invoice"
	CreditNote Kind = "credit_note"
	Payment    Kind = "payment"
	Bill       Kind = "bill"
	BillCredit Kind = "bill_credit"
	Journal    Kind = "journal"
)

// Document is one header with its resolved kind.
type Document struct {
	PostOrder uint32
	Party     uint32
	Kind      Kind
}

// Header and row field offsets, from the Peachtree data dictionary.
const (
	hdrModule    = 10
	hdrJournal   = 7
	hdrParty     = 11
	hdrAmount    = 70
	hdrPostOrder = 35
	rowAccount   = 0
	rowPostOrder = 4
	rowLink      = 26
	rowAmount    = 53
	rowAmountLen = 12
)

func field32(b []byte, off int) uint32 { return binary.LittleEndian.Uint32(b[off:]) }
func field16(b []byte, off int) uint16 { return binary.LittleEndian.Uint16(b[off:]) }

// Classify assigns every header its accounting shape. The module/journal pair names the family;
// whether a document is a charge or a credit is decided by the direction the party's control
// account moves.
//
// Settlement links are returned as source PostOrder to target PostOrder. An unresolved target is
// an error: a receipt or payment that points at nothing cannot be imported safely.
func Classify(headers, rows [][]byte) ([]Document, map[uint32][]uint32, error) {
	moduleOf, journalOf, partyOf, rowsByDoc, err := indexJournals(headers, rows)
	if err != nil {
		return nil, nil, err
	}
	ar, apByParty, paid := discoverControlAccounts(rowsByDoc, moduleOf, journalOf, partyOf)
	if len(ar) == 0 {
		return nil, nil, fmt.Errorf("%w: no receivables control account found", ErrCorrupt)
	}

	docs := make([]Document, 0, len(headers))
	links := make(map[uint32][]uint32)
	for _, h := range headers {
		po, party := field32(h, hdrPostOrder), field32(h, hdrParty)
		var kind Kind
		switch moduleOf[po] {
		case 'G':
			kind = Journal
		case 'R':
			switch journalOf[po] {
			case 3:
				amount, err := sumAccounts(rowsByDoc[po], ar)
				if err != nil {
					return nil, nil, err
				}
				kind = Invoice
				if amount < 0 {
					kind = CreditNote
				}
			case 1:
				amount, err := sumAccounts(rowsByDoc[po], ar)
				if err != nil {
					return nil, nil, err
				}
				kind = Receipt
				if amount > 0 {
					kind = Refund
				}
			default:
				return nil, nil, fmt.Errorf("%w: unexpected receivables journal %d", ErrUnsupported, journalOf[po])
			}
		case 'P':
			switch journalOf[po] {
			case 2:
				kind = Payment
			case 4:
				// The header amount is the document total: negative for a bill, positive for a
				// supplier credit. A zero-amount bill keeps the bill side. The control account is
				// still resolved per supplier for posting and reconciliation.
				control := intersect(accountsOf(rowsByDoc[po]), apByParty[party])
				if len(control) == 0 {
					if control, err = largestLine(rowsByDoc[po], paid); err != nil {
						return nil, nil, err
					}
				}
				amount, err := sumAccounts(rowsByDoc[po], control)
				if err != nil {
					return nil, nil, err
				}
				kind = Bill
				main, err := PackedDecimal(h[hdrAmount : hdrAmount+rowAmountLen])
				if err != nil {
					return nil, nil, err
				}
				if main > 0 || (main == 0 && amount > 0) {
					kind = BillCredit
				}
			default:
				return nil, nil, fmt.Errorf("%w: unexpected payables journal %d", ErrUnsupported, journalOf[po])
			}
		default:
			return nil, nil, fmt.Errorf("%w: unexpected module %q", ErrUnsupported, moduleOf[po])
		}

		for _, r := range rowsByDoc[po] {
			target := field32(r, rowLink)
			if target == 0 {
				continue
			}
			if _, ok := moduleOf[target]; !ok {
				return nil, nil, fmt.Errorf("%w: settlement points at missing document %d", ErrCorrupt, target)
			}
			links[po] = append(links[po], target)
		}
		docs = append(docs, Document{PostOrder: po, Party: party, Kind: kind})
	}
	return docs, links, nil
}

// Controls returns the control accounts Classify discovered: the receivable accounts in use and,
// per supplier, the payable account that supplier settles through.
func Controls(headers, rows [][]byte) (map[uint32]bool, map[uint32]map[uint32]bool, error) {
	moduleOf, journalOf, partyOf, rowsByDoc, err := indexJournals(headers, rows)
	if err != nil {
		return nil, nil, err
	}
	ar, ap, _ := discoverControlAccounts(rowsByDoc, moduleOf, journalOf, partyOf)
	return ar, ap, nil
}

func indexJournals(headers, rows [][]byte) (moduleOf map[uint32]byte, journalOf map[uint32]uint16, partyOf map[uint32]uint32, rowsByDoc map[uint32][][]byte, err error) {
	moduleOf = make(map[uint32]byte, len(headers))
	journalOf = make(map[uint32]uint16, len(headers))
	partyOf = make(map[uint32]uint32, len(headers))
	for i, h := range headers {
		if len(h) <= hdrPostOrder+4 {
			return nil, nil, nil, nil, fmt.Errorf("%w: header %d is short", ErrCorrupt, i+1)
		}
		po := field32(h, hdrPostOrder)
		if _, dup := moduleOf[po]; dup {
			return nil, nil, nil, nil, fmt.Errorf("%w: duplicate post order %d", ErrCorrupt, po)
		}
		moduleOf[po] = h[hdrModule]
		journalOf[po] = field16(h, hdrJournal)
		partyOf[po] = field32(h, hdrParty)
	}
	rowsByDoc = make(map[uint32][][]byte, len(headers))
	for i, r := range rows {
		if len(r) < rowAmount+rowAmountLen {
			return nil, nil, nil, nil, fmt.Errorf("%w: row %d is short", ErrCorrupt, i+1)
		}
		po := field32(r, rowPostOrder)
		if _, ok := moduleOf[po]; !ok {
			return nil, nil, nil, nil, fmt.Errorf("%w: row %d refers to unknown document %d", ErrCorrupt, i+1, po)
		}
		rowsByDoc[po] = append(rowsByDoc[po], r)
	}
	return moduleOf, journalOf, partyOf, rowsByDoc, nil
}

// discoverControlAccounts finds the accounts that actually carry receivable and payable balances.
// Receivable accounts come from the customer side: an account is a receivable control only if the
// same customer's invoices and receipts both post to it. Payable accounts are the credit-side lines
// of bills that also appear when the supplier is paid; that finds trade creditors, other creditors
// and accruals because different suppliers use different ones.
func discoverControlAccounts(rowsByDoc map[uint32][][]byte, moduleOf map[uint32]byte, journalOf map[uint32]uint16, partyOf map[uint32]uint32) (map[uint32]bool, map[uint32]map[uint32]bool, map[uint32]bool) {
	paid := map[uint32]bool{}
	for party, rows := range rowsByDoc {
		if moduleOf[party] == 'P' && journalOf[party] == 2 {
			for _, r := range rows {
				paid[field32(r, rowAccount)] = true
			}
		}
	}
	invoiceAccounts, receiptAccounts := map[uint32]map[uint32]bool{}, map[uint32]map[uint32]bool{}
	creditor := map[uint32]bool{}
	candidateHits := map[uint32]int{}
	for po, rows := range rowsByDoc {
		switch {
		case moduleOf[po] == 'R' && journalOf[po] == 3:
			invoiceAccounts[partyOf[po]] = accountsOf(rows)
		case moduleOf[po] == 'R' && journalOf[po] == 1:
			receiptAccounts[partyOf[po]] = accountsOf(rows)
		case moduleOf[po] == 'P' && journalOf[po] == 4:
			best, bestSize := uint32(0), int64(-1)
			for account, sum := range sumsByAccount(rows) {
				size := sum
				if size < 0 {
					size = -size
				}
				if !paid[account] || size <= bestSize {
					continue
				}
				best, bestSize = account, size
			}
			if bestSize > 0 {
				candidateHits[best]++
			}
		}
	}
	// Prefer the candidate used by the most invoices. That keeps the real receivable account when a
	// customer's single invoice and single receipt also share a discount or tax line.
	invoiceHits := map[uint32]int{}
	for _, set := range invoiceAccounts {
		for account := range set {
			invoiceHits[account]++
		}
	}
	// A payable account carries a credit balance across the whole ledger; an expense account that
	// happens to be the largest line on some documents does not. Requiring both many bills and a
	// credit balance keeps trade creditors, other creditors and accruals without hard-coded codes.
	net := map[uint32]int64{}
	for _, rows := range rowsByDoc {
		for account, sum := range sumsByAccount(rows) {
			net[account] += sum
		}
	}
	for account, hits := range candidateHits {
		if hits >= 2 && net[account] < 0 {
			creditor[account] = true
		}
	}
	if len(creditor) == 0 {
		return nil, nil, paid
	}
	ar := map[uint32]bool{}
	for party := range invoiceAccounts {
		best, bestHits := uint32(0), -1
		for account := range intersect(invoiceAccounts[party], receiptAccounts[party]) {
			if invoiceHits[account] > bestHits {
				best, bestHits = account, invoiceHits[account]
			}
		}
		if bestHits >= 0 {
			ar[best] = true
		}
	}
	apByParty := make(map[uint32]map[uint32]bool)
	for po := range rowsByDoc {
		if moduleOf[po] == 'P' && journalOf[po] == 4 {
			apByParty[partyOf[po]] = creditor
		}
	}
	return ar, apByParty, paid
}

func largestLine(rows [][]byte, paid map[uint32]bool) (map[uint32]bool, error) {
	var best uint32
	bestSize := int64(-1)
	for account, sum := range sumsByAccount(rows) {
		if sum < 0 {
			sum = -sum
		}
		if sum > bestSize && paid[account] {
			best, bestSize = account, sum
		}
	}
	if bestSize <= 0 {
		return nil, fmt.Errorf("%w: bill has no lines to determine its control account", ErrCorrupt)
	}
	return map[uint32]bool{best: true}, nil
}

func sumsByAccount(rows [][]byte) map[uint32]int64 {
	sums := make(map[uint32]int64, len(rows))
	for _, r := range rows {
		amount, err := PackedDecimal(r[rowAmount : rowAmount+rowAmountLen])
		if err != nil {
			continue
		}
		sums[field32(r, rowAccount)] += amount
	}
	return sums
}

func accountsOf(rows [][]byte) map[uint32]bool {
	set := make(map[uint32]bool, len(rows))
	for _, r := range rows {
		set[field32(r, rowAccount)] = true
	}
	return set
}

func sumAccounts(rows [][]byte, accounts map[uint32]bool) (int64, error) {
	var total int64
	for _, r := range rows {
		if !accounts[field32(r, rowAccount)] {
			continue
		}
		amount, err := PackedDecimal(r[rowAmount : rowAmount+rowAmountLen])
		if err != nil {
			return 0, err
		}
		total += amount
	}
	return total, nil
}

func intersect(a, b map[uint32]bool) map[uint32]bool {
	out := map[uint32]bool{}
	for k := range a {
		if b[k] {
			out[k] = true
		}
	}
	return out
}

// Exception is one source record the importer could not use, with the record's position in its
// table so a reviewer can find it in the source file.
type Exception struct {
	Table     string
	Ordinal   int    // 1-based record position in the decoded table
	PostOrder uint32 // source document group, when the record belongs to one
	Reason    string
}

// RowExceptions reports every header and row that cannot be classified, instead of stopping at the
// first one. Classify stays fail-closed for posting; this is the review list.
func RowExceptions(headers, rows [][]byte) []Exception {
	moduleOf := map[uint32]byte{}
	journalOf := map[uint32]uint16{}
	headersSeen := map[uint32]int{}
	exceptions := make([]Exception, 0)
	for i, h := range headers {
		if len(h) <= hdrPostOrder+4 {
			exceptions = append(exceptions, Exception{Table: "JRNLHDR.DAT", Ordinal: i + 1, Reason: "record is too short to read"})
			continue
		}
		po := field32(h, hdrPostOrder)
		if po == 0 {
			exceptions = append(exceptions, Exception{Table: "JRNLHDR.DAT", Ordinal: i + 1, Reason: "empty post order"})
			continue
		}
		if _, dup := headersSeen[po]; dup {
			exceptions = append(exceptions, Exception{Table: "JRNLHDR.DAT", Ordinal: i + 1, PostOrder: po, Reason: "duplicate post order"})
			continue
		}
		headersSeen[po] = i + 1
		moduleOf[po] = h[hdrModule]
		journalOf[po] = field16(h, hdrJournal)
		switch moduleOf[po] {
		case 'G', 'R', 'P':
		default:
			exceptions = append(exceptions, Exception{Table: "JRNLHDR.DAT", Ordinal: i + 1, PostOrder: po,
				Reason: fmt.Sprintf("unclassified journal family %q", string(moduleOf[po]))})
			continue
		}
		if moduleOf[po] == 'R' && journalOf[po] != 1 && journalOf[po] != 3 {
			exceptions = append(exceptions, Exception{Table: "JRNLHDR.DAT", Ordinal: i + 1, PostOrder: po,
				Reason: fmt.Sprintf("unclassified sales journal %d", journalOf[po])})
		}
		if moduleOf[po] == 'P' && journalOf[po] != 2 && journalOf[po] != 4 {
			exceptions = append(exceptions, Exception{Table: "JRNLHDR.DAT", Ordinal: i + 1, PostOrder: po,
				Reason: fmt.Sprintf("unclassified purchase journal %d", journalOf[po])})
		}
	}
	balanced := map[uint32]int64{}
	rowsSeen := map[uint32]bool{}
	for i, r := range rows {
		if len(r) < rowAmount+rowAmountLen {
			exceptions = append(exceptions, Exception{Table: "JRNLROW.DAT", Ordinal: i + 1, Reason: "record is too short to read"})
			continue
		}
		po := field32(r, rowPostOrder)
		if _, ok := moduleOf[po]; !ok {
			exceptions = append(exceptions, Exception{Table: "JRNLROW.DAT", Ordinal: i + 1, PostOrder: po,
				Reason: fmt.Sprintf("row refers to unknown document %d", po)})
			continue
		}
		rowsSeen[po] = true
		amount, err := PackedDecimal(r[rowAmount : rowAmount+rowAmountLen])
		if err != nil {
			exceptions = append(exceptions, Exception{Table: "JRNLROW.DAT", Ordinal: i + 1, PostOrder: po, Reason: "amount cannot be decoded"})
			continue
		}
		balanced[po] += amount
		if target := field32(r, rowLink); target != 0 {
			if _, ok := moduleOf[target]; !ok {
				exceptions = append(exceptions, Exception{Table: "JRNLROW.DAT", Ordinal: i + 1, PostOrder: po,
					Reason: fmt.Sprintf("settlement points at missing document %d", target)})
			}
		}
	}
	for po, sum := range balanced {
		if sum != 0 {
			exceptions = append(exceptions, Exception{Table: "JRNLHDR.DAT", Ordinal: headersSeen[po], PostOrder: po,
				Reason: fmt.Sprintf("document does not balance by %d", sum)})
		}
	}
	for po, ordinal := range headersSeen {
		if !rowsSeen[po] {
			exceptions = append(exceptions, Exception{Table: "JRNLHDR.DAT", Ordinal: ordinal, PostOrder: po,
				Reason: "document has no lines"})
		}
	}
	sort.SliceStable(exceptions, func(i, j int) bool {
		if exceptions[i].Table != exceptions[j].Table {
			return exceptions[i].Table < exceptions[j].Table
		}
		return exceptions[i].Ordinal < exceptions[j].Ordinal
	})
	return exceptions
}
