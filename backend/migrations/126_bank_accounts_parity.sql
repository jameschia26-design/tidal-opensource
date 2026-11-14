-- Phase 3a banking parity: a bank account record, per-account statement dedupe, saved reconciliations
-- and richer bank rules. Everything is additive; existing statement lines and rules keep working.

-- A bank account is still a chart account (so every posting rule and report is unchanged) that now
-- carries the fields the bank account screen shows.
ALTER TABLE accounts
  ADD COLUMN bank_account_number text NOT NULL DEFAULT '' CHECK (length(bank_account_number) <= 50),
  ADD COLUMN bank_account_type text NOT NULL DEFAULT 'bank' CHECK (bank_account_type IN ('bank', 'credit_card', 'paypal', 'other')),
  ADD COLUMN bank_opening_balance_cents bigint NOT NULL DEFAULT 0 CHECK (bank_opening_balance_cents BETWEEN -100000000000000 AND 100000000000000),
  ADD COLUMN bank_opening_balance_date date,
  ADD COLUMN bank_statement_balance_cents bigint NOT NULL DEFAULT 0 CHECK (bank_statement_balance_cents BETWEEN -100000000000000 AND 100000000000000),
  ADD COLUMN bank_currency text NOT NULL DEFAULT 'SGD' CHECK (bank_currency = 'SGD'),
  ADD COLUMN archived boolean NOT NULL DEFAULT false;
UPDATE accounts SET bank_opening_balance_date = date '2000-01-01'
  WHERE is_bank_account AND bank_opening_balance_date IS NULL;

-- Rules gain a transfer direction, an any/all condition mode and the Xero "then" actions. A rule
-- still only suggests; nothing posts by itself.
ALTER TABLE bank_rules DROP CONSTRAINT bank_rules_direction_check;
ALTER TABLE bank_rules ADD CONSTRAINT bank_rules_direction_check
  CHECK (direction IN ('receive', 'spend', 'transfer'));
ALTER TABLE bank_rules
  ADD COLUMN condition_match text NOT NULL DEFAULT 'all' CHECK (condition_match IN ('all', 'any')),
  ADD COLUMN contact_id bigint,
  ADD COLUMN tax_rate_code text CHECK (tax_rate_code ~ '^[A-Z0-9_]{1,20}$'),
  ADD COLUMN tracking jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(tracking) = 'object'),
  ADD CONSTRAINT bank_rules_contact_fk FOREIGN KEY (org_id, contact_id) REFERENCES contacts (org_id, id);

-- A saved reconciliation proves the statement and the ledger agreed at a point in time. The zero
-- difference is a database rule too, so no code path can save a reconciliation that does not balance.
CREATE TABLE bank_reconciliations (
  id                      bigserial PRIMARY KEY,
  org_id                  bigint NOT NULL REFERENCES organizations(id),
  bank_account_id         bigint NOT NULL,
  period_from             date NOT NULL,
  period_to               date NOT NULL,
  opening_balance_cents   bigint NOT NULL,
  statement_closing_cents bigint NOT NULL,
  ledger_balance_cents    bigint NOT NULL,
  difference_cents        bigint NOT NULL CHECK (difference_cents = 0),
  lines_total             int NOT NULL CHECK (lines_total >= 0),
  created_by              bigint NOT NULL REFERENCES users(id),
  created_at              timestamptz NOT NULL DEFAULT now(),
  CHECK (period_to >= period_from),
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, bank_account_id) REFERENCES accounts (org_id, id)
);
CREATE INDEX bank_reconciliations_account ON bank_reconciliations (org_id, bank_account_id, period_to DESC);

-- Statements are deduplicated per account by date, amount and reference. The old key also hashed the
-- description and was unique per company, so the same transfer imported on two statements collided.
-- Merge historical duplicates onto the lowest id first (each duplicate is the same money line), then
-- re-key and tighten the constraint.
ALTER TABLE bank_statement_lines DISABLE TRIGGER bank_lines_append_only;
DO $$
DECLARE
  r    record;
  keep bigint;
BEGIN
  FOR r IN
    SELECT org_id, COALESCE(bank_account_id, 0) AS acct, line_date, amount_cents, reference
    FROM bank_statement_lines
    GROUP BY 1, 2, 3, 4, 5
    HAVING count(*) > 1
  LOOP
    SELECT min(id) INTO keep FROM bank_statement_lines
     WHERE org_id = r.org_id AND COALESCE(bank_account_id, 0) = r.acct
       AND line_date = r.line_date AND amount_cents = r.amount_cents AND reference = r.reference;
    -- The kept line may already hold the same document match; drop the copy before moving the rest.
    DELETE FROM bank_line_matches m
      USING bank_statement_lines d, bank_line_matches k
     WHERE d.org_id = r.org_id AND COALESCE(d.bank_account_id, 0) = r.acct
       AND d.line_date = r.line_date AND d.amount_cents = r.amount_cents AND d.reference = r.reference
       AND d.id <> keep AND m.line_id = d.id
       AND k.org_id = m.org_id AND k.line_id = keep
       AND k.doc_kind = m.doc_kind AND k.doc_id = m.doc_id;
    UPDATE bank_line_matches m SET line_id = keep
      FROM bank_statement_lines d
     WHERE d.org_id = r.org_id AND COALESCE(d.bank_account_id, 0) = r.acct
       AND d.line_date = r.line_date AND d.amount_cents = r.amount_cents AND d.reference = r.reference
       AND d.id <> keep AND m.line_id = d.id;
    DELETE FROM bank_statement_lines d
     WHERE d.org_id = r.org_id AND COALESCE(d.bank_account_id, 0) = r.acct
       AND d.line_date = r.line_date AND d.amount_cents = r.amount_cents AND d.reference = r.reference
       AND d.id <> keep;
  END LOOP;
END $$;
UPDATE bank_statement_lines
   SET import_key = encode(sha256(convert_to(
     COALESCE(bank_account_id, 0)::text || '|' || line_date::text || '|' ||
     amount_cents::text || '|' || reference, 'UTF8')), 'hex');
ALTER TABLE bank_statement_lines DROP CONSTRAINT bank_statement_lines_org_id_import_key_key;
ALTER TABLE bank_statement_lines ADD CONSTRAINT bank_statement_lines_account_import_key
  UNIQUE (org_id, bank_account_id, import_key);
ALTER TABLE bank_statement_lines ENABLE TRIGGER bank_lines_append_only;

-- Spending or receiving straight from a statement line posts a plain journal. It needs its own match
-- kind so the single-match columns accept it alongside invoices, bills, expenses, receipts and transfers.
ALTER TABLE bank_line_matches DROP CONSTRAINT bank_line_matches_doc_kind_check;
ALTER TABLE bank_line_matches ADD CONSTRAINT bank_line_matches_doc_kind_check
  CHECK (doc_kind IN ('invoice', 'bill', 'expense', 'receipt', 'transfer', 'journal'));
ALTER TABLE bank_statement_lines DROP CONSTRAINT bank_statement_lines_matched_payment_kind_check;
ALTER TABLE bank_statement_lines ADD CONSTRAINT bank_statement_lines_matched_payment_kind_check
  CHECK (matched_payment_kind IN ('invoice', 'bill', 'expense', 'receipt', 'transfer', 'journal'));
