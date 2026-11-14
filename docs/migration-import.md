# Moving into Tidal from another accounting system

Upload an export on **Contacts → Move from another system** (`/migrate`). The file is
identified, converted to Tidal's own column set, shown for review, then imported. Nothing
posts before you press Import, and a rejected row says why.

## Deterministic imports

Imports use explicit, deterministic mappings; unknown columns are reviewed instead of guessed.

## What the converter reads

| System | Format read | Notes |
| --- | --- | --- |
| QuickBooks Desktop | IIF (tab-delimited, `!ACCNT` / `!CUST` / `!VEND` / `!INVITEM` / `!TRNS` / `!SPL`) | Real vendor samples in `frontend/tests/fixtures/migrate/` from Intuit's IIF Import Kit. `SPL` rows are line detail of their `TRNS`, not separate documents. |
| QuickBooks Online | CSV / XLSX-as-CSV lists | Column names follow Intuit's published import fields. |
| Xero | CSV | Xero's spelling (`ContactName`, `EmailAddress`, `BillingLine1`) is already this app's native contact format. |
| MYOB | Tab- or comma-delimited `.txt` | Card, account and item tables; `Co./Last Name` + `Card ID` identify a card. |
| Sage / Peachtree (Sage 50 US) | CSV | Numeric `Account Type` codes (0–24) map to asset/liability/equity/revenue/expense. |

Dates are read the way the source system writes them: month-first for QuickBooks,
QuickBooks Desktop and Sage 50 US, day-first for the others. A first number above 12 is
always treated as a day regardless. Amounts accept `1,234.50`, `$1,234.50` and `(45.00)`
for negative. QuickBooks Desktop signs transaction amounts by accounting direction, so a
negative bill amount becomes a positive bill.

## What converts, and what does not

Converts: chart of accounts, customers, suppliers, items, invoices, bills.

Deliberately not converted, with the reason:

- **Opening balances.** Account exports carry balance columns (`OBAMOUNT`, `Current Balance`), and the `/migrate` review screen offers to post them as one conversion journal (`/v1/conversion-balances`) dated the day Tidal goes live. Any difference between total debits and credits books to Opening Balance Equity so the books balance. This posts once per company; doing it through the journal keeps the trail intact instead of stuffing opening amounts into individual records.
- **Tax codes.** No foreign GST/VAT code is mapped to a Tidal tax code. A Singapore GST
  code chosen by guessing from `SR`/`GST`/`N-T` would silently misstate a return. Codes
  are reported in the "columns this import ignores" list so you can see them.
- **Multi-currency.** SGD only; a foreign-currency line is refused by the importer rather
  than converted at an invented rate.
- **Payments, journals, credit notes, stock quantities.** Not yet in the migration path.

## Coverage: what is proven, and with what file

Each cell is a fixture in `frontend/tests/fixtures/migrate/` that converts with zero
rejected rows. **V** = byte-exact vendor file; **D** = built to the vendor's published
field order (headers official, values ours); — = not covered.

| | Accounts | Customers | Suppliers | Items | Invoices | Bills |
| --- | --- | --- | --- | --- | --- | --- |
| QuickBooks Desktop (IIF) | **V** | **V** | **V** | **V** | **V** | **V** |
| Sage 50 US (Peachtree) | D | D | D | D | D | D |
| MYOB | D | D | D | D | D | D |
| Xero | D | D | D* | D | D | D* |
| Sage 50 UK/AU | D | D | — | — | — | — |

\* Xero names the party `ContactName` and the date `InvoiceDate` on a sales document and on a
bill alike, so the same export becomes either one depending on what you tell the importer it
is; the review screen has that selector. Sage 50 UK/AU contact and account lists are covered,
but its item and journal layouts differ from Sage 50 US and have not been verified here, so
they are left out rather than guessed.

Balance columns are read from the same account files (QuickBooks `OBAMOUNT`, Sage and
MYOB `Balance` / `Current Balance`) and post as one conversion journal.

Not covered at all, and deliberately: payments, manual journals, credit notes, stock
quantities, multi-currency, and any foreign tax code mapping.

## Fixture honesty

`frontend/tests/fixtures/migrate/` holds Intuit's official IIF Import Kit files,
verbatim: list declarations plus separate bill, invoice, vendor, customer and item
files. The Sage 50 US fixtures use the exact field order from Sage's 2024
import/export field pages (field names are official, row values are ours). MYOB fixtures
follow MYOB's published import/export field tables (headers official, values ours).
Xero fixtures use Xero's documented header spellings, which match this app's own
contact importer that already speaks them; a Xero tenant export would be the final proof.
The folder README records which is which; do not cite it as proof of a vendor's exact
export layout.

## Cutover checklist: what comes across, what does not

Opening customer and supplier balances can now arrive as real dated documents
(`POST /api/v1/opening-documents`, one per open invoice or bill). They post to
Opening Balance Equity (3900), never revenue or expenses, so receivables and
payables aging agree with the 1100 and 2100 control accounts from day one.
A batch of opening AR invoices totalling 5,000 therefore leaves the AR control
account at 5,000 and the aging total at 5,000.

Excluded from an opening-balance migration, and to be entered by hand if needed:

- Payments, credit notes and refunds already applied to open documents. Import the
  net open amount per document instead of the original gross amount.
- Stock quantities and cost layers. Enter opening stock as a stock adjustment.
- Fixed assets, accumulated depreciation and their schedules. Enter each asset,
  then post its accumulated depreciation as a conversion journal line.
- GST on opening documents. Opening balances are tax-neutral by design; any GST
  already filed with IRAS stays in the prior system's records.
- Bank statement lines and their matches.
- Users, roles and audit history of the old system.
- Custom report layouts, emailed templates and branding files.
- Multi-currency balances and revaluation history.

## Company export and its limits

`GET /api/v1/export/company?org_id=` writes every org-scoped table to JSON,
including journals and their lines, documents, allocations, matches, stock
movements and audit events.

It is an archive, not a migration back into Tidal:

- Attachment bytes are not included, only their metadata rows.
- User logins, sessions and secrets are never exported.
- There is no import endpoint for this file. Restoring it requires a database
  restore, not an API re-import; trial-balance totals match because the ledger
  rows themselves are exported.
