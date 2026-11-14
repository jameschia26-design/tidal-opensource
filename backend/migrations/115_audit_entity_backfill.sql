-- R26 follow-up: every existing audit insert already carries the record id in its detail JSON, but not in
-- the entity columns. Fill them here at insert time, so history can filter by record without editing every
-- call site. Explicit entity columns win when the handler sets them.
CREATE FUNCTION audit_entity_from_detail() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  key text;
  value bigint;
BEGIN
  IF NEW.entity_kind IS NOT NULL AND NEW.entity_id IS NOT NULL THEN
    RETURN NEW;
  END IF;
  FOREACH key IN ARRAY ARRAY[
    'invoice_id', 'bill_id', 'expense_id', 'credit_note_id', 'customer_credit_note_id',
    'supplier_credit_id', 'payment_id', 'purchase_order_id', 'purchase_receive_id',
    'quote_id', 'document_id', 'asset_id', 'adjustment_id', 'journal_id'
  ] LOOP
    IF NEW.detail ? key THEN
      value := NULLIF(NEW.detail->>key, '')::bigint;
      IF value IS NOT NULL THEN
        NEW.entity_kind := COALESCE(NEW.entity_kind, replace(key, '_id', ''));
        NEW.entity_id := COALESCE(NEW.entity_id, value);
        RETURN NEW;
      END IF;
    END IF;
  END LOOP;
  RETURN NEW;
END $$;
CREATE TRIGGER audit_entity_fill BEFORE INSERT ON audit_events
  FOR EACH ROW EXECUTE FUNCTION audit_entity_from_detail();
