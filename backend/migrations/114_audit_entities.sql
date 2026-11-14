-- R26: every financial state change names the record it changed, so history can be filtered by record.
ALTER TABLE audit_events ADD COLUMN entity_kind text;
ALTER TABLE audit_events ADD COLUMN entity_id bigint;
CREATE INDEX audit_events_org_time ON audit_events (org_id, created_at DESC, id DESC);
CREATE INDEX audit_events_entity ON audit_events (org_id, entity_kind, entity_id, id DESC);
