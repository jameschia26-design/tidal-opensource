package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"tidal/backend/internal/ptb"
)

// Balance field offsets in the Peachtree chart: each period stores debit, credit and net, 36
// bytes apart, starting at 70.
const (
	chartDebit  = 70
	chartCredit = 82
	chartNet    = 94
	chartStride = 36
	customerBal = 162
	vendorBal   = 206
)

// ptbPreview is the review screen's whole payload: what was found, what was refused, and how the
// imported figures compare with the figures the owner supplied from their own reports.
type ptbPreview struct {
	ID             int64              `json:"id"`
	Status         string             `json:"status"`
	Counts         map[string]any     `json:"counts"`
	Documents      map[string]int     `json:"documents"`
	Exceptions     []map[string]any   `json:"exceptions"`
	Reconciliation *ptbReconciliation `json:"reconciliation"`
}

type ptbReconciliation struct {
	Period   int              `json:"period"`
	Actual   map[string]int64 `json:"actual"`
	Expected map[string]int64 `json:"expected"`
	Diffs    map[string]int64 `json:"differences"`
	Matched  bool             `json:"matched"`
	// Acknowledged names the figures whose difference the owner accepted, with the known reason.
	Acknowledged []string `json:"acknowledged"`
}

type ptbPreviewRequest struct {
	Period     *int   `json:"period"`
	Debit      *int64 `json:"trial_balance_debit"`
	Credit     *int64 `json:"trial_balance_credit"`
	Receivable *int64 `json:"receivables"`
	Payable    *int64 `json:"payables"`
	// AcceptPayables acknowledges the known limit that supplier ageing cannot be rebuilt from the
	// backup. Without it a payables difference blocks the commit, as any other difference does.
	AcceptPayables bool `json:"accept_payables_difference"`
}

// previewPTBImport lists what a staged upload contains and reconciles it against the figures the
// owner supplies. A mismatch of more than one cent marks the import unreconciled, which the commit
// step refuses.
func (a *app) previewPTBImport(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	if orgID == 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	id, ok := parseOrgID(w, r.PathValue("id"))
	if !ok || id == 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	var req ptbPreviewRequest
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req)
	}

	ctx := r.Context()
	var preview ptbPreview
	var stored map[string]any
	err := a.db.QueryRow(ctx, `SELECT status, record_counts, exceptions FROM ptb_imports WHERE id=$1 AND org_id=$2`, id, access.ID).
		Scan(&preview.Status, &stored, &preview.Exceptions)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	preview.ID = id
	tables, err := loadPTBTables(ctx, a.db, id)
	if err != nil {
		serverError(w, err)
		return
	}

	counts := map[string]any{
		"accounts":  len(tables["CHART.DAT"]),
		"customers": len(tables["CUSTOMER.DAT"]),
		"suppliers": len(tables["VENDOR.DAT"]),
	}
	kinds := map[string]int{}
	docs0, _, classifyErr := ptb.Classify(tables["JRNLHDR.DAT"], tables["JRNLROW.DAT"])
	if classifyErr != nil {
		// Classify stops at the first problem because posting must be safe. The reviewer still gets
		// the whole list, per record, from RowExceptions below.
		preview.Exceptions = append(preview.Exceptions, map[string]any{"table": "JRNLHDR.DAT", "reason": classifyErr.Error()})
	}
	for _, e := range ptb.RowExceptions(tables["JRNLHDR.DAT"], tables["JRNLROW.DAT"]) {
		preview.Exceptions = append(preview.Exceptions, map[string]any{
			"table": e.Table, "ordinal": e.Ordinal, "post_order": e.PostOrder, "reason": e.Reason,
		})
	}
	// A classified document can still fail to become a document: no value, or a party the backup does
	// not name. Those keep their journal but are named here, so no source document disappears silently.
	if docs, _, err := ptbDocuments(tables); err == nil {
		documented := map[uint32]bool{}
		for _, d := range docs {
			documented[d.PostOrder] = true
		}
		reference := map[uint32]string{}
		for _, h := range tables["JRNLHDR.DAT"] {
			if len(h) >= 103 {
				reference[binary.LittleEndian.Uint32(h[35:39])] = strings.TrimSpace(ptb.Text(h[82:103]))
			}
		}
		for _, d := range docs0 {
			// General journals are meant to stay journals only; naming them would bury the real skips.
			if documented[d.PostOrder] || d.Kind == ptb.Journal {
				continue
			}
			entry := map[string]any{
				"table": "JRNLHDR.DAT", "post_order": d.PostOrder, "kind": string(d.Kind),
				"reason": "documented in the journal only: no value or no matching customer/supplier in the backup",
			}
			if ref := reference[d.PostOrder]; ref != "" {
				entry["reference"] = ref
			}
			preview.Exceptions = append(preview.Exceptions, entry)
		}
	}
	for _, d := range docs0 {
		kinds[string(d.Kind)]++
	}
	preview.Counts = counts
	preview.Documents = kinds
	preview.Exceptions = mergedExceptions(stored, preview.Exceptions)

	period := 35
	if req.Period != nil {
		period = *req.Period
	}
	actual, err := ptbActuals(tables, period)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_backup")
		return
	}
	expected := map[string]int64{}
	for key, value := range map[string]*int64{
		"trial_balance_debit":  req.Debit,
		"trial_balance_credit": req.Credit,
		"receivables":          req.Receivable,
		"payables":             req.Payable,
	} {
		if value != nil {
			expected[key] = *value
		}
	}
	rec := &ptbReconciliation{Period: period, Actual: actual, Expected: expected, Diffs: map[string]int64{}, Matched: true}
	// A reconciliation needs something to reconcile against. With no figures supplied there is nothing
	// to agree, so the import is not reconciled and cannot be committed.
	if len(expected) == 0 {
		rec.Matched = false
	}
	for key, want := range expected {
		diff := actual[key] - want
		rec.Diffs[key] = diff
		if diff > 1 || diff < -1 {
			// The supplier side cannot be rebuilt from the backup; the owner may acknowledge that
			// specific difference. Any other figure must agree exactly.
			if key == "payables" && req.AcceptPayables {
				rec.Acknowledged = append(rec.Acknowledged, key)
				continue
			}
			rec.Matched = false
		}
	}
	preview.Reconciliation = rec

	if rec.Matched {
		encoded, err := json.Marshal(rec)
		if err != nil {
			serverError(w, err)
			return
		}
		if _, err := a.db.Exec(ctx, `UPDATE ptb_imports SET status='previewed', reconciliation=$2, updated_at=now() WHERE id=$1`, id, encoded); err != nil {
			serverError(w, err)
			return
		}
		preview.Status = "previewed"
	}
	writeJSON(w, http.StatusOK, preview)
}

// mergedExceptions keeps every refused table from the upload visible even after later edits.
func mergedExceptions(stored map[string]any, current []map[string]any) []map[string]any {
	seen := map[string]bool{}
	out := make([]map[string]any, 0, len(stored)+len(current))
	for _, entry := range current {
		key, _ := entry["table"].(string)
		// Only the table-level "skipped" entries are deduplicated; per-record exceptions stay.
		if _, perRecord := entry["ordinal"]; !perRecord {
			seen[key] = true
		}
		out = append(out, entry)
	}
	if skipped, ok := stored["skipped"].(map[string]any); ok {
		for table, reason := range skipped {
			if seen[table] {
				continue
			}
			text, _ := reason.(string)
			out = append(out, map[string]any{"table": table, "reason": text})
		}
	}
	return out
}

// ptbActuals sums the stored chart balances for one period and the party balances in the subledger.
func ptbActuals(tables map[string][][]byte, period int) (map[string]int64, error) {
	if period < 0 {
		return nil, errInvalidPeriod
	}
	out := map[string]int64{}
	for _, account := range tables["CHART.DAT"] {
		dr, err := ptb.PackedDecimal(sliceAt(account, chartDebit+period*chartStride))
		if err != nil {
			return nil, err
		}
		cr, err := ptb.PackedDecimal(sliceAt(account, chartCredit+period*chartStride))
		if err != nil {
			return nil, err
		}
		net, err := ptb.PackedDecimal(sliceAt(account, chartNet+period*chartStride))
		if err != nil {
			return nil, err
		}
		out["trial_balance_debit"] += toCents(dr)
		out["trial_balance_credit"] += toCents(cr)
		out["trial_balance_net"] += toCents(net)
	}
	for _, customer := range tables["CUSTOMER.DAT"] {
		amount, err := ptb.PackedDecimal(sliceAt(customer, customerBal))
		if err != nil {
			return nil, err
		}
		out["receivables"] += toCents(amount)
	}
	for _, vendor := range tables["VENDOR.DAT"] {
		amount, err := ptb.PackedDecimal(sliceAt(vendor, vendorBal))
		if err != nil {
			return nil, err
		}
		out["payables"] += toCents(amount)
	}
	return out, nil
}

var errInvalidPeriod = errors.New("invalid balance period")

// toCents converts a value scaled by 1e9 into whole cents, rounding to the nearest cent.
func toCents(v int64) int64 {
	if v < 0 {
		return -((-v + 5_000_000) / 10_000_000)
	}
	return (v + 5_000_000) / 10_000_000
}

func sliceAt(row []byte, off int) []byte {
	if off < 0 || off+12 > len(row) {
		return nil
	}
	return row[off : off+12]
}

// loadPTBTables reads every staged record back into memory, grouped by table.
func loadPTBTables(ctx context.Context, db interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, id int64) (map[string][][]byte, error) {
	rows, err := db.Query(ctx, `SELECT table_name, payload FROM ptb_import_records WHERE import_id=$1 ORDER BY table_name, ordinal`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tables := map[string][][]byte{}
	for rows.Next() {
		var name string
		var payload []byte
		if err := rows.Scan(&name, &payload); err != nil {
			return nil, err
		}
		tables[name] = append(tables[name], payload)
	}
	return tables, rows.Err()
}
