-- Optional contra-expense account for separately presenting line discounts.
ALTER TABLE bills ADD COLUMN discount_account_id bigint;
ALTER TABLE bills ADD FOREIGN KEY (org_id, discount_account_id) REFERENCES accounts(org_id, id);
CREATE FUNCTION bill_discount_posting_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status='posted' AND NEW.discount_account_id IS DISTINCT FROM OLD.discount_account_id THEN
    RAISE EXCEPTION 'posting cannot change discount account' USING ERRCODE='insufficient_privilege';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER bill_discount_guard BEFORE UPDATE ON bills FOR EACH ROW EXECUTE FUNCTION bill_discount_posting_guard();
