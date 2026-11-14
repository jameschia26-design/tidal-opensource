package main

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

type leadProfile struct {
	CompanyName string `json:"company_name"`
	Phone       string `json:"phone"`
	JobTitle    string `json:"job_title"`
	Website     string `json:"website"`
	Notes       string `json:"notes"`
}

func validLead(in *leadIn) bool {
	if !utf8.ValidString(in.JobTitle) || strings.ContainsRune(in.JobTitle, 0) || utf8.RuneCountInString(in.JobTitle) > 100 {
		return false
	}
	p := contactInput{Role: "customer", contactProfile: contactProfile{CompanyName: in.CompanyName, Phone: in.Phone, Website: in.Website, Notes: in.Notes}}
	return validContactProfile(&p)
}

func (a *app) convertLead(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID     int64 `json:"org_id"`
		ContactID int64 `json:"contact_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.ContactID < 0 {
		writeError(w, 400, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	// One company lock prevents concurrent lead conversions creating duplicate customer identities.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	var name, email, stage string
	var p leadProfile
	var linked *int64
	err = tx.QueryRow(ctx, `SELECT name,email,stage,company_name,phone,job_title,website,notes,contact_id FROM leads WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&name, &email, &stage, &p.CompanyName, &p.Phone, &p.JobTitle, &p.Website, &p.Notes, &linked)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if linked != nil {
		writeJSON(w, 200, map[string]any{"contact_id": *linked})
		return
	}
	if stage != "qualified" {
		writeError(w, 409, "lead_not_qualified")
		return
	}
	contactID := in.ContactID
	if contactID == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contacts WHERE org_id=$1 AND (lower(name)=lower($2) OR ($3<>'' AND email=$3)))`, access.ID, name, email).Scan(&exists); err != nil {
			serverError(w, err)
			return
		}
		if exists {
			writeError(w, 409, "contact_match_exists")
			return
		}
		err = tx.QueryRow(ctx, `INSERT INTO contacts(org_id,name,email,kind,created_by,company_name,phone,website,notes) VALUES($1,$2,$3,'customer',$4,$5,$6,$7,$8) RETURNING id`, access.ID, name, email, u.ID, p.CompanyName, p.Phone, p.Website, p.Notes).Scan(&contactID)
		if err != nil {
			serverError(w, err)
			return
		}
	} else {
		var kind string
		err = tx.QueryRow(ctx, `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, contactID).Scan(&kind)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 400, "invalid_input")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if kind == "supplier" {
			if _, err := tx.Exec(ctx, `UPDATE contacts SET kind='both',updated_at=now() WHERE org_id=$1 AND id=$2`, access.ID, contactID); err != nil {
				serverError(w, err)
				return
			}
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE leads SET contact_id=$3,updated_at=now() WHERE org_id=$1 AND id=$2`, access.ID, id, contactID); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'lead.converted',jsonb_build_object('lead_id',$3::bigint,'contact_id',$4::bigint))`, access.ID, u.ID, id, contactID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"contact_id": contactID})
}

type activityInput struct {
	OrgID   int64  `json:"org_id"`
	LeadID  int64  `json:"lead_id"`
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	DueDate string `json:"due_date"`
	Notes   string `json:"notes"`
}

func (a *app) createActivity(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in activityInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Subject = strings.TrimSpace(in.Subject)
	if in.LeadID <= 0 || !validStage(in.Kind, "task", "call", "meeting", "email", "note") || in.Subject == "" || !utf8.ValidString(in.Subject) || strings.ContainsRune(in.Subject, 0) || utf8.RuneCountInString(in.Subject) > 200 || !utf8.ValidString(in.Notes) || strings.ContainsRune(in.Notes, 0) || utf8.RuneCountInString(in.Notes) > 4000 {
		writeError(w, 400, "invalid_input")
		return
	}
	var due *time.Time
	if in.DueDate != "" {
		date, ok := parseDay(in.DueDate)
		if !ok {
			writeError(w, 400, "invalid_input")
			return
		}
		due = &date
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO activities(org_id,lead_id,kind,subject,due_date,notes,created_by) SELECT $1,$2,$3,$4,$5,$6,$7 WHERE EXISTS(SELECT 1 FROM leads WHERE org_id=$1 AND id=$2) RETURNING id`, access.ID, in.LeadID, in.Kind, in.Subject, due, in.Notes, u.ID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 400, "invalid_input")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,'activity.created')`, access.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *app) listActivities(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	leadID, ok := parseOrgID(w, r.URL.Query().Get("lead_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT id,lead_id,kind,subject,COALESCE(due_date::text,''),notes,completed_at IS NOT NULL FROM activities WHERE org_id=$1 AND ($2::bigint=0 OR lead_id=$2) ORDER BY completed_at NULLS FIRST,due_date NULLS LAST,id DESC LIMIT 200`, access.ID, leadID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type activity struct {
		ID        int64  `json:"id"`
		LeadID    int64  `json:"lead_id"`
		Kind      string `json:"kind"`
		Subject   string `json:"subject"`
		DueDate   string `json:"due_date"`
		Notes     string `json:"notes"`
		Completed bool   `json:"completed"`
	}
	out := []activity{}
	for rows.Next() {
		var v activity
		if err := rows.Scan(&v.ID, &v.LeadID, &v.Kind, &v.Subject, &v.DueDate, &v.Notes, &v.Completed); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"activities": out})
}

func (a *app) completeActivity(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		OrgID int64 `json:"org_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE activities SET completed_at=COALESCE(completed_at,now()) WHERE org_id=$1 AND id=$2`, access.ID, id)
	if err != nil {
		serverError(w, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "not_found")
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action) VALUES($1,$2,'activity.completed')`, access.ID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "completed": true})
}

type dealProfile struct {
	ContactID         *int64 `json:"contact_id"`
	ExpectedCloseDate string `json:"expected_close_date"`
	Probability       int    `json:"probability"`
	DealType          string `json:"deal_type"`
	Source            string `json:"source"`
	NextStep          string `json:"next_step"`
	Notes             string `json:"notes"`
}

func (a *app) saveDeal(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in dealIn
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	in.DealType = strings.TrimSpace(in.DealType)
	in.Source = strings.TrimSpace(in.Source)
	if in.Title == "" || !validTaxText(in.Title, 200) || in.AmountCents < 0 || in.AmountCents > maxMoneyCents || in.LeadID < 0 || (in.ContactID != nil && *in.ContactID <= 0) || in.Probability < 0 || in.Probability > 100 || !validTaxText(in.DealType, 100) || !validTaxText(in.Source, 100) || !validTaxText(in.NextStep, 1000) || !validTaxText(in.Notes, 4000) {
		writeError(w, 400, "invalid_input")
		return
	}
	var closeDate *time.Time
	if in.ExpectedCloseDate != "" {
		date, ok := parseDay(in.ExpectedCloseDate)
		if !ok {
			writeError(w, 400, "invalid_input")
			return
		}
		closeDate = &date
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	var leadID *int64
	if in.LeadID != 0 {
		var linked *int64
		err = tx.QueryRow(ctx, `SELECT contact_id FROM leads WHERE org_id=$1 AND id=$2 FOR SHARE`, access.ID, in.LeadID).Scan(&linked)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 400, "invalid_input")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if linked != nil && in.ContactID != nil && *linked != *in.ContactID {
			writeError(w, 400, "deal_customer_mismatch")
			return
		}
		leadID = &in.LeadID
	}
	if in.ContactID != nil {
		var kind string
		err = tx.QueryRow(ctx, `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2 FOR SHARE`, access.ID, *in.ContactID).Scan(&kind)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 400, "invalid_customer")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if kind != "customer" && kind != "both" {
			writeError(w, 400, "invalid_customer")
			return
		}
	}
	var id int64
	status, action := 201, "deal.created"
	if r.Method == http.MethodPut {
		id, ok = pathID(w, r)
		if !ok {
			return
		}
		var stage string
		err = tx.QueryRow(ctx, `SELECT stage FROM deals WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&stage)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if stage != "open" {
			writeError(w, 409, "deal_closed")
			return
		}
		_, err = tx.Exec(ctx, `UPDATE deals SET lead_id=$3,title=$4,amount_cents=$5,contact_id=$6,expected_close_date=$7,probability=$8,deal_type=$9,source=$10,next_step=$11,notes=$12,updated_at=now() WHERE org_id=$1 AND id=$2`, access.ID, id, leadID, in.Title, in.AmountCents, in.ContactID, closeDate, in.Probability, in.DealType, in.Source, in.NextStep, in.Notes)
		status, action = 200, "deal.updated"
	} else {
		err = tx.QueryRow(ctx, `INSERT INTO deals(org_id,lead_id,title,amount_cents,contact_id,expected_close_date,probability,deal_type,source,next_step,notes,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`, access.ID, leadID, in.Title, in.AmountCents, in.ContactID, closeDate, in.Probability, in.DealType, in.Source, in.NextStep, in.Notes, u.ID).Scan(&id)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,$3,jsonb_build_object('deal_id',$4::bigint))`, access.ID, u.ID, action, id); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, status, map[string]any{"id": id, "lead_id": leadID, "title": in.Title, "amount_cents": in.AmountCents, "stage": "open", "contact_id": in.ContactID, "expected_close_date": in.ExpectedCloseDate, "probability": in.Probability, "deal_type": in.DealType, "source": in.Source, "next_step": in.NextStep, "notes": in.Notes})
}
