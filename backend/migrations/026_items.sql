CREATE TABLE items (
 id bigserial PRIMARY KEY,
 org_id bigint NOT NULL REFERENCES organizations(id),
 name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 200),
 item_type text NOT NULL CHECK (item_type IN ('goods','service')),
 unit text NOT NULL DEFAULT '' CHECK (length(unit)<=40),
 sku text NOT NULL DEFAULT '' CHECK (length(sku)<=100),
 selling_price_cents bigint NOT NULL CHECK (selling_price_cents BETWEEN 0 AND 100000000000000),
 cost_price_cents bigint NOT NULL CHECK (cost_price_cents BETWEEN 0 AND 100000000000000),
 sales_account_id bigint NOT NULL,
 purchase_account_id bigint,
 sales_description text NOT NULL DEFAULT '' CHECK (length(sales_description)<=4000),
 purchase_description text NOT NULL DEFAULT '' CHECK (length(purchase_description)<=4000),
 preferred_vendor_id bigint,
 created_by bigint NOT NULL REFERENCES users(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(org_id,id),
 FOREIGN KEY(org_id,sales_account_id) REFERENCES accounts(org_id,id),
 FOREIGN KEY(org_id,purchase_account_id) REFERENCES accounts(org_id,id),
 FOREIGN KEY(org_id,preferred_vendor_id) REFERENCES contacts(org_id,id)
);
CREATE UNIQUE INDEX items_sku_uq ON items(org_id,lower(sku)) WHERE sku<>'';
CREATE INDEX items_name_idx ON items(org_id,name,id);
