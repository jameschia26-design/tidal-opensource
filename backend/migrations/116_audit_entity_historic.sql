-- R26 follow-up: 115 fills entity columns on new inserts; this backfills events already stored, so the
-- record filter also returns history from before the columns existed.
-- audit_events is append-only, so the guard is lifted for this one migration and restored immediately;
-- all of it runs in the migration transaction. A fresh database has no rows, so this is a no-op there.
ALTER TABLE audit_events DISABLE TRIGGER audit_events_append_only;
UPDATE audit_events e
SET entity_kind = COALESCE(e.entity_kind, m.kind),
    entity_id = COALESCE(e.entity_id, m.value)
FROM (
  SELECT DISTINCT ON (a.id) a.id, replace(d.key, '_id', '') AS kind, NULLIF(d.value, '')::bigint AS value
  FROM audit_events a
  CROSS JOIN LATERAL jsonb_each_text(a.detail) AS d(key, value)
  WHERE d.key IN ('invoice_id','bill_id','expense_id','credit_note_id','customer_credit_note_id',
                  'supplier_credit_id','payment_id','purchase_order_id','purchase_receive_id',
                  'quote_id','document_id','asset_id','adjustment_id','journal_id')
    AND NULLIF(d.value, '') ~ '^[0-9]+$'
  ORDER BY a.id, array_position(ARRAY['invoice_id','bill_id','expense_id','credit_note_id',
    'customer_credit_note_id','supplier_credit_id','payment_id','purchase_order_id',
    'purchase_receive_id','quote_id','document_id','asset_id','adjustment_id','journal_id']::text[], d.key)
) AS m
WHERE e.id = m.id AND (e.entity_kind IS NULL OR e.entity_id IS NULL);
ALTER TABLE audit_events ENABLE TRIGGER audit_events_append_only;
