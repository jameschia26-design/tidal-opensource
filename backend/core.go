package main

// Core accounting and CRM: invoices (draft -> posted -> payments), ledger reports, leads and deals,
// and deterministic tax worksheets built on internal/taxrules.
//
// ponytail: SGD only; GST support limited to SR/NONE invoices; no credit notes, quotes,
// recurring billing, stock, multi-currency, payment allocation across invoices, or document reversals.
// Manual journal reversals live in accounting.go.
// Tax worksheets take caller-entered lines and do not yet read the ledger; box 13 revenue is entered.
// Upgrade path: each feature as its own module with its own acceptance tests.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"tidal/backend/internal/money"
	"tidal/backend/internal/taxrules"
)

const (
	maxMoneyCents = 100000000000000 // bounds one amount well inside int64
	maxQty        = 1000000
	maxInvLines   = 200
	maxListRows   = 200

	acctBank  = "1000"
	acctAR    = "1100"
	acctSales = "4000"
	// Inventory control pair (D3): one company-wide asset and cost-of-sales account, item override via purchase_account_id.
	acctInventory = "1300"
	acctAssetCost = "1500"
	acctCOGS      = "5100"
	// Manual stock changes are an adjustment expense, so cost of sales stays the cost of goods sold.
	acctStockAdj = "6600"
	// acctOBE holds the other side of opening balances: what the business was worth on its start date.
	acctOBE = "3900"
	// acctRevaluation holds the other side of fixed-asset revaluations, gains and losses alike.
	acctRevaluation = "3300"
)

// seedAccounts are created per organization on first write. Other accounts are not seeded here.
var seedAccounts = []struct{ code, name, typ string }{
	{acctBank, "Bank", "asset"},
	{acctAR, "Accounts Receivable", "asset"},
	{acctSales, "Sales", "revenue"},
	{acctInventory, "Inventory", "asset"},
	{acctAssetCost, "Fixed assets", "asset"},
	{acctCOGS, "Cost of Sales", "expense"},
	{acctStockAdj, "Inventory adjustments", "expense"},
	// One GST liability holds output and input tax, matching Xero's single GST line.
	{"2300", "GST", "liability"},
	{acctAP, "Accounts Payable", "liability"},
	{acctOBE, "Opening Balance Equity", "equity"},
	{acctRevaluation, "Revaluation reserve", "equity"},
}

// leadNext lists allowed lead stage moves. lost is terminal.
var leadNext = map[string]map[string]bool{
	"new":       {"contacted": true, "qualified": true, "lost": true},
	"contacted": {"qualified": true, "lost": true},
	"qualified": {"lost": true},
}

// dealNext lists allowed deal stage moves. won and lost are terminal.
var dealNext = map[string]map[string]bool{
	"open": {"won": true, "lost": true},
}

type scanner interface{ Scan(dest ...any) error }

func (a *app) coreRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/accounts", a.requireUser(a.listAccounts))
	mux.Handle("POST /api/v1/accounts", a.requireUser(a.createAccount))
	mux.Handle("PUT /api/v1/accounts/{id}", a.requireUser(a.updateAccountTax))
	mux.Handle("POST /api/v1/accounts/bulk-tax-rate", a.requireUser(a.bulkAccountTax))
	mux.Handle("GET /api/v1/transfers", a.requireUser(a.listTransfers))
	mux.Handle("POST /api/v1/transfers", a.requireUser(a.createTransfer))
	mux.Handle("GET /api/v1/repeating-journals", a.requireUser(a.listRepeatingJournals))
	mux.Handle("POST /api/v1/repeating-journals", a.requireUser(a.createRepeatingJournal))
	mux.Handle("POST /api/v1/repeating-journals/active", a.requireUser(a.stopRepeatingJournal))
	mux.Handle("POST /api/v1/repeating-journals/run", a.requireUser(a.runRepeatingJournals))
	mux.Handle("GET /api/v1/journals", a.requireUser(a.listJournals))
	mux.Handle("POST /api/v1/journals", a.requireUser(a.createJournal))
	mux.Handle("GET /api/v1/journals/{id}", a.requireUser(a.getJournal))
	mux.Handle("POST /api/v1/journals/{id}/reverse", a.requireUser(a.reverseJournal))
	mux.Handle("POST /api/v1/journals/{id}/void", a.requireUser(a.voidJournal))
	mux.Handle("GET /api/v1/purchases-overview", a.requireUser(a.purchasesOverview))
	mux.Handle("GET /api/v1/bills", a.requireUser(a.listBills))
	mux.Handle("POST /api/v1/bills", a.requireUser(a.saveBill))
	mux.Handle("GET /api/v1/bills/{id}", a.requireUser(a.getBill))
	mux.Handle("PUT /api/v1/bills/{id}", a.requireUser(a.saveBill))
	mux.Handle("POST /api/v1/bills/{id}/post", a.requireUser(a.postBill))
	mux.Handle("POST /api/v1/bills/{id}/submit", a.requireUser(a.submitBill))
	mux.Handle("POST /api/v1/bills/{id}/payments", a.requireUser(a.payBill))
	mux.Handle("POST /api/v1/opening-documents", a.requireUser(a.createOpeningDocument))
	mux.Handle("GET /api/v1/export/company", a.requireUser(a.exportCompany))
	mux.Handle("POST /api/v1/bills/{id}/credit-notes", a.requireUser(a.createSupplierCredit))
	mux.Handle("POST /api/v1/bills/{id}/reverse", a.requireUser(a.voidBill))
	mux.Handle("POST /api/v1/bills/{id}/void", a.requireUser(a.voidBill))
	mux.Handle("POST /api/v1/bill-payments/batch", a.requireUser(a.payBillsBatch))
	mux.Handle("POST /api/v1/bill-payments/{id}/reverse", a.requireUser(a.reverseBillPayment))
	mux.Handle("POST /api/v1/expenses/{id}/reverse", a.requireUser(a.reverseExpense))
	mux.Handle("GET /api/v1/supplier-credits", a.requireUser(a.listSupplierCredits))
	mux.Handle("POST /api/v1/supplier-credits", a.requireUser(a.createStandaloneSupplierCredit))
	mux.Handle("POST /api/v1/supplier-credits/{id}/approve", a.requireUser(a.approveStandaloneSupplierCredit))
	mux.Handle("POST /api/v1/supplier-credits/{id}/refund", a.requireUser(a.refundSupplierCredit))
	mux.Handle("POST /api/v1/invoices", a.requireUser(a.createInvoice))
	mux.Handle("PUT /api/v1/invoices/{id}", a.requireUser(a.updateInvoice))
	mux.Handle("GET /api/v1/sales-overview", a.requireUser(a.salesOverview))
	mux.Handle("GET /api/v1/invoices", a.requireUser(a.listInvoices))
	mux.Handle("GET /api/v1/invoices/next-number", a.requireUser(a.nextInvoiceNumber))
	mux.Handle("GET /api/v1/invoices/{id}", a.requireUser(a.getInvoice))
	mux.Handle("POST /api/v1/invoices/{id}/post", a.requireUser(a.postInvoice))
	mux.Handle("POST /api/v1/invoices/{id}/payments", a.requireUser(a.recordPayment))
	mux.Handle("POST /api/v1/invoices/{id}/void", a.requireUser(a.voidInvoice))
	mux.Handle("GET /api/v1/document-numbering", a.requireUser(a.documentNumbering))
	mux.Handle("PUT /api/v1/document-numbering", a.requireUser(a.saveDocumentNumbering))

	mux.Handle("GET /api/v1/reports/trial-balance", a.requireUser(a.trialBalance))
	mux.Handle("GET /api/v1/reports/accountant-pack", a.requireUser(a.accountantPack))
	mux.Handle("GET /api/v1/reports/pnl", a.requireUser(a.profitAndLoss))
	mux.Handle("GET /api/v1/reports/contact-activity", a.requireUser(a.contactActivity))
	mux.Handle("GET /api/v1/reports/documents", a.requireUser(a.documentReport))
	mux.Handle("GET /api/v1/reports/document-lines", a.requireUser(a.documentLines))
	mux.Handle("GET /api/v1/reports/project-time", a.requireUser(a.projectTimeReport))
	mux.Handle("GET /api/v1/reports/cash-summary", a.requireUser(a.cashSummary))
	mux.Handle("GET /api/v1/reports/executive-summary", a.requireUser(a.executiveSummary))
	mux.Handle("GET /api/v1/reports/bank-summary", a.requireUser(a.bankSummary))
	mux.Handle("GET /api/v1/reports/cash-flows-direct", a.requireUser(a.cashFlowsDirect))
	mux.Handle("GET /api/v1/reports/movements-in-equity", a.requireUser(a.movementsInEquity))
	mux.Handle("GET /api/v1/reports/expense-claim-detail", a.requireUser(a.claimDetail))
	mux.Handle("GET /api/v1/reports/billable-expenses-outstanding", a.requireUser(a.billableOutstanding))
	mux.Handle("GET /api/v1/reports/tracking-summary", a.requireUser(a.trackingSummary))
	mux.Handle("GET /api/v1/reports/document-activity", a.requireUser(a.documentActivity))
	mux.Handle("GET /api/v1/reports/gst-audit", a.requireUser(a.gstAudit))
	mux.Handle("GET /api/v1/reports/sales-tax", a.requireUser(a.salesTax))
	mux.Handle("GET /api/v1/reports/tax-reconciliation", a.requireUser(a.gstF5Report))
	mux.Handle("GET /api/v1/reports/sales-by-item", a.requireUser(a.salesByItem))
	mux.Handle("GET /api/v1/reports/bank-reconciliation", a.requireUser(a.bankReconciliation))
	mux.Handle("GET /api/v1/reports/project-financials", a.requireUser(a.projectFinancials))
	mux.Handle("GET /api/v1/reports/contact-transactions", a.requireUser(a.contactTransactions))
	mux.Handle("GET /api/v1/reports/inventory-items", a.requireUser(a.inventoryItemList))
	mux.Handle("GET /api/v1/reports/inventory-summary", a.requireUser(a.inventoryItemSummary))
	mux.Handle("GET /api/v1/reports/inventory-details", a.requireUser(a.inventoryItemDetails))
	mux.Handle("GET /api/v1/reports/analytics", a.requireUser(a.analytics))
	mux.Handle("GET /api/v1/budgets", a.requireUser(a.listBudgets))
	mux.Handle("PUT /api/v1/budgets", a.requireUser(a.saveBudget))
	mux.Handle("GET /api/v1/reports/budget-summary", a.requireUser(a.budgetSummary))
	mux.Handle("GET /api/v1/reports/budget-variance", a.requireUser(a.budgetVariance))
	mux.Handle("GET /api/v1/reports/journals", a.requireUser(a.journalReport))
	mux.Handle("GET /api/v1/reports/cash-validation", a.requireUser(a.cashValidation))
	mux.Handle("GET /api/v1/reports/gl-exceptions", a.requireUser(a.glExceptions))
	mux.Handle("GET /api/v1/reports/fixed-asset-reconciliation", a.requireUser(a.fixedAssetReconciliation))
	mux.Handle("GET /api/v1/reports/duplicate-statement-lines", a.requireUser(a.duplicateStatementLines))
	mux.Handle("GET /api/v1/reports/fixed-assets", a.requireUser(a.fixedAssetReport))
	mux.Handle("GET /api/v1/reports/disposal-schedule", a.requireUser(a.disposalSchedule))
	// Keep Phase 3c asset registry import route alongside Phase 4 disposal schedule.
	mux.Handle("POST /api/v1/assets/import", a.requireUser(a.importAssets))
	mux.Handle("GET /api/v1/assets", a.requireUser(a.listAssets))
	mux.Handle("POST /api/v1/assets", a.requireUser(a.createAsset))
	mux.Handle("POST /api/v1/assets/depreciation/run", a.requireUser(a.runDepreciation))
	mux.Handle("POST /api/v1/assets/{id}/dispose", a.requireUser(a.disposeAsset))
	mux.Handle("GET /api/v1/reports/receivables-aging", a.requireUser(a.receivablesAging))
	mux.Handle("GET /api/v1/reports/sales-by-customer", a.requireUser(a.salesByCustomer))
	mux.Handle("GET /api/v1/reports/payables-aging", a.requireUser(a.payablesAging))
	mux.Handle("GET /api/v1/reports/balance-sheet", a.requireUser(a.balanceSheet))
	mux.Handle("GET /api/v1/reports/ledger-lines", a.requireUser(a.ledgerLines))
	mux.Handle("GET /api/v1/reports/watchlist", a.requireUser(a.watchlist))
	mux.Handle("GET /api/v1/reports/ledger-summary", a.requireUser(a.ledgerSummary))

	mux.Handle("POST /api/v1/leads/{id}/convert", a.requireUser(a.convertLead))
	mux.Handle("GET /api/v1/activities", a.requireUser(a.listActivities))
	mux.Handle("POST /api/v1/activities", a.requireUser(a.createActivity))
	mux.Handle("POST /api/v1/activities/{id}/complete", a.requireUser(a.completeActivity))
	mux.Handle("POST /api/v1/leads", a.requireUser(a.createLead))
	mux.Handle("GET /api/v1/leads", a.requireUser(a.listLeads))
	mux.Handle("GET /api/v1/documents/{kind}/{id}/pdf", a.requireUser(a.documentPDF))
	mux.Handle("POST /api/v1/invoices/{id}/credit-notes", a.requireUser(a.createCreditNote))
	mux.Handle("GET /api/v1/invoices/{id}/credit-notes", a.requireUser(a.listCreditNotes))
	mux.Handle("POST /api/v1/bank/statements", a.requireUser(a.importStatement))
	mux.Handle("GET /api/v1/bank/lines", a.requireUser(a.listStatementLines))
	mux.Handle("GET /api/v1/bank/accounts", a.requireUser(a.listBankAccounts))
	mux.Handle("POST /api/v1/bank/accounts", a.requireUser(a.createBankAccount))
	mux.Handle("PUT /api/v1/bank/accounts/{id}", a.requireUser(a.updateBankAccount))
	mux.Handle("GET /api/v1/bank/reconcile", a.requireUser(a.getBankReconciliation))
	mux.Handle("GET /api/v1/bank/reconciliations", a.requireUser(a.listBankReconciliations))
	mux.Handle("POST /api/v1/bank/reconciliations", a.requireUser(a.saveBankReconciliation))
	mux.Handle("GET /api/v1/bank/lines/{id}/suggestions", a.requireUser(a.suggestStatementLine))
	mux.Handle("POST /api/v1/bank/lines/{id}/create", a.requireUser(a.createFromStatementLine))
	mux.Handle("GET /api/v1/branding-themes", a.requireUser(a.listBrandingThemes))
	mux.Handle("POST /api/v1/branding-themes", a.requireUser(a.createBrandingTheme))
	mux.Handle("PUT /api/v1/branding-themes/{id}", a.requireUser(a.updateBrandingTheme))
	mux.Handle("DELETE /api/v1/branding-themes/{id}", a.requireUser(a.deleteBrandingTheme))
	mux.Handle("POST /api/v1/branding-themes/default", a.requireUser(a.setDefaultBrandingTheme))
	mux.Handle("GET /api/v1/statements", a.requireUser(a.customerStatement))
	mux.Handle("POST /api/v1/statements/send", a.requireUser(a.emailStatement))
	mux.Handle("GET /api/v1/statements/summary", a.requireUser(a.statementSummary))
	mux.Handle("GET /api/v1/conversion-balances", a.requireUser(a.getConversionBalances))
	mux.Handle("POST /api/v1/conversion-balances", a.requireUser(a.postConversionBalances))
	mux.Handle("GET /api/v1/staff-rates", a.requireUser(a.listStaffRates))
	mux.Handle("POST /api/v1/staff-rates", a.requireUser(a.setStaffRate))
	mux.Handle("GET /api/v1/staff-rates/{id}/history", a.requireUser(a.staffRateHistory))
	mux.Handle("GET /api/v1/bank/rules", a.requireUser(a.listBankRules))
	mux.Handle("POST /api/v1/bank/rules", a.requireUser(a.createBankRule))
	mux.Handle("POST /api/v1/bank/rules/active", a.requireUser(a.setBankRuleActive))
	mux.Handle("PUT /api/v1/bank/rules/{id}", a.requireUser(a.updateBankRule))
	mux.Handle("DELETE /api/v1/bank/rules/{id}", a.requireUser(a.deleteBankRule))
	mux.Handle("POST /api/v1/bank/lines/{id}/match", a.requireUser(a.matchStatementLine))
	mux.Handle("POST /api/v1/bank/lines/{id}/unmatch", a.requireUser(a.unmatchStatementLine))
	mux.Handle("GET /api/v1/period-close", a.requireUser(a.getPeriodClose))
	mux.Handle("GET /api/v1/audit-events", a.requireUser(a.listAuditEvents))
	mux.Handle("POST /api/v1/period-close", a.requireUser(a.closePeriod))
	mux.Handle("GET /api/v1/recurring", a.requireUser(a.listRecurring))
	mux.Handle("POST /api/v1/recurring", a.requireUser(a.createRecurring))
	mux.Handle("POST /api/v1/recurring/run", a.requireUser(a.runRecurring))
	mux.Handle("POST /api/v1/expenses/{id}/invoice", a.requireUser(a.convertExpenseToInvoice))
	mux.Handle("GET /api/v1/receipts/unlinked", a.requireUser(a.listUnlinkedReceipts))
	mux.Handle("POST /api/v1/customer-credit-notes/{id}/apply", a.requireUser(a.applyCustomerCredit))
	mux.Handle("POST /api/v1/customer-credit-notes/{id}/refund", a.requireUser(a.refundCustomerCredit))
	mux.Handle("POST /api/v1/documents/{id}/receipt-link", a.requireUser(a.linkReceipt))
	mux.Handle("GET /api/v1/recurring-expenses", a.requireUser(a.listRecurringExpenses))
	mux.Handle("GET /api/v1/recurring-expenses/{id}/history", a.requireUser(a.recurringExpenseHistory))
	mux.Handle("POST /api/v1/recurring-expenses", a.requireUser(a.createRecurringExpense))
	mux.Handle("POST /api/v1/recurring-expenses/run", a.requireUser(a.runRecurringExpenses))
	mux.Handle("POST /api/v1/recurring-expenses/active", a.requireUser(a.stopRecurringExpense))
	mux.Handle("POST /api/v1/recurring-bills", a.requireUser(a.createRecurringBill))
	mux.Handle("GET /api/v1/recurring-bills", a.requireUser(a.listRecurringBills))
	mux.Handle("POST /api/v1/recurring-bills/run", a.requireUser(a.runRecurringBills))
	mux.Handle("POST /api/v1/recurring-bills/active", a.requireUser(a.stopRecurringBill))
	mux.Handle("GET /api/v1/documents", a.requireUser(a.listDocuments))
	mux.Handle("POST /api/v1/documents", a.requireUser(a.uploadDocument))
	mux.Handle("GET /api/v1/documents/{id}", a.requireUser(a.downloadDocument))
	mux.Handle("POST /api/v1/documents/{id}/action", a.requireUser(a.documentAction))
	mux.Handle("GET /api/v1/document-folders", a.requireUser(a.documentFolders))
	mux.Handle("POST /api/v1/document-folders", a.requireUser(a.documentFolders))
	mux.Handle("DELETE /api/v1/document-folders/{id}", a.requireUser(a.deleteDocumentFolder))
	mux.Handle("GET /api/v1/billing/pricing-widgets", a.requireUser(a.pricingWidgets))
	mux.Handle("POST /api/v1/billing/pricing-widgets", a.requireUser(a.pricingWidgets))
	mux.Handle("GET /api/v1/billing/products", a.requireUser(a.billingList("billing_products", "")))
	mux.Handle("POST /api/v1/billing/products", a.requireUser(a.createBillingProduct))
	mux.Handle("GET /api/v1/billing/plans", a.requireUser(a.billingList("billing_plans", "")))
	mux.Handle("POST /api/v1/billing/plans", a.requireUser(a.createBillingPlan))
	mux.Handle("POST /api/v1/billing/plans/{id}/stage", a.requireUser(a.billingStage("billing_plans")))
	mux.Handle("GET /api/v1/billing/addons", a.requireUser(a.billingList("billing_addons", "")))
	mux.Handle("POST /api/v1/billing/addons", a.requireUser(a.createBillingAddon))
	mux.Handle("POST /api/v1/billing/addons/{id}/stage", a.requireUser(a.billingStage("billing_addons")))
	mux.Handle("GET /api/v1/billing/coupons", a.requireUser(a.billingList("billing_coupons", "")))
	mux.Handle("POST /api/v1/billing/coupons", a.requireUser(a.createBillingCoupon))
	mux.Handle("POST /api/v1/billing/coupons/{id}/stage", a.requireUser(a.billingStage("billing_coupons")))
	mux.Handle("GET /api/v1/billing/subscriptions", a.requireUser(a.billingList("billing_subscriptions", ",(SELECT c.name FROM contacts c WHERE c.org_id=x.org_id AND c.id=x.contact_id) AS customer_name")))
	mux.Handle("POST /api/v1/billing/subscriptions", a.requireUser(a.createSubscription))
	mux.Handle("POST /api/v1/billing/subscriptions/run", a.requireUser(a.runSubscriptions))
	mux.Handle("POST /api/v1/billing/subscriptions/{id}/stage", a.requireUser(a.cancelSubscription))
	mux.Handle("GET /api/v1/sales-receipts", a.requireUser(a.listSalesReceipts))
	mux.Handle("POST /api/v1/sales-receipts", a.requireUser(a.createSalesReceipt))
	mux.Handle("GET /api/v1/payments-received", a.requireUser(a.listPaymentsReceived))
	mux.Handle("POST /api/v1/payments-received", a.requireUser(a.createPaymentReceived))
	mux.Handle("GET /api/v1/payments-made", a.requireUser(a.listPaymentsMade))
	mux.Handle("POST /api/v1/payments-made", a.requireUser(a.createPaymentMade))
	mux.Handle("GET /api/v1/credit-notes", a.requireUser(a.listCustomerCredits))
	mux.Handle("POST /api/v1/credit-notes", a.requireUser(a.saveCustomerCredit))
	mux.Handle("PUT /api/v1/credit-notes/{id}", a.requireUser(a.saveCustomerCredit))
	mux.Handle("POST /api/v1/credit-notes/{id}/stage", a.requireUser(a.setCustomerCreditStage))
	mux.Handle("POST /api/v1/credit-notes/{id}/void", a.requireUser(a.voidCustomerCredit))
	mux.Handle("GET /api/v1/purchase-orders", a.requireUser(a.listPurchaseOrders))
	mux.Handle("POST /api/v1/purchase-orders", a.requireUser(a.savePurchaseOrder))
	mux.Handle("PUT /api/v1/purchase-orders/{id}", a.requireUser(a.savePurchaseOrder))
	mux.Handle("POST /api/v1/purchase-orders/{id}/stage", a.requireUser(a.setPurchaseOrderStage))
	mux.Handle("POST /api/v1/purchase-orders/{id}/bill", a.requireUser(a.billFromPurchaseOrder))
	mux.Handle("GET /api/v1/purchase-receives", a.requireUser(a.listPurchaseReceives))
	mux.Handle("POST /api/v1/purchase-receives", a.requireUser(a.createPurchaseReceive))
	mux.Handle("POST /api/v1/purchase-receives/{id}/stage", a.requireUser(a.setPurchaseReceiveStage))
	mux.Handle("GET /api/v1/mileage-preferences", a.requireUser(a.mileagePreferences))
	mux.Handle("PUT /api/v1/mileage-preferences", a.requireUser(a.mileagePreferences))
	mux.Handle("GET /api/v1/expenses", a.requireUser(a.listExpenses))
	mux.Handle("POST /api/v1/expenses", a.requireUser(a.createExpense))
	mux.Handle("GET /api/v1/quotes", a.requireUser(a.listQuotes))
	mux.Handle("POST /api/v1/quotes", a.requireUser(a.saveQuote))
	mux.Handle("PUT /api/v1/quotes/{id}", a.requireUser(a.saveQuote))
	mux.Handle("POST /api/v1/quotes/{id}/stage", a.requireUser(a.setQuoteStage))
	mux.Handle("POST /api/v1/quotes/{id}/convert", a.requireUser(a.convertQuote))
	mux.Handle("PUT /api/v1/leads/{id}", a.requireUser(a.saveLead))
	mux.Handle("POST /api/v1/leads/{id}/stage", a.requireUser(a.setLeadStage))
	mux.Handle("PUT /api/v1/deals/{id}", a.requireUser(a.saveDeal))
	mux.Handle("POST /api/v1/deals", a.requireUser(a.createDeal))
	mux.Handle("GET /api/v1/deals", a.requireUser(a.listDeals))
	mux.Handle("POST /api/v1/deals/{id}/stage", a.requireUser(a.setDealStage))

	mux.Handle("GET /api/v1/currencies", a.requireUser(a.listCurrencies))
	mux.Handle("POST /api/v1/currencies", a.requireUser(a.saveCurrency))
	mux.Handle("PUT /api/v1/currencies/{code}", a.requireUser(a.saveCurrency))
	mux.Handle("POST /api/v1/currencies/rates", a.requireUser(a.saveCurrencyRate))
	mux.Handle("POST /api/v1/fx/revalue", a.requireUser(a.revalueFX))
	mux.Handle("GET /api/v1/reports/fx-gains-losses", a.requireUser(a.fxGainsLosses))
	mux.Handle("GET /api/v1/tax/gst-returns", a.requireUser(a.listGSTReturns))
	mux.Handle("POST /api/v1/tax/gst-returns/file", a.requireUser(a.fileGSTReturn))
	mux.Handle("GET /api/v1/reports/gst-f5", a.requireUser(a.gstF5Report))
	mux.Handle("POST /api/v1/tax/gst-f5", a.requireUser(a.gstF5))
	mux.Handle("GET /api/v1/company-timezone", a.requireUser(a.companyTimezone))
	mux.Handle("PUT /api/v1/company-timezone", a.requireUser(a.companyTimezone))
	mux.Handle("GET /api/v1/tax/rates", a.requireUser(a.listTaxRates))
	mux.Handle("POST /api/v1/tax/rates", a.requireUser(a.createTaxRate))
	mux.Handle("GET /api/v1/tax/settings", a.requireUser(a.taxSettingsRoute))
	mux.Handle("PUT /api/v1/tax/settings", a.requireUser(a.taxSettingsRoute))
	mux.Handle("GET /api/v1/tax/gst-f5", a.requireUser(a.draftF5))
	mux.Handle("GET /api/v1/tax/gst-f5/snapshots", a.requireUser(a.listGSTSnapshots))
	mux.Handle("POST /api/v1/tax/gst-f5/snapshots", a.requireUser(a.saveGSTSnapshot))
	mux.Handle("POST /api/v1/tax/gst-f5/snapshots/{id}/approve", a.requireUser(a.approveGSTSnapshot))
	mux.Handle("POST /api/v1/tax/cit", a.requireUser(a.citWorksheet))
	mux.Handle("POST /api/v1/tax/pit", a.requireUser(a.pitWorksheet))
	mux.Handle("GET /api/v1/tax/years", a.requireUser(a.taxYears))
}

// ---- shared helpers ----

func parseDay(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", s)
	return t, err == nil
}

func day(t time.Time) string { return t.Format("2006-01-02") }

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return 0, false
	}
	return id, true
}

// orgFor resolves the caller's membership. write=true also requires a writer role (viewers are refused).
func (a *app) orgFor(w http.ResponseWriter, r *http.Request, userID, orgID int64, write bool) (orgAccess, bool) {
	access, status, code, err := a.orgAccessFor(r.Context(), userID, orgID)
	if err != nil {
		serverError(w, err)
		return orgAccess{}, false
	}
	if code != "" {
		writeError(w, status, code)
		return orgAccess{}, false
	}
	if write && !writerRoles[access.Role] {
		writeError(w, http.StatusForbidden, "forbidden")
		return orgAccess{}, false
	}
	return access, true
}

// accountIDs seeds the standard accounts for an organization and returns code -> id.
// ON CONFLICT keeps concurrent first writes safe.
func accountIDs(ctx context.Context, tx pgx.Tx, orgID int64) (map[string]int64, error) {
	for _, s := range seedAccounts {
		if _, err := tx.Exec(ctx,
			`INSERT INTO accounts (org_id, code, name, type, class, is_bank_account) VALUES ($1, $2, $3, $4, $5, $2='1000')
			 ON CONFLICT (org_id, code) DO NOTHING`, orgID, s.code, s.name, s.typ, defaultAccountClass(s.typ, s.code)); err != nil {
			return nil, err
		}
		var typ string
		if err := tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND code=$2`, orgID, s.code).Scan(&typ); err != nil {
			return nil, err
		}
		if typ != s.typ {
			return nil, fmt.Errorf("reserved account %s has incompatible type %s", s.code, typ)
		}
	}
	rows, err := tx.Query(ctx, `SELECT code, id FROM accounts WHERE org_id=$1`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[string]int64{}
	for rows.Next() {
		var code string
		var id int64
		if err := rows.Scan(&code, &id); err != nil {
			return nil, err
		}
		ids[code] = id
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	// A company can point customer and supplier balances at other accounts; every posting reads them through here.
	var ar, ap int64
	switch err := tx.QueryRow(ctx, `SELECT receivable_account_id,payable_account_id FROM control_accounts WHERE org_id=$1`, orgID).Scan(&ar, &ap); {
	case err == nil:
		ids[acctAR], ids[acctAP] = ar, ap
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	return ids, nil
}

// postJournal writes a balanced two-line journal: debit one account, credit another.
// The deferred trigger in 001 checks balance at commit.
func postJournal(ctx context.Context, tx pgx.Tx, orgID, userID int64, date time.Time, memo, key string,
	hash [32]byte, debitAcct, creditAcct, amount int64) (int64, error) {
	var jid int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO journals (org_id, entry_date, memo, idempotency_key, request_hash, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		orgID, date, memo, key, hash[:], userID).Scan(&jid); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO journal_lines (org_id, journal_id, line_no, account_id, debit_cents, credit_cents)
		 VALUES ($1, $2, 1, $3, $4, 0), ($1, $2, 2, $5, 0, $4)`,
		orgID, jid, debitAcct, amount, creditAcct); err != nil {
		return 0, err
	}
	return jid, nil
}

// accountAmount is one credit leg of a split journal. tracking carries the line's tracking values as
// jsonb text ("" for none) so a journal leg keeps the option the document line was tagged with.
type accountAmount struct {
	account  int64
	cents    int64
	tracking string
}

// postJournalSplit posts Dr debitAcct for the total and one credit per leg. Legs must sum to the total.
func postJournalSplit(ctx context.Context, tx pgx.Tx, orgID, userID int64, date time.Time, memo, key string,
	hash [32]byte, debitAcct, total int64, credits []accountAmount) (int64, error) {
	var jid int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO journals (org_id, entry_date, memo, idempotency_key, request_hash, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		orgID, date, memo, key, hash[:], userID).Scan(&jid); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO journal_lines (org_id, journal_id, line_no, account_id, debit_cents, credit_cents) VALUES ($1, $2, 1, $3, $4, 0)`, orgID, jid, debitAcct, total); err != nil {
		return 0, err
	}
	for i, c := range credits {
		if _, err := tx.Exec(ctx, `INSERT INTO journal_lines (org_id, journal_id, line_no, account_id, debit_cents, credit_cents) VALUES ($1, $2, $3, $4, 0, $5)`, orgID, jid, i+2, c.account, c.cents); err != nil {
			return 0, err
		}
	}
	return jid, nil
}

// postJournalLegs posts one journal with explicit debit and credit legs. Both sides must sum to the
// same total; callers own that guarantee.
func postJournalLegs(ctx context.Context, tx pgx.Tx, orgID, userID int64, date time.Time, memo, key string,
	hash [32]byte, debits, credits []accountAmount, taxDirections ...map[int64]string) (int64, error) {
	// taxDirections tags a leg as output or input tax, which is how the F5 splits the one GST account.
	direction := func(account int64) string {
		if len(taxDirections) == 0 {
			return ""
		}
		return taxDirections[0][account]
	}
	var jid int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO journals (org_id, entry_date, memo, idempotency_key, request_hash, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		orgID, date, memo, key, hash[:], userID).Scan(&jid); err != nil {
		return 0, err
	}
	line := 1
	for _, d := range debits {
		if _, err := tx.Exec(ctx, `INSERT INTO journal_lines (org_id, journal_id, line_no, account_id, debit_cents, credit_cents, tax_direction, tracking) VALUES ($1, $2, $3, $4, $5, 0, NULLIF($6, ''), $7::jsonb)`, orgID, jid, line, d.account, d.cents, direction(d.account), legTracking(d.tracking)); err != nil {
			return 0, err
		}
		line++
	}
	for _, c := range credits {
		if _, err := tx.Exec(ctx, `INSERT INTO journal_lines (org_id, journal_id, line_no, account_id, debit_cents, credit_cents, tax_direction, tracking) VALUES ($1, $2, $3, $4, 0, $5, NULLIF($6, ''), $7::jsonb)`, orgID, jid, line, c.account, c.cents, direction(c.account), legTracking(c.tracking)); err != nil {
			return 0, err
		}
		line++
	}
	return jid, nil
}

// legTracking is a journal leg's stored tracking values: the line's jsonb text, or {} for none.
func legTracking(tracking string) string {
	if tracking == "" {
		return "{}"
	}
	return tracking
}

// scaleLegs spreads a partial amount across legs in proportion to the whole, with any rounding
// residual on the first leg so the legs always sum exactly to part.
func scaleLegs(legs []accountAmount, part, whole int64) []accountAmount {
	if whole <= 0 || part == whole {
		return legs
	}
	out := make([]accountAmount, 0, len(legs))
	var sum int64
	for _, l := range legs {
		c := l.cents * part / whole
		out = append(out, accountAmount{l.account, c, l.tracking})
		sum += c
	}
	if residual := part - sum; residual != 0 {
		for i := range out {
			if out[i].cents+residual > 0 {
				out[i].cents += residual
				break
			}
		}
	}
	kept := make([]accountAmount, 0, len(out))
	for _, l := range out {
		if l.cents != 0 {
			kept = append(kept, l)
		}
	}
	return kept
}

// invoiceCredits groups net line amounts by income account and books GST Output separately. Whatever the
// discount, shipping and adjustment add or take away is booked against the first revenue leg, so the legs sum to total.
func invoiceCredits(ctx context.Context, tx pgx.Tx, orgID, invID, sales, gstOutput, total int64) ([]accountAmount, error) {
	rows, err := tx.Query(ctx, `SELECT COALESCE(revenue_account_id,$3),amount_cents,gst_cents,tracking::text FROM invoice_lines WHERE org_id=$1 AND invoice_id=$2 ORDER BY line_no`, orgID, invID, sales)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []accountAmount
	var sumNet, sumGST int64
	for rows.Next() {
		var acct, net, gst int64
		var tracking string
		if err := rows.Scan(&acct, &net, &gst, &tracking); err != nil {
			return nil, err
		}
		sumNet += net
		sumGST += gst
		// Merge by account AND tracking option: two lines on one account tagged to different options
		// must stay two journal legs, otherwise the tracking summary could not split them.
		merged := false
		for i := range out {
			if out[i].account == acct && out[i].tracking == tracking {
				out[i].cents += net
				merged = true
			}
		}
		if !merged {
			out = append(out, accountAmount{acct, net, tracking})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return []accountAmount{{sales, total, ""}}, nil
	}
	out[0].cents += total - sumNet - sumGST
	// A revenue leg can go negative when the discount exceeds that account's lines; fall back to one Sales leg.
	for _, c := range out {
		if c.cents < 0 {
			out = []accountAmount{{sales, total - sumGST, ""}}
			break
		}
	}
	// Zero legs (free lines) are dropped because a journal line must carry an amount.
	legs := make([]accountAmount, 0, len(out)+1)
	for _, c := range out {
		if c.cents > 0 {
			legs = append(legs, c)
		}
	}
	if sumGST > 0 {
		legs = append(legs, accountAmount{gstOutput, sumGST, ""})
	}
	return legs, nil
}

// ---- invoices ----

type invoiceLineIn struct {
	Description      string `json:"description"`
	Quantity         int64  `json:"quantity"`
	UnitPriceCents   int64  `json:"unit_price_cents"`
	GSTCode          string `json:"gst_code"`
	ItemID           int64  `json:"item_id"`
	RevenueAccountID int64  `json:"revenue_account_id"`
	ProjectID        int64  `json:"project_id"`
	DiscountBP       int64  `json:"discount_basis_points"`
	// Tracking is one option per tracking category for this line: {"<category_id>": <option_id>}.
	Tracking map[string]int64 `json:"tracking"`
}

type invoiceIn struct {
	OrgID           int64            `json:"org_id"`
	DocumentNumber  string           `json:"document_number"`
	ContactID       int64            `json:"contact_id"`
	InvoiceDate     string           `json:"invoice_date"`
	DueDate         string           `json:"due_date"`
	Lines           []invoiceLineIn  `json:"lines"`
	Reference       string           `json:"reference"`
	Notes           string           `json:"notes"`
	Terms           string           `json:"terms"`
	Commercial      commercialFields `json:"commercial"`
	AmountsAre      string           `json:"amounts_are"`
	Currency        string           `json:"currency"`
	FXRateMicro     int64            `json:"fx_rate_micro"`
	BrandingThemeID int64            `json:"branding_theme_id"`
}

// documentCurrency normalises the currency and rate a receivables or payables document is issued in.
// Amounts on the document stay in SGD cents; a foreign document keeps the rate so the amount it was
// struck in can be shown. Blank means SGD at a rate of 1.
func normaliseCurrency(currency string) (string, bool) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "SGD"
	}
	if len(currency) != 3 || !utf8.ValidString(currency) {
		return "", false
	}
	for _, c := range currency {
		if c < 'A' || c > 'Z' {
			return "", false
		}
	}
	return currency, true
}

func documentCurrency(currency string, rate int64) (string, int64, bool) {
	currency, valid := normaliseCurrency(currency)
	if !valid {
		return "", 0, false
	}
	if rate == 0 {
		if currency != "SGD" {
			return "", 0, false // a foreign document without a rate cannot be valued
		}
		rate = 1000000
	}
	if rate < 1000 || rate > 1000000000000 {
		return "", 0, false
	}
	if (currency == "SGD") != (rate == 1000000) {
		return "", 0, false
	}
	return currency, rate, true
}

type invoiceLineOut struct {
	LineNo           int              `json:"line_no"`
	Description      string           `json:"description"`
	Quantity         int64            `json:"quantity"`
	UnitPriceCents   int64            `json:"unit_price_cents"`
	AmountCents      int64            `json:"amount_cents"`
	GSTCode          string           `json:"gst_code"`
	GSTRateBP        int64            `json:"gst_rate_bp"`
	GSTCents         int64            `json:"gst_cents"`
	ItemID           int64            `json:"item_id"`
	RevenueAccountID int64            `json:"revenue_account_id"`
	ProjectID        int64            `json:"project_id"`
	DiscountBP       int64            `json:"discount_basis_points"`
	Tracking         map[string]int64 `json:"tracking"`
}

// amountsAreFlag normalises the invoice flag: blank means exclusive. The totals do not depend on it once posted.
func amountsAreFlag(v string) string {
	if v == "inclusive" {
		return "inclusive"
	}
	return "exclusive"
}

// SR is refused unless the company is GST-registered.
// With inclusive amounts the typed price already contains GST, so the net is backed out of it.
// Returns net and GST subtotals.
func buildSalesLines(in []invoiceLineIn, gstRegistered bool, rateBP int64, inclusive bool) ([]invoiceLineOut, int64, int64, string) {
	plain := make([]invoiceLineIn, len(in))
	isSR := make([]bool, len(in))
	for i, l := range in {
		plain[i] = l
		code := strings.ToUpper(strings.TrimSpace(l.GSTCode))
		switch code {
		case "", "NONE":
		case "SR":
			if !gstRegistered {
				return nil, 0, 0, "unsupported_gst"
			}
			isSR[i] = true
			plain[i].GSTCode = ""
		case "ZR", "ES", "OS":
			// Zero-rated, exempt and out-of-scope supplies carry no GST but must keep their F5 code (R24).
			if !gstRegistered {
				return nil, 0, 0, "unsupported_gst"
			}
			plain[i].GSTCode = ""
		default:
			return nil, 0, 0, "unsupported_gst"
		}
	}
	lines, net, code := buildInvoiceLines(plain)
	if code != "" {
		return nil, 0, 0, code
	}
	var gst int64
	for i := range lines {
		lines[i].GSTCode = "NONE"
		if c := strings.ToUpper(strings.TrimSpace(in[i].GSTCode)); c == "ZR" || c == "ES" || c == "OS" {
			lines[i].GSTCode = c
		}
		if isSR[i] {
			lines[i].GSTCode = "SR"
			lines[i].GSTRateBP = rateBP
			if inclusive {
				// The typed amount already includes GST: back the net out of it, half up.
				gross := lines[i].AmountCents
				lines[i].AmountCents = (gross*10000 + (10000+rateBP)/2) / (10000 + rateBP)
				lines[i].GSTCents = gross - lines[i].AmountCents
				net -= gross - lines[i].AmountCents
			} else {
				lines[i].GSTCents = (lines[i].AmountCents*rateBP + 5000) / 10000
			}
			gst += lines[i].GSTCents
		}
	}
	if net+gst > maxMoneyCents {
		return nil, 0, 0, "invalid_input"
	}
	return lines, net, gst, ""
}

// buildInvoiceLines validates lines and computes every amount and the total on the server.
// It returns an error code ("" when valid), so the handler and unit tests share one rule set.
func buildInvoiceLines(in []invoiceLineIn) ([]invoiceLineOut, int64, string) {
	if len(in) == 0 || len(in) > maxInvLines {
		return nil, 0, "invalid_input"
	}
	out := make([]invoiceLineOut, 0, len(in))
	var total int64
	for i, l := range in {
		desc := strings.TrimSpace(l.Description)
		if desc == "" || !utf8.ValidString(desc) || strings.ContainsRune(desc, 0) || utf8.RuneCountInString(desc) > 200 || l.Quantity < 1 || l.Quantity > maxQty ||
			l.UnitPriceCents < 0 || l.UnitPriceCents > maxMoneyCents || l.DiscountBP < 0 || l.DiscountBP > 10000 {
			return nil, 0, "invalid_input"
		}
		// Only "no GST" is supported. Anything else is refused rather than priced without tax.
		if code := strings.ToUpper(strings.TrimSpace(l.GSTCode)); code != "" && code != "NONE" {
			return nil, 0, "unsupported_gst"
		}
		if l.UnitPriceCents > maxMoneyCents/l.Quantity {
			return nil, 0, "invalid_input"
		}
		amount := (l.Quantity*l.UnitPriceCents*(10000-l.DiscountBP) + 5000) / 10000
		total += amount
		if total > maxMoneyCents {
			return nil, 0, "invalid_input"
		}
		out = append(out, invoiceLineOut{LineNo: i + 1, Description: desc, Quantity: l.Quantity,
			UnitPriceCents: l.UnitPriceCents, AmountCents: amount, ItemID: l.ItemID, RevenueAccountID: l.RevenueAccountID, ProjectID: l.ProjectID, DiscountBP: l.DiscountBP,
			Tracking: l.Tracking})
	}
	if total <= 0 {
		return nil, 0, "invalid_input"
	}
	return out, total, ""
}

// nextInvoiceNumber previews the gapless number the next posted invoice will receive.
func (a *app) nextInvoiceNumber(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, false)
	if !ok {
		return
	}
	var next int64 = 1
	if err := a.db.QueryRow(r.Context(), `SELECT invoice_next FROM core_counters WHERE org_id=$1`, access.ID).Scan(&next); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	if _, err := a.db.Exec(r.Context(), `INSERT INTO document_numbering(org_id) VALUES($1) ON CONFLICT (org_id) DO NOTHING`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	var prefix string
	var draftNext int64
	if err := a.db.QueryRow(r.Context(), `SELECT invoice_prefix, invoice_next FROM document_numbering WHERE org_id=$1`, access.ID).Scan(&prefix, &draftNext); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"number": next, "document_number": prefix + fmt.Sprintf("%05d", draftNext)})
}

func (a *app) createInvoice(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.saveInvoice(w, r, u, false)
}

func (a *app) updateInvoice(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.saveInvoice(w, r, u, true)
}

func (a *app) saveInvoice(w http.ResponseWriter, r *http.Request, u currentUser, update bool) {
	var id int64
	if update {
		var ok bool
		id, ok = pathID(w, r)
		if !ok {
			return
		}
	}
	var in invoiceIn
	if !decodeJSON(w, r, &in) {
		return
	}
	in.DocumentNumber = strings.TrimSpace(in.DocumentNumber)
	for _, field := range []struct {
		value string
		limit int
	}{{in.Reference, 100}, {in.Notes, 4000}, {in.Terms, 4000}, {in.DocumentNumber, 50}} {
		if !utf8.ValidString(field.value) || strings.ContainsRune(field.value, 0) || utf8.RuneCountInString(field.value) > field.limit {
			writeError(w, 400, "invalid_input")
			return
		}
	}
	idate, ok1 := parseDay(in.InvoiceDate)
	ddate, ok2 := parseDay(in.DueDate)
	if in.ContactID <= 0 || !ok1 || !ok2 || ddate.Before(idate) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	commercial, _ := json.Marshal(in.Commercial)
	ctx := r.Context()
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	if !a.requireFeature(w, r.Context(), access.ID, u.ID, "sales") {
		return
	}
	currency, fxRate, ok := resolveDocumentCurrency(ctx, a.db, access.ID, in.Currency, in.FXRateMicro, in.InvoiceDate)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_currency")
		return
	}
	// GST follows the document date's registration, not today's status.
	gstRegistered, err := gstRegisteredOn(ctx, a.db, access.ID, day(idate))
	if err != nil {
		serverError(w, err)
		return
	}
	if in.AmountsAre != "" && in.AmountsAre != "exclusive" && in.AmountsAre != "inclusive" {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	amountsAre := in.AmountsAre
	if update && amountsAre == "" {
		// An edit that omits the flag keeps the stored reading of the typed prices.
		if err := a.db.QueryRow(ctx, `SELECT amounts_are FROM invoices WHERE org_id=$1 AND id=$2`, access.ID, id).Scan(&amountsAre); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, 404, "not_found")
				return
			}
			serverError(w, err)
			return
		}
	}
	// A line without a tax code takes the default of the account it posts to; an explicit code wins.
	defaults, err := accountTaxDefaults(ctx, a.db, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	var contactTax, contactTreatment string
	_ = a.db.QueryRow(ctx, `SELECT sales_tax_code, sales_treatment FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, in.ContactID).Scan(&contactTax, &contactTreatment)
	if amountsAre == "" && contactTreatment != "" {
		amountsAre = contactTreatment
	}
	for i := range in.Lines {
		if strings.TrimSpace(in.Lines[i].GSTCode) == "" && contactTax != "" {
			in.Lines[i].GSTCode = contactTax
		}
		if strings.TrimSpace(in.Lines[i].GSTCode) == "" && in.Lines[i].RevenueAccountID > 0 {
			in.Lines[i].GSTCode = defaults[in.Lines[i].RevenueAccountID]
		}
	}
	// The rate comes from the dated catalogue, so a past-dated invoice prices at that date's rate.
	rateBP, err := taxRateBPOn(ctx, a.db, "SR", "sale", day(idate), func() int64 { return taxrules.GSTRateBP(idate) })
	if err != nil {
		serverError(w, err)
		return
	}
	// A contact's sales discount is the default for new lines; a discount typed on the line wins.
	if !update {
		var contactDiscount int64
		if err := a.db.QueryRow(ctx, `SELECT discount_basis_points FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, in.ContactID).Scan(&contactDiscount); err == nil && contactDiscount > 0 {
			for i := range in.Lines {
				if in.Lines[i].DiscountBP == 0 {
					in.Lines[i].DiscountBP = contactDiscount
				}
			}
		}
	}
	lines, net, gst, code := buildSalesLines(in.Lines, gstRegistered, rateBP, amountsAre == "inclusive")
	if code == "unsupported_gst" {
		writeError(w, http.StatusUnprocessableEntity, code)
		return
	}
	if code != "" {
		writeError(w, http.StatusBadRequest, code)
		return
	}
	// Discounts, shipping and adjustments are not GST-apportioned yet; refuse them on GST invoices rather than mis-tax.
	c := in.Commercial
	if gst > 0 && (c.DiscountCents != 0 || c.DiscountBasisPoints != 0 || c.ShippingCents != 0 || c.AdjustmentCents != 0) {
		writeError(w, http.StatusUnprocessableEntity, "unsupported_gst")
		return
	}
	total, valid := commercialTotal(net, in.Commercial)
	if !valid {
		writeError(w, 400, "invalid_input")
		return
	}
	total += gst
	if total > maxMoneyCents {
		writeError(w, 400, "invalid_input")
		return
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)
	// A picked branding theme must belong to this company; zero means "use the company default".
	if in.BrandingThemeID < 0 {
		writeError(w, 400, "invalid_input")
		return
	}
	if in.BrandingThemeID > 0 {
		var found bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM branding_themes WHERE org_id=$1 AND id=$2)`, access.ID, in.BrandingThemeID).Scan(&found); err != nil {
			serverError(w, err)
			return
		}
		if !found {
			writeError(w, http.StatusUnprocessableEntity, "invalid_branding_theme")
			return
		}
	}

	for _, l := range lines {
		if l.ItemID <= 0 {
			continue
		}
		var iid int64
		if err := tx.QueryRow(ctx, `SELECT id FROM items WHERE org_id=$1 AND id=$2`, access.ID, l.ItemID).Scan(&iid); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeError(w, 422, "invalid_item")
				return
			}
			serverError(w, err)
			return
		}
	}
	// A line may name its own income account; anything else falls back to Sales at posting time.
	for _, l := range lines {
		if l.RevenueAccountID <= 0 {
			continue
		}
		var typ string
		if qerr := tx.QueryRow(ctx, `SELECT type FROM accounts WHERE org_id=$1 AND id=$2`, access.ID, l.RevenueAccountID).Scan(&typ); qerr != nil {
			if errors.Is(qerr, pgx.ErrNoRows) {
				writeError(w, 422, "invalid_revenue_account")
				return
			}
			serverError(w, qerr)
			return
		}
		if typ != "revenue" {
			writeError(w, 422, "invalid_revenue_account")
			return
		}
	}
	// Tracking values are checked here, inside the transaction, so a line cannot name an archived
	// option or another company's option.
	for _, l := range lines {
		okTrack, terr := validTracking(ctx, tx, access.ID, l.Tracking)
		if terr != nil {
			serverError(w, terr)
			return
		}
		if !okTrack {
			writeError(w, 422, "invalid_tracking")
			return
		}
	}
	// A line may name a project owned by this company; the project must also belong to the invoice's customer.
	for _, l := range lines {
		if l.ProjectID <= 0 {
			continue
		}
		var owner int64
		if qerr := tx.QueryRow(ctx, `SELECT contact_id FROM projects WHERE org_id=$1 AND id=$2`, access.ID, l.ProjectID).Scan(&owner); qerr != nil {
			if errors.Is(qerr, pgx.ErrNoRows) {
				writeError(w, 422, "invalid_project")
				return
			}
			serverError(w, qerr)
			return
		}
		if owner != in.ContactID {
			writeError(w, 422, "invalid_project")
			return
		}
	}
	var kind string
	err = tx.QueryRow(ctx, `SELECT kind FROM contacts WHERE org_id=$1 AND id=$2`, access.ID, in.ContactID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && kind == "supplier") {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	status, action := http.StatusCreated, "invoice.created"
	if update {
		var state string
		err := tx.QueryRow(ctx, `SELECT status FROM invoices WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&state)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		if state != "draft" {
			writeError(w, 409, "invoice_not_draft")
			return
		}
		if _, err := tx.Exec(ctx, `DELETE FROM invoice_lines WHERE org_id=$1 AND invoice_id=$2`, access.ID, id); err != nil {
			serverError(w, err)
			return
		}
		_, err = tx.Exec(ctx, `UPDATE invoices SET contact_id=$3,invoice_date=$4,due_date=$5,total_cents=$6,reference=$7,notes=$8,terms=$9,commercial=$10,gst_cents=$11,amounts_are=$12,currency=$13,fx_rate_micro=$14,document_number=COALESCE(NULLIF($15,''),document_number),branding_theme_id=NULLIF($16::bigint,0) WHERE org_id=$1 AND id=$2`, access.ID, id, in.ContactID, idate, ddate, total, in.Reference, in.Notes, in.Terms, commercial, gst, amountsAreFlag(amountsAre), currency, fxRate, in.DocumentNumber, in.BrandingThemeID)
		if err != nil {
			if isUniqueViolation(err) {
				writeError(w, http.StatusConflict, "duplicate_number")
				return
			}
			serverError(w, err)
			return
		}
		status, action = http.StatusOK, "invoice.updated"
	} else {
		if err := tx.QueryRow(ctx, `INSERT INTO invoices(org_id,contact_id,invoice_date,due_date,total_cents,created_by,reference,notes,terms,commercial,gst_cents,amounts_are,currency,fx_rate_micro,document_number,branding_theme_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,NULLIF($15,''),NULLIF($16::bigint,0)) RETURNING id`, access.ID, in.ContactID, idate, ddate, total, u.ID, in.Reference, in.Notes, in.Terms, commercial, gst, amountsAreFlag(amountsAre), currency, fxRate, in.DocumentNumber, in.BrandingThemeID).Scan(&id); err != nil {
			if isUniqueViolation(err) {
				writeError(w, http.StatusConflict, "duplicate_number")
				return
			}
			serverError(w, err)
			return
		}
	}
	for _, l := range lines {
		if _, err := tx.Exec(ctx,
			`INSERT INTO invoice_lines (org_id, invoice_id, line_no, description, quantity, unit_price_cents, amount_cents, gst_code, gst_rate_bp, gst_cents, item_id, revenue_account_id, discount_basis_points, project_id, tracking)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11::bigint,0), NULLIF($12::bigint,0), $13, NULLIF($14::bigint,0), $15)`,
			access.ID, id, l.LineNo, l.Description, l.Quantity, l.UnitPriceCents, l.AmountCents, l.GSTCode, l.GSTRateBP, l.GSTCents, l.ItemID, l.RevenueAccountID, l.DiscountBP, l.ProjectID, trackingJSON(l.Tracking)); err != nil {
			serverError(w, err)
			return
		}
	}
	if err := recordAudit(ctx, tx, access.ID, u.ID, "invoice", id, action, nil); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, status, map[string]any{
		"id": id, "status": "draft", "contact_id": in.ContactID, "reference": in.Reference, "notes": in.Notes, "terms": in.Terms, "document_number": in.DocumentNumber,
		"invoice_date": day(idate), "due_date": day(ddate),
		"total_cents": total, "gst_cents": gst, "lines": lines, "commercial": in.Commercial,
		"currency": currency, "fx_rate_micro": fxRate,
	})
}

// ponytail: the inclusive flag explains how typed prices were read; lines and totals are stored only.
type invoiceOut struct {
	ID               int64           `json:"id"`
	Number           *int64          `json:"number"`
	DocumentNumber   string          `json:"document_number"`
	ContactID        int64           `json:"contact_id"`
	Status           string          `json:"status"`
	LifecycleStatus  string          `json:"lifecycle_status"`
	InvoiceDate      string          `json:"invoice_date"`
	DueDate          string          `json:"due_date"`
	TotalCents       int64           `json:"total_cents"`
	BalanceCents     int64           `json:"balance_cents"`
	Reference        string          `json:"reference"`
	Notes            string          `json:"notes"`
	Terms            string          `json:"terms"`
	AmountsAre       string          `json:"amounts_are"`
	SellerSnapshot   json.RawMessage `json:"seller_snapshot"`
	CustomerSnapshot json.RawMessage `json:"customer_snapshot"`
	Commercial       json.RawMessage `json:"commercial"`
	Currency         string          `json:"currency"`
	FXRateMicro      int64           `json:"fx_rate_micro"`
	ContactName      string          `json:"contact_name"`
	PaidCents        int64           `json:"paid_cents"`
	SentDate         string          `json:"sent_date"`
	BrandingThemeID  int64           `json:"branding_theme_id"`
}

const invoiceSelect = `SELECT i.id, i.number, i.document_number, i.contact_id, i.status, i.invoice_date, i.due_date, i.total_cents, i.amounts_are,
  CASE WHEN i.status = 'voided' THEN 0 ELSE
  i.total_cents - COALESCE((SELECT SUM(p.amount_cents) FROM payments p WHERE p.org_id = i.org_id AND p.invoice_id = i.id), 0)
    - COALESCE((SELECT SUM(n.amount_cents) FROM credit_notes n WHERE n.org_id = i.org_id AND n.invoice_id = i.id), 0)
    - COALESCE((SELECT SUM(c.amount_cents) FROM customer_credit_applications c WHERE c.org_id = i.org_id AND c.invoice_id = i.id), 0) END AS balance_cents,
  i.reference,i.notes,i.terms,i.seller_snapshot,i.customer_snapshot,i.commercial,i.currency,i.fx_rate_micro,
  COALESCE(NULLIF(i.customer_snapshot->>'name',''), (SELECT c.name FROM contacts c WHERE c.org_id=i.org_id AND c.id=i.contact_id), ''),
  COALESCE(to_char(i.posted_at,'YYYY-MM-DD'),''), COALESCE(i.branding_theme_id,0)
  FROM invoices i`

func scanInvoice(s scanner) (invoiceOut, error) {
	var o invoiceOut
	var idate, ddate time.Time
	if err := s.Scan(&o.ID, &o.Number, &o.DocumentNumber, &o.ContactID, &o.Status, &idate, &ddate, &o.TotalCents, &o.AmountsAre, &o.BalanceCents, &o.Reference, &o.Notes, &o.Terms, &o.SellerSnapshot, &o.CustomerSnapshot, &o.Commercial, &o.Currency, &o.FXRateMicro, &o.ContactName, &o.SentDate, &o.BrandingThemeID, &o.LifecycleStatus); err != nil {
		return o, err
	}
	o.InvoiceDate, o.DueDate = day(idate), day(ddate)
	// Paid is what payments, credit notes and applied credits have settled. A voided invoice paid nothing.
	if o.Status == "posted" {
		o.PaidCents = o.TotalCents - o.BalanceCents
	}
	return o, nil
}

func (a *app) listInvoices(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	if !validLifecycle(status) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	ctx := r.Context()
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	// Paging: limit and offset default to a single page of 50 so a company with many invoices loads
	// quickly; has_more is decided by fetching one extra row.
	limit, offset := 50, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxListRows {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		limit = n
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		offset = n
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	from, hasFrom := parseDay(r.URL.Query().Get("from"))
	to, hasTo := parseDay(r.URL.Query().Get("to"))
	within := r.URL.Query().Get("within")
	where := `($2 = '' OR x.lifecycle = $2 OR ($2 = 'posted' AND x.status = 'posted') OR ($2 IN ('draft','awaiting_approval','voided') AND x.status = $2))`
	args := []any{access.ID, status, limit + 1, offset}
	add := func(clause string, v any) {
		args = append(args, v)
		where += fmt.Sprintf(clause, len(args))
	}
	if q != "" {
		add(` AND (x.document_number ILIKE '%%'||$%d||'%%' OR CAST(x.number AS text)=$%d OR x.reference ILIKE '%%'||$%d||'%%' OR x.contact_name ILIKE '%%'||$%d||'%%')`, q)
	}
	if hasFrom {
		add(` AND x.invoice_date >= $%d`, from)
	}
	if hasTo {
		add(` AND x.invoice_date <= $%d`, to)
	}
	if within == "unsent" {
		where += ` AND x.sent_date IS NULL`
	} else if within == "voided" {
		where += ` AND x.status = 'voided'`
	} else if within != "" && within != "all" {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	rows, err := a.db.Query(ctx, `SELECT * FROM (SELECT d.*, `+lifecycleCase+` FROM (`+invoiceSelect+` WHERE i.org_id=$1) d) x
		WHERE `+where+`
		ORDER BY x.id DESC LIMIT $3 OFFSET $4`, args...)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []invoiceOut{}
	for rows.Next() {
		o, err := scanInvoice(rows)
		if err != nil {
			serverError(w, err)
			return
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	counts, err := lifecycleCounts(ctx, a.db, `SELECT d.*, `+lifecycleCase+` FROM (`+invoiceSelect+` WHERE i.org_id=$1) d`, access.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invoices": out, "limit": limit, "offset": offset, "has_more": hasMore, "counts": counts})
}

func (a *app) getInvoice(w http.ResponseWriter, r *http.Request, u currentUser) {
	invID, ok := pathID(w, r)
	if !ok {
		return
	}
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	ctx := r.Context()
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	inv, err := scanInvoice(a.db.QueryRow(ctx, `SELECT d.*, `+lifecycleCase+` FROM (`+invoiceSelect+` WHERE i.org_id=$1 AND i.id=$2) d`, access.ID, invID))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	lrows, err := a.db.Query(ctx,
		`SELECT line_no, description, quantity, unit_price_cents, amount_cents, gst_code, gst_rate_bp, gst_cents, item_id, COALESCE(revenue_account_id,0), discount_basis_points, COALESCE(project_id,0), tracking FROM invoice_lines
		 WHERE org_id=$1 AND invoice_id=$2 ORDER BY line_no`, access.ID, invID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer lrows.Close()
	lines := []invoiceLineOut{}
	for lrows.Next() {
		var l invoiceLineOut
		var itemID *int64
		var tracking json.RawMessage
		if err := lrows.Scan(&l.LineNo, &l.Description, &l.Quantity, &l.UnitPriceCents, &l.AmountCents, &l.GSTCode, &l.GSTRateBP, &l.GSTCents, &itemID, &l.RevenueAccountID, &l.DiscountBP, &l.ProjectID, &tracking); err != nil {
			serverError(w, err)
			return
		}
		l.Tracking = map[string]int64{}
		_ = json.Unmarshal(tracking, &l.Tracking)
		if itemID != nil {
			l.ItemID = *itemID
		}
		lines = append(lines, l)
	}
	if err := lrows.Err(); err != nil {
		serverError(w, err)
		return
	}
	prows, err := a.db.Query(ctx,
		`SELECT id, amount_cents, payment_date, journal_id FROM payments WHERE org_id=$1 AND invoice_id=$2 ORDER BY id`,
		access.ID, invID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer prows.Close()
	type payOut struct {
		ID          int64  `json:"id"`
		AmountCents int64  `json:"amount_cents"`
		PaymentDate string `json:"payment_date"`
		JournalID   int64  `json:"journal_id"`
	}
	payments := []payOut{}
	for prows.Next() {
		var p payOut
		var pd time.Time
		if err := prows.Scan(&p.ID, &p.AmountCents, &pd, &p.JournalID); err != nil {
			serverError(w, err)
			return
		}
		p.PaymentDate = day(pd)
		payments = append(payments, p)
	}
	if err := prows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invoice": inv, "lines": lines, "payments": payments})
}

// postInvoice turns a draft into a posted invoice: gapless number, AR/sales journal, one transaction.
// The invoice row lock serializes concurrent posts; a repeat post returns the existing number.
// Body-less action, so org_id comes from the query string.
func (a *app) postInvoice(w http.ResponseWriter, r *http.Request, u currentUser) {
	invID, ok := pathID(w, r)
	if !ok {
		return
	}
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	ctx := r.Context()
	access, ok := a.orgFor(w, r, u.ID, orgID, true)
	if !ok {
		return
	}
	if allowed, err := canPostDocuments(ctx, a.db, access.ID, u.ID); err != nil {
		serverError(w, err)
		return
	} else if !allowed {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
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
	// The importer calls the same function, so a document posted either way goes through one path.
	result, code, err := postInvoiceTx(ctx, tx, access.ID, u.ID, invID, nil, false)
	if err != nil {
		serverError(w, err)
		return
	}
	if code != "" {
		writeError(w, statusForCode(code), code)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": invID, "number": result.Number, "status": "posted", "journal_id": result.Journal})
}

// statusForCode maps a posting refusal to its HTTP status. One place, so the route and the importer
// agree on what each refusal means.
func statusForCode(code string) int {
	switch code {
	case "not_found":
		return http.StatusNotFound
	case "credit_limit_exceeded", "insufficient_stock":
		return http.StatusConflict
	case "invoice_not_posted", "bill_not_posted", "overpayment", "payment_before_invoice", "payment_before_bill",
		"credit_before_invoice", "credit_before_bill", "credit_exceeds_invoice", "credit_exceeds_bill":
		return http.StatusUnprocessableEntity
	default:
		return http.StatusBadRequest
	}
}

type paymentIn struct {
	OrgID          int64  `json:"org_id"`
	AmountCents    int64  `json:"amount_cents"`
	PaymentDate    string `json:"payment_date"`
	IdempotencyKey string `json:"idempotency_key"`
	// SettlementRateMicro is the rate the payment actually cleared at. Blank (or the document rate)
	// settles at the document rate and posts no FX difference.
	SettlementRateMicro int64 `json:"settlement_rate_micro"`
}

// recordPayment posts Dr Bank / Cr AR against a posted invoice. Same idempotency key and body returns
// the original payment; same key with a different body is a conflict. The invoice row lock stops
// two payments from both passing the balance check.
func (a *app) recordPayment(w http.ResponseWriter, r *http.Request, u currentUser) {
	invID, ok := pathID(w, r)
	if !ok {
		return
	}
	var in paymentIn
	if !decodeJSON(w, r, &in) {
		return
	}
	pdate, okDate := parseDay(in.PaymentDate)
	key := strings.TrimSpace(in.IdempotencyKey)
	if in.AmountCents <= 0 || in.AmountCents > maxMoneyCents || !okDate || key == "" || len(key) > 90 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if in.SettlementRateMicro != 0 && !validFXRate(in.SettlementRateMicro) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	ctx := r.Context()
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, true)
	if !ok {
		return
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("payment|%d|%d|%s", invID, in.AmountCents, day(pdate))))
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)

	// Serialize the key per company so two requests with the same key on different invoices
	// cannot both miss the lookup and collide on the unique index.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`,
		strconv.FormatInt(access.ID, 10)+":"+key); err != nil {
		serverError(w, err)
		return
	}

	// Match credit operations' lock order: company before invoice row.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, access.ID); err != nil {
		serverError(w, err)
		return
	}
	var status string
	var total int64
	var idate time.Time
	err = tx.QueryRow(ctx, `SELECT status, total_cents, invoice_date FROM invoices WHERE org_id=$1 AND id=$2 FOR UPDATE`,
		access.ID, invID).Scan(&status, &total, &idate)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if status != "posted" {
		writeError(w, http.StatusUnprocessableEntity, "invoice_not_posted")
		return
	}
	// Payments before the invoice date would create negative receivables in earlier as-of reports.
	// Prepayments are not supported yet.
	if pdate.Before(idate) {
		writeError(w, http.StatusUnprocessableEntity, "payment_before_invoice")
		return
	}
	var paid, credited int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT SUM(amount_cents) FROM payments WHERE org_id=$1 AND invoice_id=$2),0),
		COALESCE((SELECT SUM(amount_cents) FROM credit_notes WHERE org_id=$1 AND invoice_id=$2),0) + COALESCE((SELECT SUM(amount_cents) FROM customer_credit_applications WHERE org_id=$1 AND invoice_id=$2),0)`,
		access.ID, invID).Scan(&paid, &credited); err != nil {
		serverError(w, err)
		return
	}

	var pid, pjid int64
	var phash []byte
	err = tx.QueryRow(ctx, `SELECT id, journal_id, request_hash FROM payments WHERE org_id=$1 AND idempotency_key=$2`,
		access.ID, key).Scan(&pid, &pjid, &phash)
	if err == nil {
		if !bytes.Equal(phash, hash[:]) {
			writeError(w, http.StatusConflict, "idempotency_conflict")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"payment_id": pid, "journal_id": pjid, "invoice_id": invID,
			"balance_cents": total - paid - credited})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	// The importer calls the same function, so a receipt posted either way goes through one path.
	var code string
	pid, pjid, code, err = payInvoiceTx(ctx, tx, access.ID, u.ID, invID, in.AmountCents, pdate, key, hash, in.SettlementRateMicro, nil, false)
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "idempotency_conflict")
			return
		}
		serverError(w, err)
		return
	}
	if code != "" {
		writeError(w, statusForCode(code), code)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"payment_id": pid, "journal_id": pjid, "invoice_id": invID,
		"balance_cents": total - paid - credited - in.AmountCents})
}

// ---- ledger reports (read-only, derived from journal lines) ----

type balRow struct {
	Code, Name, Type, Class string
	Debit, Credit           int64
}

// netSides reduces gross turnover to one net balance on one side, as a trial balance shows it.
func netSides(b balRow) (debit, credit int64) {
	if n := b.Debit - b.Credit; n >= 0 {
		return n, 0
	}
	return 0, b.Credit - b.Debit
}

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func isForeignKeyViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23503"
}

// balances sums journal lines per account. from nil means no lower bound; to is inclusive.
func (a *app) balances(ctx context.Context, orgID int64, from *string, to string) ([]balRow, error) {
	rows, err := a.db.Query(ctx, `
		SELECT a.code, a.name, a.type, a.class, COALESCE(SUM(x.debit_cents), 0), COALESCE(SUM(x.credit_cents), 0)
		FROM accounts a
		LEFT JOIN (
		  SELECT jl.account_id, jl.debit_cents, jl.credit_cents
		  FROM journal_lines jl JOIN journals j ON j.org_id = jl.org_id AND j.id = jl.journal_id
		  WHERE jl.org_id = $1 AND j.entry_date <= $3::date AND ($2::date IS NULL OR j.entry_date >= $2::date)
		) x ON x.account_id = a.id
		WHERE a.org_id = $1
		GROUP BY a.id, a.code, a.name, a.type, a.class
		ORDER BY a.code`, orgID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []balRow{}
	for rows.Next() {
		var b balRow
		if err := rows.Scan(&b.Code, &b.Name, &b.Type, &b.Class, &b.Debit, &b.Credit); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (a *app) trialBalance(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	asOf := r.URL.Query().Get("as_of")
	if _, ok := parseDay(asOf); !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	if !a.requireFeature(w, r.Context(), access.ID, u.ID, "reports") {
		return
	}
	rows, err := a.balances(r.Context(), access.ID, nil, asOf)
	if err != nil {
		serverError(w, err)
		return
	}
	body := map[string]any{}
	body["rows"], body["total_debit_cents"], body["total_credit_cents"] = trialRows(rows)
	body["balanced"] = body["total_debit_cents"] == body["total_credit_cents"]
	// Compare against the same date one year earlier, as Xero's previous-period column does.
	if r.URL.Query().Get("compare") == "previous" {
		prev := prevYear(asOf)
		prevRows, err := a.balances(r.Context(), access.ID, nil, prev)
		if err != nil {
			serverError(w, err)
			return
		}
		cr, cd, cc := trialRows(prevRows)
		body["compare"] = map[string]any{"as_of": prev, "rows": cr, "total_debit_cents": cd,
			"total_credit_cents": cc, "balanced": cd == cc}
	}
	writeJSON(w, http.StatusOK, body)
}

// trialRows turns account balances into debit and credit rows plus their totals.
func trialRows(rows []balRow) (out []map[string]any, td, tc int64) {
	out = []map[string]any{}
	for _, b := range rows {
		d, c := netSides(b)
		td += d
		tc += c
		out = append(out, map[string]any{"code": b.Code, "name": b.Name, "type": b.Type,
			"debit_cents": d, "credit_cents": c})
	}
	return out, td, tc
}

// prevYear is the same calendar date one year earlier; 29 February falls back to 28 February.
func prevYear(day string) string {
	t, ok := parseDay(day)
	if !ok {
		return day
	}
	prev := time.Date(t.Year()-1, t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	if prev.Month() != t.Month() {
		prev = time.Date(t.Year()-1, t.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	}
	return prev.Format("2006-01-02")
}

func (a *app) profitAndLoss(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	f, ok1 := parseDay(from)
	t, ok2 := parseDay(to)
	if !ok1 || !ok2 || t.Before(f) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	// A P&L window that runs into the future counts only postings up to today, unless asked.
	to = reportEnd(r, to)
	if t, ok2 = parseDay(to); !ok2 || t.Before(f) {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.balances(r.Context(), access.ID, &from, to)
	if err != nil {
		serverError(w, err)
		return
	}
	body := pnlBody(rows)
	// An optional tracking category adds a column per option, built from the same journal lines, so the
	// columns always add back to the account amounts above.
	if cat := r.URL.Query().Get("tracking"); cat != "" {
		categoryID, err := strconv.ParseInt(cat, 10, 64)
		if err != nil || categoryID <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_input")
			return
		}
		tracking, found, err := a.pnlTracking(r.Context(), access.ID, from, to, categoryID)
		if err != nil {
			serverError(w, err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		body["tracking"] = tracking
	}
	// Compare against the same window one year earlier, matching the balance sheet and trial balance.
	if r.URL.Query().Get("compare") == "previous" {
		prevFrom, prevTo := prevYear(from), prevYear(to)
		prevRows, err := a.balances(r.Context(), access.ID, &prevFrom, prevTo)
		if err != nil {
			serverError(w, err)
			return
		}
		prev := pnlBody(prevRows)
		prev["from"], prev["to"] = prevFrom, prevTo
		body["compare"] = prev
	}
	writeJSON(w, http.StatusOK, body)
}

func ptrDay(t time.Time) *string { d := day(t); return &d }

func (a *app) balanceSheet(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	asOf := r.URL.Query().Get("as_of")
	if _, ok := parseDay(asOf); !ok {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.balances(r.Context(), access.ID, nil, asOf)
	if err != nil {
		serverError(w, err)
		return
	}
	body := balanceSheetBody(rows)
	if r.URL.Query().Get("compare") == "previous" {
		prev := prevYear(asOf)
		prevRows, err := a.balances(r.Context(), access.ID, nil, prev)
		if err != nil {
			serverError(w, err)
			return
		}
		cmp := balanceSheetBody(prevRows)
		cmp["as_of"] = prev
		body["compare"] = cmp
	}
	writeJSON(w, http.StatusOK, body)
}

// ---- leads and deals ----

type leadIn struct {
	OrgID  int64  `json:"org_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Source string `json:"source"`
	leadProfile
}

func (a *app) createLead(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.saveLead(w, r, u)
}

func (a *app) saveLead(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in leadIn
	if !decodeJSON(w, r, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	email := strings.ToLower(strings.TrimSpace(in.Email))
	source := strings.TrimSpace(in.Source)
	emailOK := email == ""
	if !emailOK {
		_, emailOK = normalizeEmail(email)
	}
	if name == "" || !validTaxText(name, maxNameLen) || !emailOK || !validTaxText(source, 100) || !validLead(&in) {
		writeError(w, http.StatusBadRequest, "invalid_input")
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
	var id int64
	stage := "new"
	var contactID *int64
	status, action := http.StatusCreated, "lead.created"
	if r.Method == http.MethodPut {
		var ok bool
		id, ok = pathID(w, r)
		if !ok {
			return
		}
		err = tx.QueryRow(ctx, `SELECT stage,contact_id FROM leads WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&stage, &contactID)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "not_found")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		_, err = tx.Exec(ctx, `UPDATE leads SET name=$3,email=$4,source=$5,company_name=$6,phone=$7,job_title=$8,website=$9,notes=$10,updated_at=now() WHERE org_id=$1 AND id=$2`, access.ID, id, name, email, source, in.CompanyName, in.Phone, in.JobTitle, in.Website, in.Notes)
		status, action = http.StatusOK, "lead.updated"
	} else {
		err = tx.QueryRow(ctx, `INSERT INTO leads (org_id,name,email,source,created_by,company_name,phone,job_title,website,notes) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`, access.ID, name, email, source, u.ID, in.CompanyName, in.Phone, in.JobTitle, in.Website, in.Notes).Scan(&id)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,$3,jsonb_build_object('lead_id',$4::bigint))`, access.ID, u.ID, action, id); err != nil {
		serverError(w, err)
		return
	}
	if err = tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	in.OrgID, in.Name, in.Email, in.Source = access.ID, name, email, source
	writeJSON(w, status, struct {
		ID        int64  `json:"id"`
		Stage     string `json:"stage"`
		ContactID *int64 `json:"contact_id"`
		leadIn
	}{id, stage, contactID, in})
}

func (a *app) listLeads(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	stage := r.URL.Query().Get("stage")
	if stage != "" && !validStage(stage, "new", "contacted", "qualified", "lost") {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(),
		`SELECT id, name, email, source, stage,company_name,phone,job_title,website,notes,contact_id FROM leads WHERE org_id=$1 AND ($2 = '' OR stage = $2)
		 ORDER BY id DESC LIMIT $3`, access.ID, stage, maxListRows)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type lead struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		Email     string `json:"email"`
		Source    string `json:"source"`
		Stage     string `json:"stage"`
		ContactID *int64 `json:"contact_id"`
		leadProfile
	}
	out := []lead{}
	for rows.Next() {
		var l lead
		if err := rows.Scan(&l.ID, &l.Name, &l.Email, &l.Source, &l.Stage, &l.CompanyName, &l.Phone, &l.JobTitle, &l.Website, &l.Notes, &l.ContactID); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"leads": out})
}

type stageIn struct {
	OrgID int64  `json:"org_id"`
	Stage string `json:"stage"`
}

func validStage(s string, allowed ...string) bool {
	for _, a := range allowed {
		if s == a {
			return true
		}
	}
	return false
}

func (a *app) setLeadStage(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in stageIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validStage(in.Stage, "new", "contacted", "qualified", "lost") {
		writeError(w, http.StatusBadRequest, "invalid_input")
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
	var cur string
	var linked *int64
	err = tx.QueryRow(ctx, `SELECT stage,contact_id FROM leads WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&cur, &linked)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if linked != nil {
		writeError(w, 409, "lead_already_converted")
		return
	}
	if !leadNext[cur][in.Stage] {
		writeError(w, http.StatusUnprocessableEntity, "invalid_transition")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE leads SET stage=$3,updated_at=now() WHERE org_id=$1 AND id=$2`, access.ID, id, in.Stage); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'lead.stage_changed',jsonb_build_object('lead_id',$3::bigint,'stage',$4::text))`, access.ID, u.ID, id, in.Stage); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "stage": in.Stage})
}

type dealIn struct {
	OrgID       int64  `json:"org_id"`
	LeadID      int64  `json:"lead_id"`
	Title       string `json:"title"`
	AmountCents int64  `json:"amount_cents"`
	dealProfile
}

func (a *app) createDeal(w http.ResponseWriter, r *http.Request, u currentUser) {
	a.saveDeal(w, r, u)
}

func (a *app) listDeals(w http.ResponseWriter, r *http.Request, u currentUser) {
	orgID, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	stage := r.URL.Query().Get("stage")
	if stage != "" && !validStage(stage, "open", "won", "lost") {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, orgID, false)
	if !ok {
		return
	}
	rows, err := a.db.Query(r.Context(),
		`SELECT id,lead_id,title,amount_cents,stage,contact_id,COALESCE(expected_close_date::text,''),probability,deal_type,source,next_step,notes FROM deals WHERE org_id=$1 AND ($2 = '' OR stage = $2)
		 ORDER BY id DESC LIMIT $3`, access.ID, stage, maxListRows)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type deal struct {
		ID          int64  `json:"id"`
		LeadID      *int64 `json:"lead_id"`
		Title       string `json:"title"`
		AmountCents int64  `json:"amount_cents"`
		Stage       string `json:"stage"`
		dealProfile
	}
	out := []deal{}
	for rows.Next() {
		var d deal
		if err := rows.Scan(&d.ID, &d.LeadID, &d.Title, &d.AmountCents, &d.Stage, &d.ContactID, &d.ExpectedCloseDate, &d.Probability, &d.DealType, &d.Source, &d.NextStep, &d.Notes); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deals": out})
}

func (a *app) setDealStage(w http.ResponseWriter, r *http.Request, u currentUser) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in stageIn
	if !decodeJSON(w, r, &in) {
		return
	}
	if !validStage(in.Stage, "open", "won", "lost") {
		writeError(w, http.StatusBadRequest, "invalid_input")
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
	var cur string
	err = tx.QueryRow(ctx, `SELECT stage FROM deals WHERE org_id=$1 AND id=$2 FOR UPDATE`, access.ID, id).Scan(&cur)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if !dealNext[cur][in.Stage] {
		writeError(w, http.StatusUnprocessableEntity, "invalid_transition")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE deals SET stage=$3,updated_at=now() WHERE org_id=$1 AND id=$2`, access.ID, id, in.Stage); err != nil {
		serverError(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(org_id,user_id,action,detail) VALUES($1,$2,'deal.stage_changed',jsonb_build_object('deal_id',$3::bigint,'stage',$4::text))`, access.ID, u.ID, id, in.Stage); err != nil {
		serverError(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "stage": in.Stage})
}

// ---- tax worksheets (deterministic breakdown, not a filed return) ----

type gstLineIn struct {
	Kind        string      `json:"kind"`
	Code        string      `json:"code"`
	Description string      `json:"description"`
	NetCents    money.Cents `json:"net_cents"`
	GSTCents    money.Cents `json:"gst_cents"`
}

type gstIn struct {
	OrgID        int64       `json:"org_id"`
	PeriodStart  string      `json:"period_start"`
	PeriodEnd    string      `json:"period_end"`
	RevenueCents money.Cents `json:"revenue_cents"`
	Lines        []gstLineIn `json:"lines"`
	// Conditional F5 fields (boxes 10-17). Each is accepted as entered and echoed back in the boxes.
	TouristRefundClaim           bool        `json:"tourist_refund_claim"`
	BadDebtOrRCRefund            bool        `json:"bad_debt_or_rc_refund"`
	PreRegistrationClaim         bool        `json:"pre_registration_claim"`
	ImportedServicesRC           bool        `json:"imported_services_rc"`
	TouristRefundAmount          money.Cents `json:"tourist_refund_amount_cents"`
	BadDebtOrRCRefundAmount      money.Cents `json:"bad_debt_or_rc_refund_amount_cents"`
	PreRegistrationPurchaseValue money.Cents `json:"pre_registration_purchase_value_cents"`
	PreRegistrationClaimAmount   money.Cents `json:"pre_registration_claim_amount_cents"`
	MarketplaceRemote            bool        `json:"marketplace_remote"`
	MarketplaceRemoteAmount      money.Cents `json:"marketplace_remote_amount_cents"`
	RedelivererLVG               bool        `json:"redeliverer_lvg"`
	RedelivererLVGAmount         money.Cents `json:"redeliverer_lvg_amount_cents"`
	SuppliedLVG                  bool        `json:"supplied_lvg"`
	SuppliedLVGAmount            money.Cents `json:"supplied_lvg_amount_cents"`
	// IGDS boxes 18-21 need a separate working paper; Tidal refuses rather than accepts a figure it cannot compute.
	IGDSApplicable bool `json:"igds_applicable"`
}

func (a *app) gstF5(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in gstIn
	if !decodeJSON(w, r, &in) {
		return
	}
	start, ok1 := parseDay(in.PeriodStart)
	end, ok2 := parseDay(in.PeriodEnd)
	if !ok1 || !ok2 || end.Before(start) || in.RevenueCents < 0 || len(in.Lines) == 0 || len(in.Lines) > 5000 {
		writeError(w, http.StatusBadRequest, "invalid_input")
		return
	}
	if !gstLinesBounded(in.RevenueCents, in.Lines) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_input")
		return
	}
	if !gstPeriodSupported(start, end) {
		writeError(w, http.StatusUnprocessableEntity, "unsupported_period")
		return
	}
	access, ok := a.orgFor(w, r, u.ID, in.OrgID, false)
	if !ok {
		return
	}
	// A return for a period the company was registered in stays available after deregistration.
	registered, err := gstRegisteredInPeriod(r.Context(), a.db, access.ID, in.PeriodStart, in.PeriodEnd)
	if err != nil {
		serverError(w, err)
		return
	}
	// A business not registered for GST has no F5 to file; refuse rather than print zeros as a return.
	if !registered {
		writeError(w, http.StatusUnprocessableEntity, "gst_not_registered")
		return
	}
	lines := make([]taxrules.GSTLine, 0, len(in.Lines))
	for _, l := range in.Lines {
		lines = append(lines, taxrules.GSTLine{Kind: l.Kind, Code: l.Code, Description: l.Description, Net: l.NetCents, GST: l.GSTCents})
	}
	if in.IGDSApplicable {
		writeError(w, http.StatusUnprocessableEntity, "unsupported_igds")
		return
	}
	res, err := taxrules.ComputeGSTF5(taxrules.GSTInput{
		PeriodStart: start, PeriodEnd: end, Lines: lines, Revenue: in.RevenueCents,
		TouristRefundClaim: in.TouristRefundClaim, BadDebtOrRCRefund: in.BadDebtOrRCRefund,
		PreRegistrationClaim: in.PreRegistrationClaim, ImportedServicesRC: in.ImportedServicesRC,
		TouristRefundAmount: in.TouristRefundAmount, BadDebtOrRCRefundAmount: in.BadDebtOrRCRefundAmount,
		PreRegistrationPurchaseValue: in.PreRegistrationPurchaseValue, PreRegistrationClaimAmount: in.PreRegistrationClaimAmount,
		MarketplaceRemote: in.MarketplaceRemote, MarketplaceRemoteAmount: in.MarketplaceRemoteAmount,
		RedelivererLVG: in.RedelivererLVG, RedelivererLVGAmount: in.RedelivererLVGAmount,
		SuppliedLVG: in.SuppliedLVG, SuppliedLVGAmount: in.SuppliedLVGAmount,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_input")
		return
	}
	boxes := []map[string]any{}
	for _, b := range res.Boxes {
		boxes = append(boxes, map[string]any{"box": b.Box, "label": b.Label, "value_cents": b.Value})
	}
	writeJSON(w, http.StatusOK, map[string]any{"worksheet_only": true, "rate_bp": res.RateBP, "boxes": boxes,
		"net_gst_cents": res.NetGST, "due_date": day(res.DueDate), "warnings": res.Warnings})
}

// Tax input ceilings. One S$10bn in cents bounds each amount, and the sum of absolute values of every
// amount in one request is bounded too. Then rate multiplications (at most 2400 bp) and sums stay far
// inside int64, and MinInt64 is refused before any abs() call.
const (
	maxTaxAbsTotal = 1000000000000
	maxTaxItems    = 200
)

type taxBudget struct{ total int64 }

// add returns false when any value is out of range or the running absolute total exceeds the ceiling.
func (b *taxBudget) add(vs ...money.Cents) bool {
	for _, v := range vs {
		if v < -maxTaxAbsTotal || v > maxTaxAbsTotal {
			return false
		}
		b.total += int64(money.Abs(v))
		if b.total > maxTaxAbsTotal {
			return false
		}
	}
	return true
}

func validTaxText(value string, limit int) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0) && utf8.RuneCountInString(value) <= limit
}

func (b *taxBudget) addItems(items []taxrules.Item) bool {
	if len(items) > maxTaxItems {
		return false
	}
	for _, it := range items {
		if !validTaxText(it.Label, 200) || !validTaxText(it.Reason, 1000) || !validTaxText(it.Ref, 200) || !b.add(it.Amount) {
			return false
		}
	}
	return true
}

func citInputBounded(in taxrules.CITInput) bool {
	var b taxBudget
	return b.add(in.Revenue, in.NetProfitBeforeTax, in.RRDeduction, in.EISDeductions, in.FurtherDeductions,
		in.BalancingCharge, in.UnutilisedCABF, in.CurrentYearCA, in.BalancingAllowance, in.UnutilisedLossesBF,
		in.NetRentalIncome, in.InterestIncome, in.OtherTaxableIncome, in.UnutilisedDonationsBF,
		in.IPCDonations, in.OverseasDonations) &&
		b.addItems(in.SeparateSourceIncome) && b.addItems(in.NonTaxableIncome) && b.addItems(in.NonDeductible)
}

func pitInputBounded(in taxrules.PITInput) bool {
	for _, issue := range in.CapitalAllowanceIssues {
		if !validTaxText(issue, 1000) {
			return false
		}
	}
	var b taxBudget
	return b.add(in.Revenue, in.GrossProfit, in.AllowableExpenses, in.PartnershipShare, in.CapitalAllowances,
		in.BalancingCharge, in.LossesBF, in.CapitalAllowancesBF, in.InterestExcluded, in.EmploymentIncome,
		in.NetRentalIncome, in.OtherIncome, in.IPCDonations) &&
		b.addItems(in.Reliefs) && len(in.CapitalAllowanceIssues) <= maxTaxItems
}

func gstLinesBounded(revenue money.Cents, lines []gstLineIn) bool {
	var b taxBudget
	if !b.add(revenue) {
		return false
	}
	for _, l := range lines {
		if !b.add(l.NetCents, l.GSTCents) {
			return false
		}
	}
	return true
}

// gstPeriodSupported rejects periods longer than a year and periods spanning a GST rate change,
// because one rate cannot be applied honestly to the whole return.
func gstPeriodSupported(start, end time.Time) bool {
	return end.Sub(start) <= 366*24*time.Hour && taxrules.GSTRateBP(start) == taxrules.GSTRateBP(end)
}

func (a *app) citWorksheet(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in taxrules.CITInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if !citInputBounded(in) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_input")
		return
	}
	if _, err := taxrules.CITFor(in.YA); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "unsupported_year")
		return
	}
	res, err := taxrules.ComputeCIT(in)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_input")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"worksheet_only": true, "result": res})
}

func (a *app) pitWorksheet(w http.ResponseWriter, r *http.Request, u currentUser) {
	var in taxrules.PITInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if !pitInputBounded(in) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_input")
		return
	}
	if _, err := taxrules.PITFor(in.YA); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "unsupported_year")
		return
	}
	res, err := taxrules.ComputePIT(in)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_input")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"worksheet_only": true, "result": res})
}

func (a *app) taxYears(w http.ResponseWriter, r *http.Request, u currentUser) {
	pit := []int{}
	for y := 2000; y <= 2100; y++ {
		if _, err := taxrules.PITFor(y); err == nil {
			pit = append(pit, y)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"cit": taxrules.SupportedCITYears(), "pit": pit})
}
