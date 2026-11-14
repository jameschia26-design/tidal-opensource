-- Inventory tracking: stock on hand is the sum of posted document movements (invoices out, bills in).
-- Items opt in with track_inventory; untracked items never produce movements.
ALTER TABLE items ADD COLUMN track_inventory boolean NOT NULL DEFAULT false;

ALTER TABLE invoice_lines ADD COLUMN item_id bigint;
ALTER TABLE invoice_lines ADD CONSTRAINT invoice_lines_item_fk FOREIGN KEY (org_id, item_id) REFERENCES items(org_id, id);
CREATE INDEX invoice_lines_item ON invoice_lines(org_id, item_id);

ALTER TABLE bill_lines ADD COLUMN item_id bigint;
ALTER TABLE bill_lines ADD CONSTRAINT bill_lines_item_fk FOREIGN KEY (org_id, item_id) REFERENCES items(org_id, id);
CREATE INDEX bill_lines_item ON bill_lines(org_id, item_id);

CREATE TABLE stock_movements (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  item_id bigint NOT NULL,
  movement_date date NOT NULL,
  qty_delta bigint NOT NULL CHECK (qty_delta <> 0),
  unit_cost_cents bigint NOT NULL CHECK (unit_cost_cents >= 0),
  source_kind text NOT NULL CHECK (source_kind IN ('invoice','bill')),
  source_id bigint NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  -- One movement per item per document: posting sums the document's lines for that item.
  UNIQUE (org_id, item_id, source_kind, source_id),
  FOREIGN KEY (org_id, item_id) REFERENCES items(org_id, id)
);
CREATE INDEX stock_movements_item_date ON stock_movements(org_id, item_id, movement_date);

CREATE TRIGGER stock_movements_append_only BEFORE UPDATE OR DELETE ON stock_movements
  FOR EACH ROW EXECUTE FUNCTION forbid_change();
