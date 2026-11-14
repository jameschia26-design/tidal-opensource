package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"tidal/backend/internal/ptb"
)

// Peachtree account type codes, read from the chart's AccountType field. The mapping is by account
// family, not by code prefix, because a Singapore chart can number debtors as 2000 and still call
// them assets.
var ptbAccountTypes = map[uint16]string{
	0: "asset", 1: "asset", 4: "asset", 5: "asset", 6: "asset",
	10: "liability", 12: "liability", 14: "liability",
	16: "equity", 18: "equity", 19: "equity",
	21: "revenue",
	23: "expense", 24: "expense",
}

type ptbCommitPlan struct {
	Accounts int   `json:"accounts"`
	Contacts int   `json:"contacts"`
	Journals int   `json:"journals"`
	Lines    int   `json:"lines"`
	Debit    int64 `json:"debit_cents"`
	Credit   int64 `json:"credit_cents"`
}

// ptbPayablesSnapshotInput carries one open supplier document exactly as the source report shows
// it. Dates are not accepted from the client: they come from the backup so a mistyped or
// re-formatted report date cannot move a document.
type ptbPayablesSnapshotInput struct {
	VendorCode string  `json:"vendor_code"`
	Party      string  `json:"party"`
	Reference  string  `json:"reference"`
	OpenCents  int64   `json:"open_cents"`
	Buckets    []int64 `json:"buckets_cents"`
}

type ptbCommitRequest struct {
	DryRun               bool                       `json:"dry_run"`
	AsOf                 string                     `json:"as_of"`
	PayablesSnapshot     []ptbPayablesSnapshotInput `json:"payables_snapshot,omitempty"`
	PayablesSourceSHA256 string                     `json:"payables_source_sha256,omitempty"`
}

// commitPTBImport posts a reconciled upload into the company's books. The whole post is one
// transaction keyed by source identity, so a failure writes nothing and a repeat run creates no
// duplicates.
func (a *app) commitPTBImport(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok || orgID == 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, true)
	if !ok {
		return
	}
	id, ok := parseOrgID(w, r.PathValue("id"))
	if !ok || id == 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	var req ptbCommitRequest
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req)
	}

	ctx := r.Context()
	var status, sum string
	var reconciliation map[string]any
	err := a.db.QueryRow(ctx, `SELECT status, sha256, reconciliation FROM ptb_imports WHERE id=$1 AND org_id=$2`, id, access.ID).
		Scan(&status, &sum, &reconciliation)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if status == "posted" {
		writeError(w, http.StatusConflict, "already_posted")
		return
	}
	// The stored reconciliation must both exist and have been given figures to check. An empty
	// panel would otherwise let any upload through.
	expected, _ := reconciliation["expected"].(map[string]any)
	if reconciliation == nil || reconciliation["matched"] != true || len(expected) == 0 {
		writeError(w, http.StatusConflict, "not_reconciled")
		return
	}
	tables, err := loadPTBTables(ctx, a.db, id)
	if err != nil {
		serverError(w, err)
		return
	}
	plan, err := buildPTBPlan(tables, sum, req.AsOf)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_backup")
		return
	}
	if plan.Journals == 0 {
		writeError(w, http.StatusUnprocessableEntity, "no_journals")
		return
	}

	var snapshotValidated []validatedPTBPayableSnapshotRow
	if len(req.PayablesSnapshot) > 0 {
		snapshotValidated, err = validatePTBPayablesSnapshot(tables, req.AsOf, req.PayablesSourceSHA256, req.PayablesSnapshot)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_payables_snapshot")
			return
		}
	} else if req.PayablesSourceSHA256 != "" {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if req.DryRun {
		writeJSON(w, http.StatusOK, map[string]any{"dry_run": true, "would_create": plan, "payables_snapshot_rows": len(snapshotValidated)})
		return
	}

	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)

	accountIDs, err := ensurePTBAccounts(ctx, tx, access.ID, tables["CHART.DAT"])
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_backup")
		return
	}
	if err := ensurePTBContacts(ctx, tx, access.ID, u.ID, tables); err != nil {
		serverError(w, err)
		return
	}
	created, skipped, lines, journalIDs, err := postPTBJournals(ctx, tx, access.ID, u.ID, sum, req.AsOf, accountIDs, tables)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_backup")
		return
	}
	// The documents are written inside a savepoint: the journals are the books and must post, while a
	// document failure is reported as an exception instead of discarding the whole import.
	docCounts := ptbDocCounts{}
	var docErr string
	if _, err := tx.Exec(ctx, "SAVEPOINT ptb_documents"); err != nil {
		serverError(w, err)
		return
	}
	docs, partyControl, docBuildErr := ptbDocuments(tables)
	salesID := int64(0)
	if docBuildErr == nil {
		salesID, docBuildErr = ptbRevenueAccount(ctx, tx, access.ID, tables["CHART.DAT"], accountIDs)
	}
	if docBuildErr == nil {
		asOf := ""
		if _, ok := parseDay(req.AsOf); ok {
			asOf = req.AsOf
		}
		docCounts, docBuildErr = recordPTBDocuments(ctx, tx, access.ID, u.ID, sum, docs, journalIDs, salesID, partyControl, asOf)
	}
	if docBuildErr == nil && len(snapshotValidated) > 0 {
		docBuildErr = insertPTBPayablesSnapshot(ctx, tx, access.ID, id, u.ID, req.AsOf, req.PayablesSourceSHA256, snapshotValidated)
	}
	if docBuildErr != nil {
		docErr = docBuildErr.Error()
		if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT ptb_documents"); err != nil {
			serverError(w, err)
			return
		}
	} else if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT ptb_documents"); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE ptb_imports SET status='posted', updated_at=now() WHERE id=$1`, id); err != nil {
		fmt.Fprintf(os.Stderr, "ptb status update: %v\n", err)
		serverError(w, err)
		return
	}
	detail, err := json.Marshal(map[string]any{
		"import_id": id, "sha256": sum, "journals": created, "skipped": skipped, "lines": lines,
		"documents": docCounts, "documents_error": docErr,
	})
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'ptb_import',$3)`, access.ID, u.ID, detail); err != nil {
		fmt.Fprintf(os.Stderr, "ptb audit: %v\n", err)
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "ptb commit: %v\n", err)
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "posted", "created": created, "skipped": skipped, "lines": lines,
		"documents": docCounts, "documents_error": docErr,
	})
}

type ptbJournal struct {
	Post  uint32
	Key   string
	Date  string
	Memo  string
	Lines []ptbJournalLine
}

type ptbJournalLine struct {
	Account string
	Debit   int64
	Credit  int64
}

// buildPTBPlan is the read-only shape of the post: which accounts, contacts and journals the upload
// would create. It runs the same decoding as the write path so a dry run cannot promise something
// the post would refuse.
func buildPTBPlan(tables map[string][][]byte, sum, asOf string) (ptbCommitPlan, error) {
	plan := ptbCommitPlan{}
	seenAccounts := map[string]bool{}
	for _, account := range tables["CHART.DAT"] {
		code := ptb.Text(account[:16])
		if code == "" {
			return plan, fmt.Errorf("%w: account with no code", ptb.ErrCorrupt)
		}
		if _, ok := ptbAccountTypes[binary.LittleEndian.Uint16(account[16:18])]; !ok {
			return plan, fmt.Errorf("%w: unknown account type for %s", ptb.ErrUnsupported, code)
		}
		seenAccounts[code] = true
	}
	plan.Accounts = len(seenAccounts)
	plan.Contacts = countPTBContacts(tables)
	journals, err := buildPTBJournals(tables, sum, asOf)
	if err != nil {
		return plan, err
	}
	plan.Journals = len(journals)
	for _, j := range journals {
		for _, line := range j.Lines {
			plan.Lines++
			plan.Debit += line.Debit
			plan.Credit += line.Credit
		}
	}
	return plan, nil
}

func countPTBContacts(tables map[string][][]byte) int {
	return len(tables["CUSTOMER.DAT"]) + len(tables["VENDOR.DAT"])
}

// buildPTBJournals turns every decoded header and its rows into one balanced journal. Source
// identity is the post order, so the same backup always produces the same keys.
func buildPTBJournals(tables map[string][][]byte, sum, asOf string) ([]ptbJournal, error) {
	accountCode := map[uint32]string{}
	for _, account := range tables["CHART.DAT"] {
		accountCode[binary.LittleEndian.Uint32(account[66:70])] = ptb.Text(account[:16])
	}
	headerByPost := map[uint32][]byte{}
	for _, header := range tables["JRNLHDR.DAT"] {
		if len(header) < 103 {
			return nil, fmt.Errorf("%w: short journal header", ptb.ErrCorrupt)
		}
		headerByPost[binary.LittleEndian.Uint32(header[35:39])] = header
	}
	rowsByDoc := map[uint32][][]byte{}
	for _, row := range tables["JRNLROW.DAT"] {
		if len(row) < 65 {
			return nil, fmt.Errorf("%w: short journal row", ptb.ErrCorrupt)
		}
		po := binary.LittleEndian.Uint32(row[4:8])
		rowsByDoc[po] = append(rowsByDoc[po], row)
	}
	prefix := "ptb:" + sum[:16] + ":"
	journals := make([]ptbJournal, 0, len(headerByPost))
	for po, header := range headerByPost {
		entry, err := ptb.Date(header[15:19])
		if err != nil {
			return nil, err
		}
		journal := ptbJournal{
			Post: po,
			Key:  prefix + fmt.Sprint(po),
			Date: entry.Format("2006-01-02"),
			Memo: ptbMemo(header),
		}
		var debit, credit int64
		for _, row := range rowsByDoc[po] {
			code := accountCode[binary.LittleEndian.Uint32(row[0:4])]
			if code == "" {
				return nil, fmt.Errorf("%w: journal %d uses an unknown account", ptb.ErrCorrupt, po)
			}
			amount, err := ptb.PackedDecimal(row[53:65])
			if err != nil {
				return nil, err
			}
			cents := toCents(amount)
			if cents == 0 {
				continue
			}
			line := ptbJournalLine{Account: code}
			if cents > 0 {
				line.Debit = cents
				debit += cents
			} else {
				line.Credit = -cents
				credit += -cents
			}
			journal.Lines = append(journal.Lines, line)
		}
		if debit != credit {
			return nil, fmt.Errorf("%w: journal %d is out of balance by %d", ptb.ErrCorrupt, po, debit-credit)
		}
		if len(journal.Lines) == 0 {
			continue
		}
		journals = append(journals, journal)
	}
	bf, err := ptbBroughtForward(tables, sum, journals, asOf)
	if err != nil {
		return nil, err
	}
	if bf != nil {
		journals = append([]ptbJournal{*bf}, journals...)
	}
	return journals, nil
}

// ptbBroughtForward sets each account's opening balance so posted journals reproduce the chart's
// cumulative balance. Purged history is not in the journals, so the difference is carried as one
// opening entry dated before the earliest journal. Returns nil when nothing needs carrying.
func ptbBroughtForward(tables map[string][][]byte, sum string, journals []ptbJournal, asOf string) (*ptbJournal, error) {
	// The chart balance describes one period end, so only journals up to that date may be subtracted.
	// Counting later journals here would push their movement into the report date and make every
	// as-at figure wrong while the all-time total still looked right.
	jsum := map[string]int64{}
	for _, j := range journals {
		if asOf != "" && j.Date > asOf {
			continue
		}
		for _, l := range j.Lines {
			jsum[l.Account] += l.Debit - l.Credit
		}
	}
	bf := ptbJournal{Key: "ptb:" + sum[:16] + ":bf", Date: "1900-01-01", Memo: "Peachtree brought-forward balances"}
	var debit, credit int64
	for _, account := range tables["CHART.DAT"] {
		code := ptb.Text(account[:16])
		var cum int64
		for i := 0; i < 36; i++ {
			v, err := ptb.PackedDecimal(account[chartNet+i*chartStride : chartNet+i*chartStride+12])
			if err != nil {
				return nil, err
			}
			cum += v
		}
		diff := toCents(cum) - jsum[code]
		if diff == 0 {
			continue
		}
		line := ptbJournalLine{Account: code}
		if diff > 0 {
			line.Debit, debit = diff, debit+diff
		} else {
			line.Credit, credit = -diff, credit-diff
		}
		bf.Lines = append(bf.Lines, line)
	}
	if len(bf.Lines) == 0 {
		return nil, nil
	}
	if debit != credit {
		return nil, fmt.Errorf("%w: brought-forward out of balance by %d", ptb.ErrCorrupt, debit-credit)
	}
	return &bf, nil
}

func ptbMemo(header []byte) string {
	reference := strings.TrimSpace(ptb.Text(header[82:103]))
	if reference == "" {
		reference = "Peachtree import"
	}
	if len(reference) > 490 {
		reference = reference[:490]
	}
	return reference
}

// ensurePTBAccounts creates any missing chart accounts and returns every code's Tidal id.
func ensurePTBAccounts(ctx context.Context, tx pgx.Tx, orgID int64, chart [][]byte) (map[string]int64, error) {
	ids := map[string]int64{}
	for _, account := range chart {
		code := ptb.Text(account[:16])
		name := ptb.Text(account[18:49])
		if name == "" {
			name = code
		}
		kind, ok := ptbAccountTypes[binary.LittleEndian.Uint16(account[16:18])]
		if !ok {
			return nil, fmt.Errorf("%w: unknown account type for %s", ptb.ErrUnsupported, code)
		}
		var id int64
		err := tx.QueryRow(ctx, `
			INSERT INTO accounts(org_id, code, name, type, class) VALUES($1,$2,$3,$4,$5)
			ON CONFLICT (org_id, code) DO UPDATE SET name=accounts.name
			RETURNING id`, orgID, code, name, kind, defaultAccountClass(kind, code)).Scan(&id)
		if err != nil {
			return nil, err
		}
		ids[code] = id
	}
	return ids, nil
}

// ptbRevenueAccount returns the company's revenue account for imported document lines: the source
// chart's first revenue account, or 4000 Sales when the chart has none.
func ptbRevenueAccount(ctx context.Context, tx pgx.Tx, orgID int64, chart [][]byte, accountIDs map[string]int64) (int64, error) {
	codes := make([]string, 0, len(chart))
	for _, account := range chart {
		code := ptb.Text(account[:16])
		if ptbAccountTypes[binary.LittleEndian.Uint16(account[16:18])] != "revenue" {
			continue
		}
		if _, ok := accountIDs[code]; ok {
			codes = append(codes, code)
		}
	}
	if len(codes) > 0 {
		sort.Strings(codes)
		return accountIDs[codes[0]], nil
	}
	return ensureAccount(ctx, tx, orgID, "4000", "Sales", "revenue")
}

// ensurePTBContacts adds the customer and supplier records that documents refer to.
func ensurePTBContacts(ctx context.Context, tx pgx.Tx, orgID, userID int64, tables map[string][][]byte) error {
	add := func(name string, kind string) error {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil
		}
		if len(name) > 200 {
			name = name[:200]
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO contacts(org_id, name, kind, created_by)
			SELECT $1,$2,$3,$4
			WHERE NOT EXISTS (SELECT 1 FROM contacts WHERE org_id=$1 AND lower(name)=lower($2))`, orgID, name, kind, userID)
		return err
	}
	for _, customer := range tables["CUSTOMER.DAT"] {
		if err := add(ptb.Text(customer[:21]), "customer"); err != nil {
			return err
		}
	}
	for _, vendor := range tables["VENDOR.DAT"] {
		if err := add(ptb.Text(vendor[37:77]), "supplier"); err != nil {
			return err
		}
	}
	return nil
}

// postPTBJournals writes the journals through the same tables manual journals use, skipping any
// source identity already posted. It returns how many were created.
func postPTBJournals(ctx context.Context, tx pgx.Tx, orgID, userID int64, sum, asOf string, accountIDs map[string]int64, tables map[string][][]byte) (created, skipped, lines int, ids map[uint32]int64, err error) {
	journals, err := buildPTBJournals(tables, sum, asOf)
	if err != nil {
		return 0, 0, 0, nil, err
	}
	ids = map[uint32]int64{}
	for _, journal := range journals {
		var existing int64
		err := tx.QueryRow(ctx, `SELECT id FROM journals WHERE org_id=$1 AND idempotency_key=$2`, orgID, journal.Key).Scan(&existing)
		if err == nil {
			skipped++
			ids[journal.Post] = existing
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, 0, nil, err
		}
		hash := sha256.Sum256([]byte(journal.Key + journal.Date + journal.Memo))
		var id int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO journals(org_id, entry_date, memo, idempotency_key, request_hash, created_by)
			VALUES($1,$2,$3,$4,$5,$6) RETURNING id`,
			orgID, journal.Date, journal.Memo, journal.Key, hash[:], userID).Scan(&id); err != nil {
			return 0, 0, 0, nil, err
		}
		ids[journal.Post] = id
		posted := make([]postedLine, 0, len(journal.Lines))
		for _, line := range journal.Lines {
			account, ok := accountIDs[line.Account]
			if !ok {
				return 0, 0, 0, nil, fmt.Errorf("%w: missing account %s", ptb.ErrCorrupt, line.Account)
			}
			posted = append(posted, postedLine{AccountID: account, Debit: line.Debit, Credit: line.Credit})
		}
		if err := insertJournalLines(ctx, tx, orgID, id, posted); err != nil {
			return 0, 0, 0, nil, err
		}
		if err := auditJournal(ctx, tx, orgID, userID, id); err != nil {
			return 0, 0, 0, nil, err
		}
		lines += len(posted)
		created++
	}
	return created, skipped, lines, ids, nil
}
