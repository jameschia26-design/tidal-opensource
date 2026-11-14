-- Stock locations and transfers. Stock movements keep a nullable location: NULL is the company's
-- default location, so existing movements need no rewrite and the append-only trigger still holds.
CREATE TABLE inventory_locations (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
  is_default boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  UNIQUE (org_id, name)
);
CREATE UNIQUE INDEX inventory_locations_one_default ON inventory_locations (org_id) WHERE is_default;

ALTER TABLE stock_movements ADD COLUMN location_id bigint;
ALTER TABLE stock_movements ADD CONSTRAINT stock_movements_location_fk FOREIGN KEY (org_id, location_id) REFERENCES inventory_locations(org_id, id);

CREATE TABLE stock_transfers (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  item_id bigint NOT NULL,
  from_location_id bigint NOT NULL,
  to_location_id bigint NOT NULL,
  transfer_date date NOT NULL,
  qty bigint NOT NULL CHECK (qty > 0),
  note text NOT NULL DEFAULT '' CHECK (length(note) <= 500),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (from_location_id <> to_location_id),
  FOREIGN KEY (org_id, item_id) REFERENCES items(org_id, id),
  FOREIGN KEY (org_id, from_location_id) REFERENCES inventory_locations(org_id, id),
  FOREIGN KEY (org_id, to_location_id) REFERENCES inventory_locations(org_id, id)
);
CREATE INDEX stock_transfers_item_date ON stock_transfers(org_id, item_id, transfer_date);

CREATE TRIGGER stock_transfers_append_only BEFORE UPDATE OR DELETE ON stock_transfers
  FOR EACH ROW EXECUTE FUNCTION forbid_change();
