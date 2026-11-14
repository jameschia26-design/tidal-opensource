-- Xero Add Account options: show the account on the dashboard watchlist,
-- offer it in expense claims and allow payments against it.
ALTER TABLE accounts
  ADD COLUMN show_on_dashboard boolean NOT NULL DEFAULT false,
  ADD COLUMN show_in_expense_claims boolean NOT NULL DEFAULT false,
  ADD COLUMN enable_payments boolean NOT NULL DEFAULT false;
