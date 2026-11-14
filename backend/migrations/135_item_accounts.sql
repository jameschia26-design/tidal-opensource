-- Xero item details carry the inventory asset and cost-of-sales accounts, and an active/archived state.
ALTER TABLE items
  ADD COLUMN inventory_asset_account_id bigint,
  ADD COLUMN cogs_account_id bigint,
  ADD COLUMN archived boolean NOT NULL DEFAULT false;
