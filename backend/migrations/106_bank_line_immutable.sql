-- Restore the imported-line immutability that 105 replaced: unmatching is allowed, but the date, amount and
-- description of an imported line never change.
CREATE OR REPLACE FUNCTION bank_line_match_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.matched_payment_id IS NOT NULL THEN
    IF NEW.matched_payment_id IS NULL THEN
      IF NEW.line_date IS DISTINCT FROM OLD.line_date OR NEW.amount_cents IS DISTINCT FROM OLD.amount_cents OR NEW.description IS DISTINCT FROM OLD.description THEN
        RAISE EXCEPTION 'imported bank lines are immutable';
      END IF;
      RETURN NEW;
    END IF;
    IF NEW.matched_payment_id IS DISTINCT FROM OLD.matched_payment_id OR NEW.matched_payment_kind IS DISTINCT FROM OLD.matched_payment_kind THEN
      RAISE EXCEPTION 'bank line match is final';
    END IF;
  END IF;
  IF NEW.line_date IS DISTINCT FROM OLD.line_date OR NEW.amount_cents IS DISTINCT FROM OLD.amount_cents OR NEW.description IS DISTINCT FROM OLD.description THEN
    RAISE EXCEPTION 'imported bank lines are immutable';
  END IF;
  RETURN NEW;
END $$;
