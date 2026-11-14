package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// GST returns (Phase 3b). The return list follows the company's tax period; the F5 figures come from
// the ledger through the existing draft calculation, and filing freezes that draft with who filed it
// and when. Nothing is submitted to IRAS.

var gstPeriodKinds = map[string]bool{"monthly": true, "quarterly": true, "twice_monthly": true}

func lastDayOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC)
}

// gstPeriods lists every GST period in a calendar year for a filing frequency.
func gstPeriods(period string, year int) [][2]time.Time {
	out := [][2]time.Time{}
	hour := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	switch period {
	case "monthly":
		for m := 1; m <= 12; m++ {
			start := time.Date(year, time.Month(m), 1, 0, 0, 0, 0, time.UTC)
			out = append(out, [2]time.Time{start, lastDayOfMonth(start)})
		}
	case "quarterly":
		for m := 1; m <= 10; m += 3 {
			start := time.Date(year, time.Month(m), 1, 0, 0, 0, 0, time.UTC)
			out = append(out, [2]time.Time{start, lastDayOfMonth(start.AddDate(0, 2, 0))})
		}
	case "twice_monthly":
		for m := 1; m <= 12; m++ {
			first := time.Date(year, time.Month(m), 1, 0, 0, 0, 0, time.UTC)
			out = append(out, [2]time.Time{first, time.Date(year, time.Month(m), 15, 0, 0, 0, 0, time.UTC)})
			second := time.Date(year, time.Month(m), 16, 0, 0, 0, 0, time.UTC)
			out = append(out, [2]time.Time{second, lastDayOfMonth(second)})
		}
	default:
		_ = hour
	}
	return out
}

func gstDueDate(end time.Time) time.Time {
	// Due at the end of the month after the period ends; month arithmetic avoids month-length drift.
	return time.Date(end.Year(), end.Month()+2, 0, 0, 0, 0, 0, time.UTC)
}

// validGSTPeriod reports whether a start/end pair is exactly one period of the chosen frequency.
func validGSTPeriod(period, start, end string) bool {
	f, ok1 := parseDay(start)
	t, ok2 := parseDay(end)
	if !ok1 || !ok2 || t.Before(f) {
		return false
	}
	for _, p := range gstPeriods(period, f.Year()) {
		if p[0].Equal(f) && p[1].Equal(t) {
			return true
		}
	}
	return false
}

// listGSTReturns: GET /api/v1/tax/gst-returns?org_id[&year]
func (a *app) listGSTReturns(w http.ResponseWriter, r *http.Request, u currentUser) {
	q := r.URL.Query()
	org, ok := parseOrgID(w, q.Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	year := time.Now().Year()
	if s := q.Get("year"); s != "" {
		y, valid := parseOrgID(w, s)
		if !valid {
			return
		}
		year = int(y)
	}
	if year < 2000 || year > 2100 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	ctx := r.Context()
	var period, basis string
	if err := a.db.QueryRow(ctx, `SELECT tax_period, tax_basis FROM organizations WHERE id=$1`, access.ID).Scan(&period, &basis); err != nil {
		serverError(w, err)
		return
	}
	if !gstPeriodKinds[period] {
		period = "quarterly"
	}
	rows, err := a.db.Query(ctx, `SELECT r.period_start::text, r.period_end::text, r.due_date::text, r.status,
		COALESCE(r.filed_at::text,''), COALESCE(NULLIF(u.name,''), u.email, '')
		FROM gst_returns r LEFT JOIN users u ON u.id = r.filed_by
		WHERE r.org_id=$1 AND r.period_start >= $2::date AND r.period_start <= $3::date`, access.ID,
		time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type saved struct {
		due, status, filedAt, filedBy string
	}
	existing := map[string]saved{}
	for rows.Next() {
		var start, end string
		var s saved
		if err := rows.Scan(&start, &end, &s.due, &s.status, &s.filedAt, &s.filedBy); err != nil {
			serverError(w, err)
			return
		}
		existing[start] = s
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	out := []map[string]any{}
	for _, p := range gstPeriods(period, year) {
		start := day(p[0])
		row := map[string]any{
			"period_start": start, "period_end": day(p[1]), "due_date": day(gstDueDate(p[1])),
			"status": "draft", "filed_at": "", "filed_by": "",
		}
		if s, ok := existing[start]; ok {
			row["due_date"] = s.due
			row["status"] = s.status
			row["filed_at"] = s.filedAt
			row["filed_by"] = s.filedBy
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"year": year, "tax_period": period, "tax_basis": basis, "returns": out})
}

// fileGSTReturn: POST /api/v1/tax/gst-returns/file — computes the ledger draft and records the filing.
func (a *app) fileGSTReturn(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in struct {
		OrgID       int64  `json:"org_id"`
		PeriodStart string `json:"period_start"`
		PeriodEnd   string `json:"period_end"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if _, ok := parseDay(in.PeriodStart); !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if _, ok := parseDay(in.PeriodEnd); !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	var period string
	if err := a.db.QueryRow(ctx, `SELECT tax_period FROM organizations WHERE id=$1`, access.ID).Scan(&period); err != nil {
		serverError(w, err)
		return
	}
	if !validGSTPeriod(period, in.PeriodStart, in.PeriodEnd) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_gst_period")
		return
	}
	registered, err := gstRegisteredInPeriod(ctx, a.db, access.ID, in.PeriodStart, in.PeriodEnd)
	if err != nil {
		serverError(w, err)
		return
	}
	if !registered {
		writeError(w, http.StatusUnprocessableEntity, "gst_not_registered")
		return
	}
	status, payload := a.gstDraftPayload(ctx, access.ID, in.PeriodStart, in.PeriodEnd)
	if status != http.StatusOK {
		writeJSON(w, status, payload)
		return
	}
	payload["filed"] = true
	payload["status"] = "filed"
	payload["filed_by"] = u.Name
	if payload["filed_by"] == "" {
		payload["filed_by"] = u.Email
	}
	payload["filed_at"] = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.Marshal(payload)
	if err != nil {
		serverError(w, err)
		return
	}
	end, _ := parseDay(in.PeriodEnd)
	var id int64
	err = a.db.QueryRow(ctx, `INSERT INTO gst_returns (org_id, period_start, period_end, due_date, status, payload, filed_by, filed_at, created_by)
		VALUES ($1,$2::date,$3::date,$4::date,'filed',$5,$6,now(),$6) RETURNING id`,
		access.ID, in.PeriodStart, in.PeriodEnd, day(gstDueDate(end)), encoded, u.ID).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "already_filed")
			return
		}
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, payload)
}

// gstF5Report: GET /api/v1/reports/gst-f5?org_id&from&to — the F5 boxes for a period. A filed period
// shows the frozen payload; an unfiled one shows the live ledger draft.
func (a *app) gstF5Report(w http.ResponseWriter, r *http.Request, u currentUser) {
	q := r.URL.Query()
	org, ok := parseOrgID(w, q.Get("org_id"))
	if !ok {
		return
	}
	from, to, ok := ledgerRange(w, r)
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	ctx := r.Context()
	var payload []byte
	var filedAt string
	var filedBy string
	err := a.db.QueryRow(ctx, `SELECT r.payload, to_char(r.filed_at,'YYYY-MM-DD"T"HH24:MI:SSOF'),
		COALESCE(NULLIF(u.name,''), u.email, '')
		FROM gst_returns r LEFT JOIN users u ON u.id=r.filed_by
		WHERE r.org_id=$1 AND r.period_start=$2::date AND r.period_end=$3::date AND r.status='filed'`,
		access.ID, from, to).Scan(&payload, &filedAt, &filedBy)
	if err == nil {
		var body map[string]any
		if err := json.Unmarshal(payload, &body); err != nil {
			serverError(w, err)
			return
		}
		body["filed"] = true
		body["status"] = "filed"
		body["filed_at"] = filedAt
		body["filed_by"] = filedBy
		body["period_start"] = from
		body["period_end"] = to
		writeJSON(w, http.StatusOK, body)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	status, body := a.gstDraftPayload(ctx, access.ID, from, to)
	if status != http.StatusOK {
		writeJSON(w, status, body)
		return
	}
	body["filed"] = false
	body["status"] = "draft"
	body["filed_at"] = ""
	body["filed_by"] = ""
	writeJSON(w, http.StatusOK, body)
}
