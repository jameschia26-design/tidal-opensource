package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type accountInput struct {
	OrgID       int64  `json:"org_id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Class       string `json:"class"`
	Description string `json:"description"`
	// Xero Add Account options. Round-tripped so edits keep the flags.
	ShowOnDashboard     bool `json:"show_on_dashboard"`
	ShowInExpenseClaims bool `json:"show_in_expense_claims"`
	EnablePayments      bool `json:"enable_payments"`
	IsBankAccount       bool `json:"is_bank_account"`
	// TaxRateCode is the default tax rate a new line on this account starts with; blank means none.
	TaxRateCode string `json:"tax_rate_code"`
}

// accountClassGroups places each Xero chart class inside the broad account type the ledger posts
// with. The type keeps every report and posting rule unchanged; the class is the finer chart split.
var accountClassGroups = map[string]string{
	"bank": "asset", "current_asset": "asset", "fixed_asset": "asset", "inventory": "asset",
	"prepayment": "asset", "non_current_asset": "asset",
	"current_liability": "liability", "non_current_liability": "liability",
	"equity": "equity", "retained_earnings": "equity",
	"sales": "revenue", "revenue": "revenue", "other_income": "revenue",
	"cost_of_sales": "expense", "expense": "expense", "other_expense": "expense",
}

func classGroup(class string) string { return accountClassGroups[class] }

// defaultAccountClass applies the same code-prefix rules as the 120 backfill, so an account created
// or imported without a class lands where the existing chart does.
func defaultAccountClass(typ, code string) string {
	switch typ {
	case "revenue":
		if strings.HasPrefix(code, "4") {
			return "sales"
		}
		return "revenue"
	case "expense":
		if strings.HasPrefix(code, "5") {
			return "cost_of_sales"
		}
		return "expense"
	case "asset":
		switch {
		case strings.HasPrefix(code, "28"):
			return "bank"
		case strings.HasPrefix(code, "15"), strings.HasPrefix(code, "17"):
			return "fixed_asset"
		case strings.HasPrefix(code, "24"):
			return "inventory"
		}
		return "current_asset"
	case "liability":
		return "current_liability"
	case "equity":
		return "equity"
	}
	return ""
}

func validAccount(in *accountInput) bool {
	in.Code = strings.TrimSpace(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	in.Class = strings.TrimSpace(in.Class)
	in.Description = strings.TrimSpace(in.Description)
	in.TaxRateCode = strings.ToUpper(strings.TrimSpace(in.TaxRateCode))
	if !utf8.ValidString(in.Description) || utf8.RuneCountInString(in.Description) > 4000 || strings.ContainsRune(in.Description, 0) {
		return false
	}
	if in.Code == "" || len(in.Code) > 20 || in.Name == "" || !utf8.ValidString(in.Name) || utf8.RuneCountInString(in.Name) > 200 || strings.ContainsRune(in.Name, 0) {
		return false
	}
	for _, c := range in.Code {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	switch in.Type {
	case "asset", "liability", "equity", "revenue", "expense":
	default:
		return false
	}
	// A class is optional: an omitted one defaults from the type and code. A supplied one must exist
	// and stay inside the type's group, so the two columns cannot disagree.
	if in.Class == "" {
		in.Class = defaultAccountClass(in.Type, in.Code)
	}
	return classGroup(in.Class) == in.Type
}

func (a *app) listAccounts(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	var fyEnd string
	_ = a.db.QueryRow(r.Context(), `SELECT fiscal_year_end FROM organizations WHERE id=$1`, access.ID).Scan(&fyEnd)
	fyStart := fiscalYearStart(fyEnd, time.Now())
	rows, err := a.db.Query(r.Context(), `SELECT a.id,a.code,a.name,a.type,a.class,a.description,a.show_on_dashboard,a.show_in_expense_claims,a.enable_payments,COALESCE(a.is_bank_account,false),COALESCE(a.tax_rate_code,''),
		COALESCE((SELECT SUM(jl.debit_cents-jl.credit_cents) FROM journal_lines jl JOIN journals j ON j.org_id=jl.org_id AND j.id=jl.journal_id WHERE jl.org_id=a.org_id AND jl.account_id=a.id AND j.entry_date>=$2::date AND j.entry_date<=$3::date),0)
		FROM accounts a WHERE a.org_id=$1 ORDER BY a.code`, access.ID, fyStart, day(time.Now()))
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type account struct {
		ID                  int64  `json:"id"`
		Code                string `json:"code"`
		Name                string `json:"name"`
		Type                string `json:"type"`
		Class               string `json:"class"`
		Description         string `json:"description"`
		ShowOnDashboard     bool   `json:"show_on_dashboard"`
		ShowInExpenseClaims bool   `json:"show_in_expense_claims"`
		EnablePayments      bool   `json:"enable_payments"`
		IsBankAccount       bool   `json:"is_bank_account"`
		TaxRateCode         string `json:"tax_rate_code"`
		YTDCents            int64  `json:"ytd_cents"`
	}
	out := []account{}
	for rows.Next() {
		var row account
		if err := rows.Scan(&row.ID, &row.Code, &row.Name, &row.Type, &row.Class, &row.Description, &row.ShowOnDashboard, &row.ShowInExpenseClaims, &row.EnablePayments, &row.IsBankAccount, &row.TaxRateCode, &row.YTDCents); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

func (a *app) createAccount(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in accountInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validAccount(&in) {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" && access.Role != "accountant" {
		writeError(w, 403, "forbidden")
		return
	}
	// These codes belong to built-in posting rules. Reserving them prevents invoices posting to a wrong account type.
	for _, s := range seedAccounts {
		if in.Code == s.code {
			writeError(w, 409, "reserved_account")
			return
		}
	}
	ctx := r.Context()
	okTax, err := validAccountTaxCode(ctx, a.db, in.Type, in.TaxRateCode)
	if err != nil {
		serverError(w, err)
		return
	}
	if !okTax {
		writeError(w, http.StatusUnprocessableEntity, "tax_rate_not_allowed")
		return
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO accounts(org_id,code,name,type,class,description,show_on_dashboard,show_in_expense_claims,enable_payments,is_bank_account,tax_rate_code) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,'')) RETURNING id`, access.ID, in.Code, in.Name, in.Type, in.Class, in.Description, in.ShowOnDashboard, in.ShowInExpenseClaims, in.EnablePayments, in.IsBankAccount, in.TaxRateCode).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, 409, "account_code_exists")
		} else {
			serverError(w, err)
		}
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,'account.created')`, access.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "code": in.Code, "name": in.Name, "type": in.Type, "class": in.Class, "description": in.Description, "show_on_dashboard": in.ShowOnDashboard, "show_in_expense_claims": in.ShowInExpenseClaims, "enable_payments": in.EnablePayments, "tax_rate_code": in.TaxRateCode})
}

type journalLine struct {
	Description string `json:"description"`
	AccountID   int64  `json:"account_id"`
	DebitCents  int64  `json:"debit_cents"`
	CreditCents int64  `json:"credit_cents"`
	// Tracking is one option per tracking category for this line: {"<category_id>": <option_id>}.
	Tracking map[string]int64 `json:"tracking"`
}
type journalInput struct {
	OrgID          int64         `json:"org_id"`
	EntryDate      string        `json:"entry_date"`
	ReversalDate   string        `json:"reversal_date"`
	CopyNarration  bool          `json:"copy_narration"`
	Memo           string        `json:"memo"`
	IdempotencyKey string        `json:"idempotency_key"`
	Lines          []journalLine `json:"lines"`
}

func validJournal(in *journalInput) bool {
	in.Memo = strings.TrimSpace(in.Memo)
	entry, ok := parseDay(in.EntryDate)
	if !ok {
		return false
	}
	// An auto-reversing journal carries the date its reversal posts on. It must be later than the entry:
	// a same-day or past reversal would unwind the books before anyone reads them.
	// ponytail: a plain date, no scheduled runner; the reversal is written when the journal is created.
	in.ReversalDate = strings.TrimSpace(in.ReversalDate)
	if in.ReversalDate != "" {
		reverse, ok := parseDay(in.ReversalDate)
		if !ok || !reverse.After(entry) {
			return false
		}
	}
	if !utf8.ValidString(in.Memo) || len(in.Memo) > 500 || strings.ContainsRune(in.Memo, 0) || len(in.IdempotencyKey) < 1 || len(in.IdempotencyKey) > 100 || strings.ContainsRune(in.IdempotencyKey, 0) || len(in.Lines) < 2 || len(in.Lines) > 200 {
		return false
	}
	var debit, credit int64
	for _, l := range in.Lines {
		if !validTaxText(l.Description, 500) || l.AccountID <= 0 || l.DebitCents < 0 || l.CreditCents < 0 || (l.DebitCents == 0) == (l.CreditCents == 0) || l.DebitCents > maxMoneyCents || l.CreditCents > maxMoneyCents {
			return false
		}
		debit += l.DebitCents
		credit += l.CreditCents
		if debit > maxMoneyCents || credit > maxMoneyCents {
			return false
		}
	}
	return debit > 0 && debit == credit
}

func (a *app) createJournal(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in journalInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validJournal(&in) {
		writeError(w, 400, "invalid_journal")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" && access.Role != "accountant" {
		writeError(w, 403, "forbidden")
		return
	}
	if len(in.IdempotencyKey) > 93 {
		writeError(w, 400, "invalid_input")
		return
	}
	in.IdempotencyKey = "manual:" + in.IdempotencyKey
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
	in.OrgID = access.ID
	encoded, _ := json.Marshal(in)
	hash := sha256.Sum256(encoded)
	var id int64
	var existing []byte
	err = tx.QueryRow(ctx, `SELECT id,request_hash FROM journals WHERE org_id=$1 AND idempotency_key=$2`, access.ID, in.IdempotencyKey).Scan(&id, &existing)
	if err == nil {
		if !bytes.Equal(existing, hash[:]) {
			writeError(w, 409, "idempotency_conflict")
			return
		}
		writeJSON(w, 200, map[string]any{"id": id})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	for _, l := range in.Lines {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE org_id=$1 AND id=$2)`, access.ID, l.AccountID).Scan(&exists); err != nil {
			serverError(w, err)
			return
		}
		if !exists {
			writeError(w, 400, "invalid_account")
			return
		}
	}
	var reverseOn *time.Time
	if in.ReversalDate != "" {
		d, _ := parseDay(in.ReversalDate)
		reverseOn = &d
	}
	err = tx.QueryRow(ctx, `INSERT INTO journals(org_id,entry_date,memo,idempotency_key,request_hash,created_by,reversal_date) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, access.ID, in.EntryDate, in.Memo, in.IdempotencyKey, hash[:], u.ID, reverseOn).Scan(&id)
	if isPeriodClosed(err) {
		writeError(w, http.StatusUnprocessableEntity, "period_closed")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	lines := make([]postedLine, len(in.Lines))
	for i, l := range in.Lines {
		// Xero's "default narration to line description" fills only lines left blank; typed text always wins.
		desc := l.Description
		if in.CopyNarration && strings.TrimSpace(desc) == "" {
			desc = in.Memo
		}
		lines[i] = postedLine{AccountID: l.AccountID, Debit: l.DebitCents, Credit: l.CreditCents, Description: desc, Tracking: l.Tracking}
	}
	if err := insertJournalLines(ctx, tx, access.ID, id, lines); err != nil {
		serverError(w, err)
		return
	}
	if err := auditJournal(ctx, tx, access.ID, u.ID, id); err != nil {
		serverError(w, err)
		return
	}
	var reversalID int64
	if reverseOn != nil {
		// The reversal is a normal journal dated on the chosen day, so any report for that period nets it off.
		if err := tx.QueryRow(ctx, `INSERT INTO journals(org_id,entry_date,memo,idempotency_key,request_hash,created_by,reversal_of) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
			access.ID, *reverseOn, "Reversal of journal "+strconv.FormatInt(id, 10), "autoreverse:"+strconv.FormatInt(id, 10), hash[:], u.ID, id).Scan(&reversalID); err != nil {
			if isPeriodClosed(err) {
				writeError(w, http.StatusUnprocessableEntity, "period_closed")
				return
			}
			serverError(w, err)
			return
		}
		for i, l := range in.Lines {
			if _, err := tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents,description,tracking) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
				access.ID, reversalID, i+1, l.AccountID, l.CreditCents, l.DebitCents, l.Description, trackingJSON(l.Tracking)); err != nil {
				serverError(w, err)
				return
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "reversal_id": reversalID})
}

func (a *app) listJournals(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if status != "" && status != "posted" && status != "voided" {
		writeError(w, 400, "invalid_input")
		return
	}
	filter := ""
	if status == "voided" {
		filter = ` AND EXISTS (SELECT 1 FROM document_reversals dr WHERE dr.org_id=j.org_id AND dr.doc_kind='journal' AND dr.doc_id=j.id)`
	} else if status == "posted" {
		filter = ` AND NOT EXISTS (SELECT 1 FROM document_reversals dr WHERE dr.org_id=j.org_id AND dr.doc_kind='journal' AND dr.doc_id=j.id)`
	}
	rows, err := a.db.Query(r.Context(), `SELECT j.id,j.entry_date::text,j.memo,COALESCE(j.reversal_of,0),SUM(l.debit_cents),COALESCE((SELECT r.id FROM journals r WHERE r.org_id=j.org_id AND r.reversal_of=j.id),0),j.idempotency_key LIKE 'manual:%',
		CASE WHEN EXISTS (SELECT 1 FROM document_reversals dr WHERE dr.org_id=j.org_id AND dr.doc_kind='journal' AND dr.doc_id=j.id) THEN 'voided' ELSE 'posted' END
		FROM journals j JOIN journal_lines l ON l.org_id=j.org_id AND l.journal_id=j.id WHERE j.org_id=$1`+filter+` GROUP BY j.id ORDER BY j.entry_date DESC,j.id DESC LIMIT 200`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type journal struct {
		ID         int64  `json:"id"`
		EntryDate  string `json:"entry_date"`
		Memo       string `json:"memo"`
		ReversalOf int64  `json:"reversal_of"`
		TotalCents int64  `json:"total_cents"`
		ReversedBy int64  `json:"reversed_by"`
		Manual     bool   `json:"manual"`
		Status     string `json:"status"`
	}
	out := []journal{}
	for rows.Next() {
		var j journal
		if err := rows.Scan(&j.ID, &j.EntryDate, &j.Memo, &j.ReversalOf, &j.TotalCents, &j.ReversedBy, &j.Manual, &j.Status); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"journals": out})
}

func (a *app) getJournal(w http.ResponseWriter, r *http.Request, u currentUser) {
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
	rows, err := a.db.Query(r.Context(), `SELECT l.account_id,a.code,a.name,l.debit_cents,l.credit_cents,l.description FROM journal_lines l JOIN accounts a ON a.org_id=l.org_id AND a.id=l.account_id WHERE l.org_id=$1 AND l.journal_id=$2 ORDER BY l.line_no`, access.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type line struct {
		journalLine
		Code string `json:"code"`
		Name string `json:"name"`
	}
	out := []line{}
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.AccountID, &l.Code, &l.Name, &l.DebitCents, &l.CreditCents, &l.Description); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	if len(out) == 0 {
		writeError(w, 404, "not_found")
		return
	}
	writeJSON(w, 200, map[string]any{"lines": out})
}

func (a *app) reverseJournal(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID     int64  `json:"org_id"`
		EntryDate string `json:"entry_date"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if _, ok := parseDay(in.EntryDate); !ok {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if access.Role != "owner" && access.Role != "admin" && access.Role != "accountant" {
		writeError(w, 403, "forbidden")
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
	var existing int64
	err = tx.QueryRow(ctx, `SELECT id FROM journals WHERE org_id=$1 AND reversal_of=$2`, access.ID, id).Scan(&existing)
	if err == nil {
		writeJSON(w, 200, map[string]any{"id": existing})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	var originalDate, key string
	var reversalOf *int64
	err = tx.QueryRow(ctx, `SELECT entry_date::text,idempotency_key,reversal_of FROM journals WHERE org_id=$1 AND id=$2`, access.ID, id).Scan(&originalDate, &key, &reversalOf)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	// ponytail: only manual journals can be reversed here; invoice/payment reversals need linked document state changes.
	if reversalOf != nil || !strings.HasPrefix(key, "manual:") {
		writeError(w, 409, "document_reversal_required")
		return
	}
	if in.EntryDate < originalDate {
		writeError(w, 400, "invalid_input")
		return
	}
	encoded, _ := json.Marshal(in)
	hash := sha256.Sum256(encoded)
	var reversed int64
	err = tx.QueryRow(ctx, `INSERT INTO journals(org_id,entry_date,memo,idempotency_key,request_hash,created_by,reversal_of) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, access.ID, in.EntryDate, "Reversal of journal "+strconv.FormatInt(id, 10), "reverse:"+strconv.FormatInt(id, 10), hash[:], u.ID, id).Scan(&reversed)
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents,description) SELECT org_id,$3,line_no,account_id,credit_cents,debit_cents,description FROM journal_lines WHERE org_id=$1 AND journal_id=$2`, access.ID, id, reversed); err != nil {
		serverError(w, err)
		return
	}
	if err := recordAudit(ctx, tx, access.ID, u.ID, "journal", id, "journal.reversed", map[string]any{"reversal_journal_id": reversed}); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": reversed})
}

// postedLine is one side of a posted journal. The manual journal handler and the Peachtree import
// both go through insertJournalLines so the same constraints and line ordering apply to each.
type postedLine struct {
	AccountID   int64
	Debit       int64
	Credit      int64
	Description string
	Tracking    map[string]int64
}

func insertJournalLines(ctx context.Context, tx pgx.Tx, orgID, journalID int64, lines []postedLine) error {
	for i, l := range lines {
		if _, err := tx.Exec(ctx, `INSERT INTO journal_lines(org_id,journal_id,line_no,account_id,debit_cents,credit_cents,description,tracking) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
			orgID, journalID, i+1, l.AccountID, l.Debit, l.Credit, l.Description, trackingJSON(l.Tracking)); err != nil {
			return err
		}
	}
	return nil
}

func auditJournal(ctx context.Context, tx pgx.Tx, orgID, userID, journalID int64) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'journal.posted',jsonb_build_object('journal_id',$3::bigint))`, orgID, userID, journalID)
	return err
}

// fiscalYearStart returns the first day of the financial year containing now, given an MM-DD end.
func fiscalYearStart(end string, now time.Time) string {
	parts := strings.Split(end, "-")
	if len(parts) != 2 {
		return now.Format("2006") + "-01-01"
	}
	month, _ := strconv.Atoi(parts[0])
	dayOfMonth, _ := strconv.Atoi(parts[1])
	if month < 1 || month > 12 || dayOfMonth < 1 || dayOfMonth > 31 {
		return now.Format("2006") + "-01-01"
	}
	endThisYear := time.Date(now.Year(), time.Month(month), dayOfMonth, 0, 0, 0, 0, time.UTC)
	start := endThisYear.AddDate(0, 0, 1)
	if !now.After(endThisYear) {
		start = endThisYear.AddDate(-1, 0, 0).AddDate(0, 0, 1)
	}
	return start.Format("2006-01-02")
}
