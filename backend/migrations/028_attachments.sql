CREATE TABLE attachments (
 id bigserial PRIMARY KEY,
 org_id bigint NOT NULL REFERENCES organizations(id),
 entity_kind text NOT NULL CHECK(entity_kind IN ('contacts','invoices','quotes','bills','items','expenses','purchase-orders','purchase-receives','sales-receipts')),
 entity_id bigint NOT NULL CHECK(entity_id>0),
 filename text NOT NULL CHECK(length(filename) BETWEEN 1 AND 200),
 content_type text NOT NULL,
 contents bytea NOT NULL CHECK(octet_length(contents) BETWEEN 1 AND 10485760),
 created_by bigint NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(org_id,id)
);
CREATE INDEX attachments_entity_idx ON attachments(org_id,entity_kind,entity_id,id);
