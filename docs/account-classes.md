# Account classes (Xero chart categories)

Xero's chart offers 16 selectable account types. Tidal keeps its five broad types
(`asset`, `liability`, `equity`, `revenue`, `expense`) as the posting and reporting group — every
existing report, posting rule and control account reads `accounts.type` and is unchanged — and adds
`accounts.class` for the finer Xero category.

Migration: `backend/migrations/120_account_class.sql` (numbered after `119_invitation_revocation.sql`).

## Schema

`accounts.class text NOT NULL` with exactly these values:

| class | group (`accounts.type`) |
|---|---|
| `bank` | asset |
| `current_asset` | asset |
| `fixed_asset` | asset |
| `inventory` | asset |
| `prepayment` | asset |
| `non_current_asset` | asset |
| `current_liability` | liability |
| `non_current_liability` | liability |
| `equity` | equity |
| `retained_earnings` | equity |
| `sales` | revenue |
| `revenue` | revenue |
| `other_income` | revenue |
| `cost_of_sales` | expense |
| `expense` | expense |
| `other_expense` | expense |

Two constraints enforce the pair: `accounts_class_check` restricts the value to the list above, and
`accounts_class_group_check` requires the class to sit inside the row's broad type. A class whose group
disagrees with `type` cannot be stored, so the columns cannot drift apart.

## Backfill rules (existing rows)

Migration 120 fills `class` from data already on the row, then sets `NOT NULL`:

| type | condition | class |
|---|---|---|
| revenue | code starts `4` (Tidal's sales range, e.g. `4000`) | `sales` |
| revenue | anything else | `revenue` |
| expense | code starts `5` (direct-cost range, e.g. `5100`) | `cost_of_sales` |
| expense | anything else | `expense` |
| asset | code starts `28` | `bank` |
| asset | code starts `15` or `17` | `fixed_asset` |
| asset | code starts `24` | `inventory` |
| asset | anything else | `current_asset` |
| liability | always | `current_liability` |
| equity | name contains "retained" or "accumulated profit" | `retained_earnings` |
| equity | anything else (e.g. `3900` Opening Balance Equity) | `equity` |

The code prefixes are the Tidal chart convention (`1000` Bank, `1500` Fixed assets, `2400` Inventory,
`4000` Sales, `5100` Cost of Sales). The name check is only used for equity, where no code range marks
retained earnings. `prepayment` and `non_current_asset` are selectable but never produced by the
backfill; an accountant picks them explicitly.

Known limitation of the documented rules: the seeded `1000` Bank account does not start with `28`, so it
backfills (and defaults) to `current_asset`. Choose `Bank` on that account to match Xero's chart. This is
deliberate for this phase; a bank-account rule belongs with the Phase 3 bank account object.

## Defaults for new accounts and imports

`defaultAccountClass(type, code)` applies the same table, without the equity name check
(new equity accounts default to `equity`). It exists in:

- `backend/accounting.go` — used by account create/validation, `core.go` seed accounts,
  `fixed_assets.go` `ensureAccount`, and `ptb_commit.go` `ensurePTBAccounts`.
- `frontend/lib/migrate.ts` — sets `class` on every converted chart row; `frontend/lib/imports.ts`
  forwards it in the account import body.
- `frontend/lib/account-classes.ts` — re-exports it for the chart screen and holds Xero's labels and
  chart tabs.

## API

`POST /api/v1/accounts` accepts optional `class`. Omitted → derived from `type` and `code`. Present →
must be one of the 16 values inside the type's group, otherwise `400 invalid_input`. `GET
/api/v1/accounts` returns `class` per account.

There is no account update endpoint in this release, so only create carries the field. When an update
endpoint is added it should call the same `validAccount` check.

## UI

`frontend/app/(app)/accounting/page.tsx`: the new-account form has a class selector filtered to the
chosen broad type (so a mismatch cannot be submitted), the table shows the class label, and the chart
has Xero's tabs — All, Assets, Liabilities, Equity, Expenses, Revenue.

## Out of scope for this phase

P&L and balance-sheet groupings (Phase 1b), tax defaults (1c), document statuses (1d), tracking (1e),
and report column changes. `type` remains the only input to report maths until Phase 1b.
