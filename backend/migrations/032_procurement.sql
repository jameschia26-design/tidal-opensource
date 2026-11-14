-- Purchase orders and purchase receives. Neither posts to the ledger: a PO is a commitment and a receive is a delivery record.
CREATE TABLE purchase_orders (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  contact_id bigint NOT NULL,
  po_number text NOT NULL CHECK (length(trim(po_number)) BETWEEN 1 AND 50),
  reference text NOT NULL DEFAULT '' CHECK (length(reference) <= 100),
  po_date date NOT NULL,
  delivery_date date NOT NULL CHECK (delivery_date > po_date),
  delivery_address text NOT NULL DEFAULT '' CHECK (length(delivery_address) <= 1000),
  shipment_preference text NOT NULL DEFAULT '' CHECK (length(shipment_preference) <= 200),
  lines jsonb NOT NULL,
  commercial jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(commercial)='object'),
  total_cents bigint NOT NULL CHECK (total_cents BETWEEN 1 AND 100000000000000),
  notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
  terms text NOT NULL DEFAULT '' CHECK (length(terms) <= 4000),
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','issued','received','cancelled')),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  UNIQUE (org_id, po_number),
  FOREIGN KEY (org_id, contact_id) REFERENCES contacts (org_id, id),
  CHECK (valid_quote_lines(lines, quote_line_subtotal(lines)) AND commercial_total(quote_line_subtotal(lines), commercial) = total_cents)
);

CREATE TABLE purchase_receives (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  purchase_order_id bigint NOT NULL,
  receive_number text NOT NULL CHECK (length(trim(receive_number)) BETWEEN 1 AND 50),
  received_date date NOT NULL,
  tracking_number text NOT NULL DEFAULT '' CHECK (length(tracking_number) <= 100),
  tracking_url text NOT NULL DEFAULT '' CHECK (length(tracking_url) <= 500),
  items jsonb NOT NULL CHECK (jsonb_typeof(items)='array' AND jsonb_array_length(items) BETWEEN 1 AND 200),
  notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','received','cancelled')),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  UNIQUE (org_id, receive_number),
  FOREIGN KEY (org_id, purchase_order_id) REFERENCES purchase_orders (org_id, id)
);
