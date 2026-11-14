-- R24: a saved GST F5 draft may be approved once, and an approved snapshot can never change.
CREATE TABLE gst_f5_snapshots (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  period_start date NOT NULL,
  period_end date NOT NULL,
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','approved')),
  payload jsonb NOT NULL,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  approved_by bigint REFERENCES users(id),
  approved_at timestamptz,
  CHECK ((status='approved') = (approved_by IS NOT NULL AND approved_at IS NOT NULL)),
  UNIQUE (org_id, id)
);

CREATE FUNCTION gst_f5_snapshot_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.status = 'approved' THEN
      RAISE EXCEPTION 'approved GST snapshot is immutable';
    END IF;
    RETURN OLD;
  END IF;
  IF OLD.status = 'approved' AND NEW.payload IS DISTINCT FROM OLD.payload THEN
    RAISE EXCEPTION 'approved GST snapshot is immutable';
  END IF;
  IF OLD.status = 'approved' AND NEW.status <> 'approved' THEN
    RAISE EXCEPTION 'approved GST snapshot is immutable';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER gst_f5_snapshot_guard BEFORE UPDATE OR DELETE ON gst_f5_snapshots
  FOR EACH ROW EXECUTE FUNCTION gst_f5_snapshot_guard();
