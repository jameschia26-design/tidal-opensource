-- Asset purchases link to the register (D4) and disposals are recorded (R10).
-- A bill line posted to an asset account creates its register entry; lines below the company threshold are
-- marked low value so they are not depreciated. Disposal keeps the cost, proceeds and gain or loss.
ALTER TABLE organizations ADD COLUMN capitalisation_threshold_cents bigint NOT NULL DEFAULT 100000
  CHECK (capitalisation_threshold_cents >= 0);

ALTER TABLE fixed_assets
  ADD COLUMN cost_account_id bigint,
  ADD COLUMN acquisition_journal_id bigint,
  ADD COLUMN low_value boolean NOT NULL DEFAULT false,
  ADD COLUMN disposed_on date,
  ADD COLUMN proceeds_cents bigint CHECK (proceeds_cents >= 0),
  ADD COLUMN gain_loss_cents bigint,
  ADD COLUMN disposal_journal_id bigint;
