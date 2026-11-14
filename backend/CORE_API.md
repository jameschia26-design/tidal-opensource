# Tidal core API (invoices, payments, reports, CRM, tax worksheets)

Status: core routes implemented in `core.go`, with accounting and CRM helpers. As of 2026-10-29, backend checks including disposable-PostgreSQL integration tests and race detection pass locally. Frontend type checks, tests and production build pass. Signed-in browser acceptance and production rollout remain unverified. Unavailable fallback remains for banking. Not approved for real books.

Conventions follow API.md: paths under `/api/v1`, JSON bodies (unknown fields rejected), errors `{"error": "<code>"}`, money as integer cents, session cookie, `Origin` required on writes.

`org_id`: numeric, in query or body as listed. Optional when the caller belongs to exactly one company, else `400 org_required`. Non-members get `403 forbidden`.

Roles: any member reads. Writes need `owner`, `admin`, `accountant` or `member`. `viewer` gets `403 forbidden`.

## Invoices

Only SGD, no GST. Each line's tax code must be omitted or `"NONE"`; anything else is `422 unsupported_gst`. A GST-registered company cannot create invoices at all yet (`422 unsupported_gst`). Totals are computed server-side; a client `total_cents` field is rejected (`400 invalid_json`).

| Method | Path | Body / query | Success | Errors |
| --- | --- | --- | --- | --- |
| POST | `/api/v1/invoices` | `{"org_id?","contact_id","invoice_date":"YYYY-MM-DD","due_date":"YYYY-MM-DD","lines":[{"description","quantity","unit_price_cents","gst_code?"}]}` | `201 {"id","status":"draft","contact_id","invoice_date","due_date","total_cents","lines":[{"line_no","description","quantity","unit_price_cents","amount_cents"}]}` | `400 invalid_input`; `422 unsupported_gst`; `403 forbidden` |
| GET | `/api/v1/invoices?org_id=&status=draft\|posted` | optional `status` | `200 {"invoices":[{"id","number","contact_id","status","invoice_date","due_date","total_cents","balance_cents"}]}` (max 200, newest first) | `400 invalid_input`; `403 forbidden` |
| GET | `/api/v1/invoices/{id}?org_id=` | | `200 {"invoice":{...as above},"lines":[...],"payments":[{"id","amount_cents","payment_date","journal_id"}]}` | `404 not_found`; `403 forbidden` |
| POST | `/api/v1/invoices/{id}/post?org_id=` | no body | `200 {"id","number","status":"posted","journal_id"}`. Repeat post returns the same result. | `404 not_found`; `403 forbidden` |
| POST | `/api/v1/invoices/{id}/payments` | `{"org_id?","amount_cents","payment_date","idempotency_key"}` (key 1-90 chars) | `201 {"payment_id","journal_id","invoice_id","balance_cents"}`. Same key and body returns `200` with the original payment. | `400 invalid_input`; `404 not_found`; `409 idempotency_conflict` (key reused with different body, or concurrent duplicate); `422 invoice_not_posted`; `422 payment_before_invoice` (payment date earlier than invoice date; prepayments unsupported); `422 overpayment`; `403 forbidden` |

`POST /api/v1/invoices` and `PUT /api/v1/invoices/{id}` also accept `reference` (max 100 characters), `notes` and `terms` (max 4000 each). PUT fully replaces a draft, including its lines, and returns 200; posted edits return `409 invoice_not_draft`. List/detail responses include these fields plus `seller_snapshot` and `customer_snapshot`. Posting freezes seller identity/contact/address and customer identity/contact/billing/shipping details. Snapshots are empty for drafts and older invoices posted before this migration; no historical identity is invented. Later profile edits do not change saved snapshots. Draft creation is not idempotent: after an uncertain response, check the list before retrying.

Rules: a posted invoice is immutable (amounts, dates, contact, lines, notes, terms and snapshots). Draft edits replace all lines atomically. Database checks reject mismatched totals, empty invoices, moved lines and any mutation or insertion of posted lines. Numbers are gapless per company and assigned at posting. A draft is not in the ledger. Posting debits Accounts Receivable and credits Sales. A payment debits Bank and credits Accounts Receivable. No reversal endpoint in this release.

Invoice status: `draft` | `posted`. Payment state is shown by `balance_cents`; there is no separate `paid` status.

## Quotes

`GET /api/v1/quotes?org_id=` lists latest 200 quotes, including line details, frozen party details and linked invoice ID. All members read; viewers cannot write.

- `POST /api/v1/recurring`: also accepts `amounts_are` (`exclusive` default, or `inclusive`) and per-line `gst_code` (`NONE` default, or `SR`). `SR` needs a GST-registered company (`422 unsupported_gst`). Item, account and project links on lines are refused (`400 invalid_input`). The template stores typed amounts only; each run in `POST /api/v1/recurring/run` computes GST at the rate for that run date. Templates saved before GST support run as before with no GST.
- `POST /api/v1/quotes`: `org_id`, `contact_id`, `quote_date`, `expiry_date`, `reference` (100), `notes`/`terms` (4000 each), `lines` using invoice line fields. Returns 201 with ID/status/total. SGD, whole quantities and no GST only; GST-registered companies refused. Same-company customer/both contact required. Expiry cannot precede quote date. Server computes totals; database validates JSON line fields and totals.
- `PUT /api/v1/quotes/{id}`: same full replacement body. Draft only; sent edits return `409 quote_not_draft`.
- `POST /api/v1/quotes/{id}/stage`: `org_id`, `stage`. Draft to sent; sent to accepted/rejected. Same-status retry succeeds without another audit event. Marking sent freezes seller/customer identities and locks contents; no email is sent. Accepted/rejected are final.
- `POST /api/v1/quotes/{id}/convert`: `org_id`, `invoice_date`, `due_date`. Accepted quotes only; invoice date cannot precede quote date, due date cannot precede invoice date. Creates one draft with quoted customer, reference, notes, terms and exact line amounts in one transaction. Concurrent calls and retries return the same invoice ID (201 first, 200 repeat). Draft can be reviewed/edited before posting; posting freezes then-current invoice parties separately from quote history. Quote conversion itself never posts money.

Quote records cannot be deleted. Changes are audited. No quote emailing, printable document, GST, revisions, public customer-acceptance link or custom numbering yet. After an uncertain draft-create response, inspect the quote list before retrying.

## Supplier bills

Supplier bills support GST codes blank/NONE/SR/ZR/ES/OS for GST-registered companies. Whole-number quantities, SGD cents, latest 200 rows. Supplier must belong to the company and have supplier/both role. Untracked service/goods items resolve their purchase expense account; tracked inventory and fixed-asset bill posting remain unsupported. Supplier credit adjustments/refunds are unsupported; GST reports include customer credit notes only. Supplier bill SR coding represents the supplier's tax invoice classification and calculates tax, but should be claimed in Box 5/7 only when IRAS input-tax conditions are met. The software does not yet capture claimability or blocked-tax adjustments.

- `GET /api/v1/bills?org_id=&status=`: list drafts/posted bills with supplier name, reference, dates, total, balance and frozen supplier snapshot.
- `POST /api/v1/bills`: `org_id`, `contact_id`, required `supplier_reference` (trimmed, 1–100 characters, unique per supplier), `bill_date`, `due_date`, optional `notes` (4000), `lines` (1–200, each `description`, `quantity`, `unit_price_cents`, optional `account_id` for non-item lines or `item_id` for catalog items, optional `gst_code` blank/NONE/SR/ZR/ES/OS). Item uses its configured purchase expense account and preserves item link; tracked goods, explicit item/account combinations, and non-expense purchase accounts are refused. SR calculates GST at bill date rate; zero-rated, exempt and out-of-scope codes carry zero GST. Returns 201 with ID/status/total.
- `PUT /api/v1/bills/{id}`: same body, full draft replacement. Returns 200; posted bills return `409 bill_not_draft`. Duplicate supplier reference returns `409 bill_reference_exists`. Unique reference also prevents a lost create response from producing another bill with the same reference.
- `GET /api/v1/bills/{id}?org_id=`: `bill`, `lines` including expense account code/name, `payments` including date/reference/journal ID.
- `POST /api/v1/bills/{id}/post?org_id=`: no body. Locks draft, debits selected expense accounts, debits claimable supplier GST to 2310 Input GST, credits 2100 Accounts Payable for gross total, freezes supplier identity/contact/address and records audit event atomically. Returns 200 with same journal ID on retry. Posted header/lines cannot change.
- `POST /api/v1/bills/{id}/payments`: `org_id`, `amount_cents`, `payment_date`, optional `reference` (100), `idempotency_key` (1–90). New payment returns 201, identical retry 200, changed retry 409. Debits Accounts Payable, credits 1000 Bank. Rejects dates before bill, unposted bills and amounts above balance. Concurrent payments cannot overpay. Records payment only; never initiates bank transfer.

All members read; viewers cannot create/edit/post/pay. Common errors: `invalid_supplier`, `invalid_expense_account`, `unsupported_bill_gst`, `bill_not_posted`, `payment_before_bill`, `overpayment`. Built-in account type conflicts fail closed rather than posting to an incorrect account. Posted bills and payments currently have no reversal endpoint.

## Ledger reports (read-only)

Derived from posted journals. Standard accounts are created per company on first write: `1000` Bank (asset), `1100` Accounts Receivable (asset), `2100` Accounts Payable (liability), `4000` Sales (revenue). Other accounts are not seeded in this release.

| Method | Path | Query | Success |
| --- | --- | --- | --- |
| GET | `/api/v1/reports/trial-balance` | `org_id?`, `as_of=YYYY-MM-DD` | `200 {"rows":[{"code","name","type","debit_cents","credit_cents"}],"total_debit_cents","total_credit_cents","balanced"}` |
| GET | `/api/v1/reports/pnl` | `org_id?`, `from`, `to` | `200 {"revenue_cents","expense_cents","profit_cents","lines":[{"code","name","amount_cents"}]}` |
| GET | `/api/v1/reports/balance-sheet` | `org_id?`, `as_of` | `200 {"assets_cents","liabilities_cents","equity_cents","lines":[...],"balanced"}` (equity includes current-period profit) |

Each account shows one net balance on one side (debit or credit), not gross turnover. `balanced` is true when total debits equal total credits.

Errors: `400 invalid_input`, `403 forbidden`.

## CRM

Leads: stages `new` -> `contacted` -> `qualified` -> `lost` (`lost` is terminal; no backward moves).
Deals: stages `open` -> `won` | `lost` (both terminal).

| Method | Path | Body / query | Success | Errors |
| --- | --- | --- | --- | --- |
| POST | `/api/v1/leads` | `{"org_id?","name","email?","source?"}` | `201 {"id","name","email","source","stage":"new"}` | `400 invalid_input`; `403 forbidden` |
| GET | `/api/v1/leads?org_id=&stage=` | optional `stage` | `200 {"leads":[{"id","name","email","source","stage"}]}` | `400 invalid_input`; `403 forbidden` |
| POST | `/api/v1/leads/{id}/stage` | `{"org_id?","stage"}` | `200 {"id","stage"}` | `400 invalid_input`; `404 not_found`; `422 invalid_transition`; `403 forbidden` |
| POST | `/api/v1/deals` | `{"org_id?","lead_id?","title","amount_cents"}` | `201 {"id","lead_id","title","amount_cents","stage":"open"}` | `400 invalid_input`; `403 forbidden` |
| GET | `/api/v1/deals?org_id=&stage=` | optional `stage` | `200 {"deals":[{"id","lead_id","title","amount_cents","stage"}]}` | `400 invalid_input`; `403 forbidden` |
| POST | `/api/v1/deals/{id}/stage` | `{"org_id?","stage"}` | `200 {"id","stage"}` | `400 invalid_input`; `404 not_found`; `422 invalid_transition`; `403 forbidden` |

Leads and deals are company-scoped. `lead_id` must belong to the same company.

`PUT /api/v1/leads/{id}` fully replaces editable identity/contact fields: name, email, source, company_name, phone, job_title, website and notes. Stage and linked customer are retained; customer details are edited separately. Create/edit and stage changes are audited. Converted leads cannot change stage (`409 lead_already_converted`).

Deal create/list and `PUT /api/v1/deals/{id}` expose optional `contact_id` (positive customer/both ID or null), `expected_close_date` (YYYY-MM-DD or empty), `probability` (integer 0–100, default 0), `deal_type` and `source` (100 characters each), `next_step` (1000), and `notes` (4000). Title max 200; amount non-negative and bounded to 100000000000000 cents. Customer and lead must belong to the company; when the lead already links a customer, a different customer is refused. PUT fully replaces an open deal; closed edits return `409 deal_closed`. Existing terminal stage rules remain. Changes and closures are audited; winning never posts income or creates an invoice. Frontend groups relationship, forecast and follow-up fields and requires confirmation before final closure. Record selectors list latest 200 contacts/leads; linked older IDs remain visible by ID.

## Tax worksheets

Frontend now exposes itemized company adjustments (separate-source, non-taxable and non-deductible), personal relief codes/amounts with reasons/references, and capital-allowance review notes. Negative amounts are accepted only in supported loss/deficit fields. Item text is bounded at the backend boundary: label 200, reason 1000, reference 200 characters; review-note lines 1000 each. Worksheets are not saved or linked to company books yet. Existing tax computation rules were not revised by this form increment.

Deterministic breakdowns only. Input ceiling: each amount and the sum of all absolute amounts in one request is bounded to 1,000,000,000,000 cents (S$10bn), which keeps rate multiplication and sums inside int64. Every response has `"worksheet_only": true`: not a filed return, not certified, not ready for IRAS submission. CIT and PIT bodies wrap the taxrules result as `{"worksheet_only":true,"result":{...}}`.

Supported years: `GET /api/v1/tax/years` returns `{"cit":[2020..2027],"pit":[2023..2027]}`.

| Method | Path | Body | Success | Errors |
| --- | --- | --- | --- | --- |
| POST | `/api/v1/tax/gst-f5` | `{"org_id?","period_start","period_end","revenue_cents","lines":[{"kind":"sale"\|"purchase","code","net_cents","gst_cents","description?"}]}` | `200 {"rate_bp","boxes":[{"box","label","value_cents","yes_no?"}],"net_gst_cents","due_date","warnings":[]}` | `400 invalid_input`; `422 invalid_input` (rule violation); `422 gst_not_registered`; `403 forbidden` |
| POST | `/api/v1/tax/cit` | Camel-case JSON as in `internal/taxrules/cit.go`, e.g. `{"ya","revenue","netProfitBeforeTax","separateSourceIncome":[{"label","amount"}],"nonTaxableIncome","nonDeductible","rrDeduction","eisDeductions","furtherDeductions","balancingCharge","unutilisedCaBf","currentYearCa","balancingAllowance","unutilisedLossesBf","netRentalIncome","interestIncome","otherTaxableIncome","unutilisedDonationsBf","ipcDonations","overseasDonations","startUpExemption","localEmployee","onlyIncomeAt17","claimsCarryBack","claimsGroupRelief","claimsInvestmentAllowance","claimsForeignTaxCredit"}`. Amounts in cents. Max 200 items per list; aggregate absolute amount <= 1,000,000,000,000 cents (S$10bn) | `200 {"worksheet_only":true,"result":{...}}` | `422 unsupported_year`; `422 invalid_input` (out of bounds); `400 invalid_json` |
| POST | `/api/v1/tax/pit` | Camel-case JSON as in `internal/taxrules/pit.go`: `ya`, `age`, `resident`, `foreigner`, `partner`, `revenue`, `grossProfit`, `allowableExpenses`, `partnershipShare`, `capitalAllowances`, `balancingCharge`, `capitalAllowanceIssues`, `lossesBf`, `capitalAllowancesBf`, `interestExcluded`, `employmentIncome`, `netRentalIncome`, `otherIncome`, `ipcDonations`, `reliefs:[{label,amount}]`. Same ceilings as CIT | `200 {"worksheet_only":true,"result":{...}}` | `422 unsupported_year`; `422 invalid_input` (out of bounds); `400 invalid_json` |

GST F5: company must have `gst_registered=true`. Period must be at most one year and must not span a GST rate change (`422 unsupported_period`). Lines: `{"kind","code","description?","net_cents","gst_cents"}`, at most 5000; aggregate absolute value <= 1,000,000,000,000 cents. Codes as in `internal/taxrules/gst.go`. Effective-dated statutory rates are stored in `gst_rates`; bill SR calculations select applicable rate by bill date. F5 remains a manually supplied worksheet, not ledger-driven return. Purchase reports expose posted transaction basis and GST; F5 Box 5 excludes purchases whose input tax is not claimable, so report totals are not automatically filing totals.

## Not in this release

Credit notes, recurring billing, document reversal endpoint, GST/stock/asset bills, banking, stock, multi-currency, GST on invoices (including for GST-registered companies), prepayments, payment allocation across invoices, ledger-driven tax input, and any `paid` invoice status.
- `POST /api/v1/expenses/{id}/invoice`: body `{"org_id","gst_code?"}`. Makes a draft invoice from a billable expense. `gst_code` is `NONE` (default) or `SR`; `SR` needs a GST-registered company (`422 unsupported_gst`, as does any other code). The expense amount is the price before GST; 9%/8%/7% by expense date is added on top. A refusal creates nothing and leaves the expense unlinked. Other failures: `422 expense_not_billable`, `409 expense_already_invoiced`, `404 not_found`.
