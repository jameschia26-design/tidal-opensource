-- Dated GST-registration history. Existing organizations retain gst_registered as their current
-- compatibility status; do not invent a historical effective date during migration.
CREATE TABLE gst_registrations (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  effective_from date NOT NULL,
  effective_to date,
  registration_number text NOT NULL DEFAULT '' CHECK (length(registration_number) <= 30),
  created_by bigint REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (effective_to IS NULL OR effective_to >= effective_from),
  UNIQUE (org_id, effective_from)
);
CREATE UNIQUE INDEX gst_registrations_one_open ON gst_registrations(org_id) WHERE effective_to IS NULL;
CREATE INDEX gst_registrations_effective_lookup ON gst_registrations(org_id,effective_from,effective_to);
CREATE FUNCTION check_gst_registration_period() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM gst_registrations r WHERE r.org_id=NEW.org_id AND r.id<>NEW.id
    AND daterange(r.effective_from,r.effective_to,'[]') && daterange(NEW.effective_from,NEW.effective_to,'[]')) THEN
    RAISE EXCEPTION 'overlapping GST registration periods' USING ERRCODE='exclusion_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER gst_registrations_no_overlap BEFORE INSERT OR UPDATE ON gst_registrations
  FOR EACH ROW EXECUTE FUNCTION check_gst_registration_period();
CREATE FUNCTION protect_gst_registration_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='DELETE' OR OLD.effective_to IS NOT NULL OR NEW.effective_to IS NULL
     OR NEW.effective_to < OLD.effective_from
     OR (to_jsonb(NEW) - 'effective_to') <> (to_jsonb(OLD) - 'effective_to') THEN
    RAISE EXCEPTION 'GST registration history is append-only' USING ERRCODE='check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER gst_registrations_append_only BEFORE UPDATE OR DELETE ON gst_registrations
  FOR EACH ROW EXECUTE FUNCTION protect_gst_registration_history();

-- Static statutory rates are effective-dated facts, not tenant-editable tax configuration.
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='bill_lines'::regclass AND conname='bill_lines_gst_code_check') THEN
    ALTER TABLE bill_lines DROP CONSTRAINT bill_lines_gst_code_check;
  END IF;
END $$;
ALTER TABLE bill_lines ADD CONSTRAINT bill_lines_gst_code_check CHECK (gst_code IN ('NONE','SR','ZR','ES','OS'));

-- Migration records no guessed dates for pre-existing GST-registered organizations.
-- Application permits profile edits but status changes require source-backed history.
ALTER TABLE organizations ADD COLUMN gst_registration_history_missing boolean NOT NULL DEFAULT false;
UPDATE organizations SET gst_registration_history_missing=true WHERE gst_registered;

CREATE TABLE gst_rates (
  effective_from date PRIMARY KEY,
  rate_bp bigint NOT NULL CHECK (rate_bp BETWEEN 0 AND 10000),
  source_url text NOT NULL
);
INSERT INTO gst_rates(effective_from,rate_bp,source_url) VALUES
 ('1994-04-01',300,'https://www.iras.gov.sg/taxes/goods-services-tax-(gst)/basics-of-gst/current-gst-rates'),
 ('2003-01-01',400,'https://www.iras.gov.sg/taxes/goods-services-tax-(gst)/basics-of-gst/current-gst-rates'),
 ('2004-01-01',500,'https://www.iras.gov.sg/taxes/goods-services-tax-(gst)/basics-of-gst/current-gst-rates'),
 ('2007-07-01',700,'https://www.iras.gov.sg/taxes/goods-services-tax-(gst)/basics-of-gst/current-gst-rates'),
 ('2023-01-01',800,'https://www.iras.gov.sg/taxes/goods-services-tax-(gst)/basics-of-gst/current-gst-rates'),
 ('2024-01-01',900,'https://www.iras.gov.sg/taxes/goods-services-tax-(gst)/basics-of-gst/current-gst-rates');

