# Migration fixtures

Provenance, because a fixture that pretends to be a vendor export is worse than none.

## Verbatim vendor files

From Intuit's official **IIF Import Kit**
(`https://support.quickbooks.intuit.com/support/pages/executable/iif/iif_import_kit.zip`).
Tab-delimited, CRLF, `!`-prefixed column declarations. Bytes untouched; the smaller
per-record files are the relevant rows copied out of the kit's list-declarations file
with their `!` declaration lines, nothing else.

- `qb-lists.iif` — `ACCNT` and `INVITEM` list declarations plus rows.
- `qb-customers.iif`, `qb-vendors.iif`, `qb-items.iif` — the `CUST`, `VEND` and `INVITEM`
  rows with their declarations.
- `qb-bill.iif` — `Bill Related (AP)/2004 Bill.iif`.
- `qb-invoice.iif` — `Invoice (AR)/Invoice (Sales Tax Preference=off).iif`.
- `qb-invoice-tax.iif` — `Invoice (AR)/2007 Invoice (With Sales Tax Preference=on).iif`.
  Its sales-tax line is deliberately rejected on import (see `migrate.test.mjs`).

These are the only byte-exact vendor exports here.

## Built to a vendor's published field order

Header names and their order come from the vendor's own documentation. Row values are ours.

- `peachtree-*.csv` — Sage 50 US 2024 help, *Import/Export Fields* pages
  (`help-sage50.na.sage.com/.../Import_Export_Fields_{Chart_of_Accounts,Customer_List,Vendor_List,Inventory_Item_List,Sales_Journal,Purchase_Journal}.htm`).
  Sage's default export file names are `CHART.CSV`, `CUSTOMER.CSV`, `VENDOR.CSV`.
- `myob-*.txt` — MYOB's import/export field tables
  (`help.myob.com.au/prem19/win/impexptables.htm`). Tab-delimited, as MYOB writes. Cards,
  accounts, items, and the Sales/Purchases - Service journal layouts.

## Documented spelling, not yet confirmed against a tenant export

- `xero-contacts.csv`, `xero-invoices.csv`, `xero-accounts.csv`, `xero-items.csv` — Xero's
  import header spelling. Xero's help centre is login-gated, so the spelling is taken from
  Xero's documentation and from this app's own contact importer, which already speaks it.
  A real Xero export is the last piece of proof.
- `sage50-coa.csv`, `sage50-customer.csv` — Sage 50 UK/AU style headers (Account Number,
  Account Name, Header, Balance, Account Type), cross-checked against
  `paulhayler/woocommerce-sage50`.

Do not cite this folder as proof of a vendor's exact export layout. Where real files were
unobtainable the README says so; keep it that way.
