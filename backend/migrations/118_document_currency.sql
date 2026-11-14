-- Foreign currency on receivables and payables documents.
-- Books stay in SGD cents; a document records the currency of the deal and the rate used, so the
-- foreign amount can be shown alongside. Rate is millionths: 1 unit of currency = fx_rate_micro/1e6 SGD.
CREATE FUNCTION currency_rate_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.currency = 'SGD' AND NEW.fx_rate_micro <> 1000000 THEN
    RAISE EXCEPTION 'SGD documents use a rate of 1' USING ERRCODE='check_violation';
  END IF;
  IF NEW.currency <> 'SGD' AND NEW.fx_rate_micro = 1000000 THEN
    RAISE EXCEPTION 'a foreign document needs its own rate' USING ERRCODE='check_violation';
  END IF;
  RETURN NEW;
END $$;

ALTER TABLE invoices
  ADD COLUMN currency text NOT NULL DEFAULT 'SGD' CHECK (currency ~ '^[A-Z]{3}$'),
  ADD COLUMN fx_rate_micro bigint NOT NULL DEFAULT 1000000 CHECK (fx_rate_micro > 0);
ALTER TABLE bills
  ADD COLUMN currency text NOT NULL DEFAULT 'SGD' CHECK (currency ~ '^[A-Z]{3}$'),
  ADD COLUMN fx_rate_micro bigint NOT NULL DEFAULT 1000000 CHECK (fx_rate_micro > 0);

CREATE TRIGGER invoices_currency_guard BEFORE INSERT OR UPDATE ON invoices FOR EACH ROW EXECUTE FUNCTION currency_rate_guard();
CREATE TRIGGER bills_currency_guard BEFORE INSERT OR UPDATE ON bills FOR EACH ROW EXECUTE FUNCTION currency_rate_guard();
