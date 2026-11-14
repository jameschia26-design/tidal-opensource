-- R16 matches expenses, receipts and transfers too, so the legacy single-match columns accept every kind.
ALTER TABLE bank_statement_lines DROP CONSTRAINT bank_statement_lines_matched_payment_kind_check;
ALTER TABLE bank_statement_lines ADD CONSTRAINT bank_statement_lines_matched_payment_kind_check
  CHECK (matched_payment_kind IN ('invoice','bill','expense','receipt','transfer'));
