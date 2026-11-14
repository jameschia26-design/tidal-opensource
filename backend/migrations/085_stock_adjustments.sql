-- Manual stock quantity changes. An adjustment posts one append-only movement with a reason;
-- a stocktake records counted quantities and posts one adjustment per item that differs from the
-- quantity on hand at the count date. Transfers between locations are not modelled because Tidal
-- has a single stock pool per company; see docs/xero-gap-analysis.md.
ALTER TABLE stock_movements DROP CONSTRAINT stock_movements_source_kind_check;
ALTER TABLE stock_movements ADD CONSTRAINT stock_movements_source_kind_check
  CHECK (source_kind IN ('invoice','bill','adjustment','stocktake'));

CREATE TABLE stock_adjustments (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  item_id bigint NOT NULL,
  adjustment_date date NOT NULL,
  qty_delta bigint NOT NULL CHECK (qty_delta <> 0),
  unit_cost_cents bigint NOT NULL CHECK (unit_cost_cents >= 0),
  reason text NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 100),
  note text NOT NULL DEFAULT '' CHECK (length(note) <= 4000),
  stocktake_id bigint,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, item_id) REFERENCES items(org_id, id)
);
CREATE INDEX stock_adjustments_item ON stock_adjustments(org_id, item_id, adjustment_date);

CREATE TABLE stocktakes (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  taken_on date NOT NULL,
  note text NOT NULL DEFAULT '' CHECK (length(note) <= 4000),
  lines jsonb NOT NULL CHECK (jsonb_typeof(lines) = 'array' AND jsonb_array_length(lines) BETWEEN 1 AND 500),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id)
);

ALTER TABLE stock_adjustments ADD CONSTRAINT stock_adjustments_stocktake_fk
  FOREIGN KEY (org_id, stocktake_id) REFERENCES stocktakes(org_id, id);
