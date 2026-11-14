-- Phase 3b GST returns: one return per tax period with a filing state. Figures come from the ledger
-- through the existing draft F5 calculation; filing freezes the payload as at the moment it was filed.
CREATE TABLE gst_returns (
  id           bigserial PRIMARY KEY,
  org_id       bigint NOT NULL REFERENCES organizations(id),
  period_start date NOT NULL,
  period_end   date NOT NULL,
  due_date     date NOT NULL,
  status       text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'filed')),
  payload      jsonb,
  filed_by     bigint REFERENCES users(id),
  filed_at     timestamptz,
  created_by   bigint NOT NULL REFERENCES users(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  CHECK (period_end >= period_start),
  CHECK ((status = 'filed') = (filed_by IS NOT NULL AND filed_at IS NOT NULL)),
  UNIQUE (org_id, period_start, period_end),
  UNIQUE (org_id, id)
);

CREATE FUNCTION gst_return_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    IF OLD.status = 'filed' THEN
      RAISE EXCEPTION 'a filed GST return is immutable';
    END IF;
    RETURN OLD;
  END IF;
  IF OLD.status = 'filed' AND (NEW.payload IS DISTINCT FROM OLD.payload OR NEW.status <> 'filed') THEN
    RAISE EXCEPTION 'a filed GST return is immutable';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER gst_return_guard BEFORE UPDATE OR DELETE ON gst_returns
  FOR EACH ROW EXECUTE FUNCTION gst_return_guard();
