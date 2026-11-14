-- Unposted payment forms ("Save as Draft"). Nothing here touches the books; posting goes through the normal payment endpoints.
CREATE TABLE payment_drafts (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  side text NOT NULL CHECK (side IN ('received','made')),
  data jsonb NOT NULL CHECK (jsonb_typeof(data)='object' AND pg_column_size(data) <= 20000),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id)
);
CREATE INDEX payment_drafts_org ON payment_drafts (org_id, side, id);
