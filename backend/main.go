// Command tidal runs the Tidal backend API. "tidal migrate" applies migrations and exits.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var buildCommit = "development"

type app struct {
	cfg    config
	db     *pgxpool.Pool
	mail   *mailer
	google *googleState
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("tidal: %v", err)
	}
}

func run(args []string) error {
	// The deployment stamps the exact commit into the running container, so /readyz can prove which
	// build is live even when the image was built before the release hook ran.
	if v := os.Getenv("TIDAL_COMMIT"); v != "" {
		buildCommit = v
	}
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := migrate(ctx, pool); err != nil {
		return err
	}
	// Seeded password admin for the first sign-in: idempotent, never overwrites without opt-in.
	if err := ensureAdmin(ctx, pool, cfg); err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "seed-preview" {
		return seedPreview(ctx, pool, os.Stdout)
	}
	if len(args) > 0 && args[0] == "seed-demo-2025" {
		// Explicit operator command: needs the admin from TIDAL_ADMIN_EMAIL and returns before the server.
		return seedDemo2025(ctx, &app{cfg: cfg, db: pool, mail: newMailer(cfg)}, os.Stdout)
	}
	if len(args) > 0 && args[0] == "migrate" {
		log.Print("migrations up to date")
		return nil
	}

	a := &app{cfg: cfg, db: pool, mail: newMailer(cfg)}
	if a.google, err = newGoogle(ctx, cfg); err != nil {
		// Fail closed: Google sign-in answers 503 until configured. Other sign-in methods are unaffected.
		if !errors.Is(err, errGoogleNotConfigured) {
			log.Printf("google sign-in disabled: %v", err)
		}
		a.google = nil
	}
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           a.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log.Printf("listening on %s (production=%t, email configured=%t)", cfg.ListenAddr, cfg.Production, a.mail.configured())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", a.readyz)

	mux.HandleFunc("POST /api/v1/auth/signup", a.signup)
	mux.HandleFunc("POST /api/v1/auth/signup/verify", a.signupVerify)
	mux.HandleFunc("POST /api/v1/auth/login", a.login)
	mux.HandleFunc("POST /api/v1/auth/login/verify", a.loginVerify)
	mux.HandleFunc("POST /api/v1/auth/password/forgot", a.passwordForgot)
	mux.HandleFunc("POST /api/v1/auth/password/reset", a.passwordReset)
	mux.HandleFunc("POST /api/v1/auth/logout", a.logout)
	mux.HandleFunc("GET /api/v1/auth/google/start", a.googleStart)
	mux.HandleFunc("GET /api/v1/auth/google/callback", a.googleCallback)
	mux.Handle("GET /api/v1/me", a.requireUser(a.me))
	mux.Handle("PATCH /api/v1/me", a.requireUser(a.updateProfile))

	mux.Handle("POST /api/v1/admin/invitations", a.requireUser(a.adminCreateInvitation))
	mux.Handle("GET /api/v1/admin/users", a.requireUser(a.adminListUsers))
	mux.Handle("PATCH /api/v1/admin/users/{id}", a.requireUser(a.adminUpdateUser))
	mux.Handle("GET /api/v1/organizations", a.requireUser(a.listOrgs))
	mux.Handle("POST /api/v1/organizations", a.requireUser(a.createOrg))
	mux.Handle("POST /api/v1/organizations/{id}/books", a.requireUser(a.createBook))
	mux.Handle("GET /api/v1/organizations/{id}", a.requireUser(a.getCompany))
	mux.Handle("PUT /api/v1/organizations/{id}", a.requireUser(a.updateCompany))
	mux.Handle("GET /api/v1/organizations/{id}/logo", a.requireUser(a.getCompanyLogo))
	mux.Handle("POST /api/v1/organizations/{id}/logo", a.requireUser(a.uploadCompanyLogo))
	mux.Handle("DELETE /api/v1/organizations/{id}/logo", a.requireUser(a.deleteCompanyLogo))
	mux.Handle("GET /api/v1/contacts", a.requireUser(a.listContacts))
	mux.Handle("GET /api/v1/invitations", a.requireUser(a.myInvitations))
	mux.Handle("POST /api/v1/invitations/{id}/accept", a.requireUser(a.acceptInvitation))
	mux.Handle("PATCH /api/v1/members/{id}", a.requireUser(a.changeMember))
	mux.Handle("PATCH /api/v1/members/{id}/permissions", a.requireUser(a.setMemberPermissions))
	mux.Handle("GET /api/v1/login-history", a.requireUser(a.loginHistory))
	mux.Handle("GET /api/v1/search", a.requireUser(a.globalSearch))
	mux.Handle("GET /api/v1/saved-reports", a.requireUser(a.listSavedReports))
	mux.Handle("POST /api/v1/saved-reports", a.requireUser(a.saveSavedReport))
	mux.Handle("PUT /api/v1/saved-reports/{id}", a.requireUser(a.saveSavedReport))
	mux.Handle("DELETE /api/v1/saved-reports/{id}", a.requireUser(a.deleteSavedReport))
	mux.Handle("GET /api/v1/backorders", a.requireUser(a.listBackorders))
	mux.Handle("POST /api/v1/backorders/{id}/fulfil", a.requireUser(a.fulfilBackorderNow))
	mux.Handle("GET /api/v1/projects/{id}/tasks", a.requireUser(a.projectTasks))
	mux.Handle("POST /api/v1/projects/{id}/tasks", a.requireUser(a.saveProjectTask))
	mux.Handle("PATCH /api/v1/project-tasks/{id}", a.requireUser(a.saveProjectTask))
	mux.Handle("GET /api/v1/projects/{id}/members", a.requireUser(a.projectMembers))
	mux.Handle("POST /api/v1/projects/{id}/members", a.requireUser(a.saveProjectMember))
	mux.Handle("DELETE /api/v1/projects/{id}/members", a.requireUser(a.saveProjectMember))
	mux.Handle("GET /api/v1/assets/{id}/revaluations", a.requireUser(a.listAssetRevaluations))
	mux.Handle("POST /api/v1/assets/{id}/revalue", a.requireUser(a.revalueAsset))
	mux.Handle("POST /api/v1/assets/{id}/revaluations/{rid}/rollback", a.requireUser(a.rollbackAssetRevaluation))
	mux.Handle("POST /api/v1/invoices/{id}/approve-and-email", a.requireUser(a.approveAndSendInvoice))
	mux.Handle("POST /api/v1/invoices/{id}/submit", a.requireUser(a.submitInvoice))
	mux.Handle("POST /api/v1/bills/{id}/approve-and-email", a.requireUser(a.approveAndSendBill))
	mux.Handle("GET /api/v1/reports/backorders", a.requireUser(a.backorderReport))
	mux.Handle("GET /api/v1/reports/blank", a.requireUser(a.blankReport))
	mux.Handle("GET /api/v1/time/timer", a.requireUser(a.getTimer))
	mux.Handle("POST /api/v1/time/timer/start", a.requireUser(a.startTimer))
	mux.Handle("POST /api/v1/time/timer/stop", a.requireUser(a.stopTimer))
	mux.Handle("DELETE /api/v1/members/{id}", a.requireUser(a.changeMember))
	mux.Handle("GET /api/v1/members", a.requireUser(a.listMembers))
	mux.Handle("POST /api/v1/members/invite", a.requireUser(a.inviteMember))
	mux.Handle("GET /api/v1/members/invitations", a.requireUser(a.listMemberInvites))
	mux.Handle("DELETE /api/v1/members/invitations/{id}", a.requireUser(a.revokeMemberInvite))
	mux.Handle("POST /api/v1/contacts", a.requireUser(a.createContact))
	mux.Handle("POST /api/v1/imports/ptb", a.requireUser(a.uploadPTBImport))
	mux.Handle("POST /api/v1/imports/ptb/{id}/preview", a.requireUser(a.previewPTBImport))
	mux.Handle("POST /api/v1/imports/ptb/{id}/commit", a.requireUser(a.commitPTBImport))
	mux.Handle("POST /api/v1/contacts/import", a.requireUser(a.importContacts))
	mux.Handle("GET /api/v1/contacts/export", a.requireUser(a.exportContacts))
	mux.Handle("PUT /api/v1/contacts/{id}", a.requireUser(a.updateContact))
	mux.Handle("GET /api/v1/contacts/{id}/addresses", a.requireUser(a.contactAddresses))
	mux.Handle("POST /api/v1/contacts/{id}/addresses", a.requireUser(a.contactAddresses))
	mux.Handle("DELETE /api/v1/contact-addresses/{id}", a.requireUser(a.deleteContactAddress))
	mux.Handle("POST /api/v1/contacts/{id}/archive", a.requireUser(a.archiveContact))
	mux.Handle("GET /api/v1/contacts/{id}/documents", a.requireUser(a.contactDocuments))
	mux.Handle("GET /api/v1/contact-groups", a.requireUser(a.listContactGroups))
	mux.Handle("POST /api/v1/contact-groups", a.requireUser(a.saveContactGroup))
	mux.Handle("PUT /api/v1/contact-groups/{id}", a.requireUser(a.saveContactGroup))
	mux.Handle("DELETE /api/v1/contact-groups/{id}", a.requireUser(a.deleteContactGroup))
	mux.Handle("POST /api/v1/contact-groups/{id}/members", a.requireUser(a.addGroupMembers))
	mux.Handle("DELETE /api/v1/contact-groups/{id}/members/{contactId}", a.requireUser(a.removeGroupMember))
	mux.Handle("GET /api/v1/contact-smart-lists", a.requireUser(a.listContactSmartLists))
	mux.Handle("POST /api/v1/contact-smart-lists", a.requireUser(a.saveContactSmartList))
	mux.Handle("DELETE /api/v1/contact-smart-lists/{id}", a.requireUser(a.deleteContactSmartList))

	mux.Handle("GET /api/v1/entities/{kind}/{id}/attachments", a.requireUser(a.entityAttachments))
	mux.Handle("POST /api/v1/entities/{kind}/{id}/attachments", a.requireUser(a.entityAttachments))
	mux.Handle("GET /api/v1/attachments/{id}", a.requireUser(a.downloadAttachment))
	mux.Handle("GET /api/v1/custom-fields", a.requireUser(a.listCustomFields))
	mux.Handle("POST /api/v1/custom-fields", a.requireUser(a.createCustomField))
	mux.Handle("GET /api/v1/salespeople", a.requireUser(a.listSalespeople))
	mux.Handle("POST /api/v1/salespeople", a.requireUser(a.createSalesperson))
	mux.Handle("POST /api/v1/salespeople/{id}/active", a.requireUser(a.setDefinitionActive("salespeople")))
	mux.Handle("GET /api/v1/payment-terms", a.requireUser(a.listPaymentTerms))
	mux.Handle("POST /api/v1/payment-terms", a.requireUser(a.createPaymentTerm))
	mux.Handle("GET /api/v1/setup-progress", a.requireUser(a.setupProgress))
	mux.Handle("GET /api/v1/notifications", a.requireUser(a.notifications))
	mux.Handle("GET /api/v1/scorecard-targets", a.requireUser(a.scorecardTargets))
	mux.Handle("PUT /api/v1/scorecard-targets", a.requireUser(a.scorecardTargets))
	mux.Handle("GET /api/v1/control-accounts", a.requireUser(a.controlAccounts))
	mux.Handle("GET /api/v1/sales-rounding", a.requireUser(a.salesRounding))
	mux.Handle("PUT /api/v1/sales-rounding", a.requireUser(a.salesRounding))
	mux.Handle("GET /api/v1/events", a.requireUser(a.listEvents))
	mux.Handle("GET /api/v1/payment-drafts", a.requireUser(a.paymentDrafts))
	mux.Handle("POST /api/v1/payment-drafts", a.requireUser(a.paymentDrafts))
	mux.Handle("PUT /api/v1/payment-drafts/{id}", a.requireUser(a.paymentDrafts))
	mux.Handle("DELETE /api/v1/payment-drafts/{id}", a.requireUser(a.deletePaymentDraft))
	mux.Handle("GET /api/v1/time-entries", a.requireUser(a.listTimeEntries))
	mux.Handle("POST /api/v1/time-entries", a.requireUser(a.createTimeEntry))
	mux.Handle("GET /api/v1/projects", a.requireUser(a.listProjects))
	mux.Handle("POST /api/v1/projects", a.requireUser(a.createProject))
	mux.Handle("PATCH /api/v1/projects/{id}", a.requireUser(a.updateProject))
	mux.Handle("POST /api/v1/projects/{id}/active", a.requireUser(a.setDefinitionActive("projects")))
	mux.Handle("PUT /api/v1/control-accounts", a.requireUser(a.controlAccounts))
	mux.Handle("GET /api/v1/payment-modes", a.requireUser(a.listPaymentModes))
	mux.Handle("POST /api/v1/payment-modes", a.requireUser(a.createPaymentMode))
	mux.Handle("POST /api/v1/payment-modes/{id}/active", a.requireUser(a.setDefinitionActive("payment_modes")))
	mux.Handle("POST /api/v1/payment-terms/{id}/active", a.requireUser(a.setDefinitionActive("payment_terms")))
	mux.Handle("POST /api/v1/custom-fields/{id}/active", a.requireUser(a.setDefinitionActive("custom_fields")))
	mux.Handle("GET /api/v1/tracking/categories", a.requireUser(a.listTrackingCategories))
	mux.Handle("POST /api/v1/tracking/categories", a.requireUser(a.saveTrackingCategory))
	mux.Handle("PUT /api/v1/tracking/categories/{id}", a.requireUser(a.saveTrackingCategory))
	mux.Handle("POST /api/v1/tracking/options", a.requireUser(a.saveTrackingOption))
	mux.Handle("PUT /api/v1/tracking/options/{id}", a.requireUser(a.saveTrackingOption))
	mux.Handle("GET /api/v1/reporting-tags", a.requireUser(a.listReportingTags))
	mux.Handle("POST /api/v1/reporting-tags", a.requireUser(a.createReportingTag))
	mux.Handle("POST /api/v1/reporting-tags/{id}/active", a.requireUser(a.setDefinitionActive("reporting_tags")))
	mux.Handle("GET /api/v1/entities/{kind}/{id}/extras", a.requireUser(a.entityExtras))
	mux.Handle("PUT /api/v1/entities/{kind}/{id}/extras", a.requireUser(a.entityExtras))
	mux.Handle("GET /api/v1/items", a.requireUser(a.listItems))
	mux.Handle("POST /api/v1/items", a.requireUser(a.saveItem))
	mux.Handle("PUT /api/v1/items/{id}", a.requireUser(a.saveItem))
	mux.Handle("GET /api/v1/stock/adjustments", a.requireUser(a.listStockAdjustments))
	mux.Handle("POST /api/v1/stock/adjustments", a.requireUser(a.createStockAdjustment))
	mux.Handle("POST /api/v1/stock/stocktakes", a.requireUser(a.createStocktake))
	mux.Handle("GET /api/v1/stock/locations", a.requireUser(a.listStockLocations))
	mux.Handle("POST /api/v1/stock/locations", a.requireUser(a.createStockLocation))
	mux.Handle("POST /api/v1/stock/transfers", a.requireUser(a.createStockTransfer))
	mux.Handle("GET /api/v1/reports/stock-by-location", a.requireUser(a.stockByLocation))
	a.coreRoutes(mux)

	// Modules named in the plan but not built in this release answer explicitly, never fake success.
	for _, p := range []string{"/api/v1/banking"} {
		mux.HandleFunc(p, notAvailable)
		mux.HandleFunc(p+"/", notAvailable)
	}
	return a.noStore(a.originCheck(mux))
}

func (a *app) noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// originCheck rejects state-changing requests whose Origin is not the public app origin.
// The Next.js proxy must forward the browser Origin header unchanged.
func (a *app) originCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if got := r.Header.Get("Origin"); got == "" || got != a.cfg.PublicOrigin {
				writeError(w, http.StatusForbidden, "origin_rejected")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *app) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.db.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "commit": buildCommit})
}

func notAvailable(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_available")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError uses the contract shape {"error": "<snake_case_code>"}. Details stay in server logs.
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

// serverError logs the detail and returns a generic code so no internals leak.
func serverError(w http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	writeError(w, http.StatusInternalServerError, "internal_error")
}

// decodeJSON reads one strict JSON object (unknown fields and trailing data rejected, 1 MiB cap).
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}
