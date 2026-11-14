CREATE TABLE pricing_widgets (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  product_id bigint NOT NULL,
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
  created_at timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (org_id, product_id) REFERENCES billing_products(org_id, id)
);
CREATE UNIQUE INDEX pricing_widgets_name_uq ON pricing_widgets(org_id, lower(name));
