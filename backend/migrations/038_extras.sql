-- Custom fields and reporting tags. Definitions are per company; values hang off any document or contact
-- (same entity kinds as attachments). Values are plain jsonb keyed by definition id.
CREATE TABLE custom_fields (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  entity_kind text NOT NULL,
  label text NOT NULL CHECK (length(trim(label)) BETWEEN 1 AND 100),
  field_type text NOT NULL CHECK (field_type IN ('text','number','date','yesno')),
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id)
);
CREATE UNIQUE INDEX custom_fields_label ON custom_fields (org_id, entity_kind, lower(label));

CREATE TABLE reporting_tags (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
  options jsonb NOT NULL CHECK (jsonb_typeof(options)='array' AND jsonb_array_length(options) BETWEEN 1 AND 50),
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id)
);
CREATE UNIQUE INDEX reporting_tags_name ON reporting_tags (org_id, lower(name));

CREATE TABLE entity_extras (
  org_id bigint NOT NULL REFERENCES organizations(id),
  entity_kind text NOT NULL,
  entity_id bigint NOT NULL,
  custom jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(custom)='object'),
  tags jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(tags)='object'),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, entity_kind, entity_id)
);
