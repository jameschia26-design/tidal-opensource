-- R15/R16 need an audited unmatch. The original guard made a match permanent; it now allows clearing the
-- match (the handler audits it) but still refuses moving a line to a different payment.
CREATE OR REPLACE FUNCTION bank_line_match_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.matched_payment_id IS NOT NULL THEN
    IF NEW.matched_payment_id IS NULL THEN
      RETURN NEW;
    END IF;
    IF NEW.matched_payment_id IS DISTINCT FROM OLD.matched_payment_id OR NEW.matched_payment_kind IS DISTINCT FROM OLD.matched_payment_kind THEN
      RAISE EXCEPTION 'bank line match is final';
    END IF;
  END IF;
  RETURN NEW;
END $$;
