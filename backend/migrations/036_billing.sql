-- Attachments allow credit notes (missed in 034) and billing catalog images.
DO $$ DECLARE c text;
BEGIN
 SELECT conname INTO c FROM pg_constraint WHERE conrelid='attachments'::regclass AND contype='c' AND pg_get_constraintdef(oid) LIKE '%entity_kind%';
 EXECUTE format('ALTER TABLE attachments DROP CONSTRAINT %I', c);
END $$;
ALTER TABLE attachments ADD CONSTRAINT attachments_entity_kind_check CHECK (entity_kind IN
 ('contacts','invoices','quotes','bills','items','expenses','purchase-orders','purchase-receives','sales-receipts','credit-notes','billing-plans','billing-addons'));

-- Billing catalog: product -> plans, addons, coupons. Subscriptions snapshot prices at signup and create invoice drafts when run.
CREATE TABLE billing_products (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
  description text NOT NULL DEFAULT '' CHECK (length(description) <= 4000),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id)
);
CREATE UNIQUE INDEX billing_products_name_uq ON billing_products (org_id, lower(name));

CREATE TABLE billing_plans (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  product_id bigint NOT NULL,
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
  code text NOT NULL CHECK (code ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,49}$'),
  interval_count int NOT NULL CHECK (interval_count BETWEEN 1 AND 120),
  interval_unit text NOT NULL CHECK (interval_unit IN ('week','month','year')),
  billing_cycles int CHECK (billing_cycles IS NULL OR billing_cycles BETWEEN 1 AND 1200),
  description text NOT NULL DEFAULT '' CHECK (length(description) <= 4000),
  pricing_model text NOT NULL DEFAULT 'per_unit' CHECK (pricing_model = 'per_unit'),
  unit_name text NOT NULL DEFAULT '' CHECK (length(unit_name) <= 50),
  price_cents bigint NOT NULL CHECK (price_cents BETWEEN 0 AND 100000000000000),
  trial_days int NOT NULL DEFAULT 0 CHECK (trial_days BETWEEN 0 AND 365),
  setup_fee_cents bigint NOT NULL DEFAULT 0 CHECK (setup_fee_cents BETWEEN 0 AND 100000000000000),
  item_type text NOT NULL CHECK (item_type IN ('goods','service')),
  tax text NOT NULL DEFAULT 'none' CHECK (tax = 'none'),
  show_in_widget boolean NOT NULL DEFAULT true,
  allow_plan_change boolean NOT NULL DEFAULT true,
  plan_account_id bigint NOT NULL,
  setup_fee_account_id bigint NOT NULL,
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, product_id) REFERENCES billing_products (org_id, id),
  FOREIGN KEY (org_id, plan_account_id) REFERENCES accounts (org_id, id),
  FOREIGN KEY (org_id, setup_fee_account_id) REFERENCES accounts (org_id, id)
);
CREATE UNIQUE INDEX billing_plans_code_uq ON billing_plans (org_id, lower(code));

CREATE TABLE billing_addons (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  product_id bigint NOT NULL,
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
  code text NOT NULL CHECK (code ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,49}$'),
  description text NOT NULL DEFAULT '' CHECK (length(description) <= 4000),
  addon_type text NOT NULL CHECK (addon_type IN ('one_time','recurring')),
  interval_count int NOT NULL DEFAULT 1 CHECK (interval_count BETWEEN 1 AND 120),
  interval_unit text NOT NULL DEFAULT 'month' CHECK (interval_unit IN ('week','month','year')),
  pricing_model text NOT NULL DEFAULT 'per_unit' CHECK (pricing_model = 'per_unit'),
  unit_name text NOT NULL DEFAULT '' CHECK (length(unit_name) <= 50),
  price_cents bigint NOT NULL CHECK (price_cents BETWEEN 0 AND 100000000000000),
  plan_ids bigint[] NOT NULL DEFAULT '{}',
  item_type text NOT NULL CHECK (item_type IN ('goods','service')),
  tax text NOT NULL DEFAULT 'none' CHECK (tax = 'none'),
  show_in_widget boolean NOT NULL DEFAULT true,
  allow_portal_association boolean NOT NULL DEFAULT true,
  account_id bigint NOT NULL,
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, product_id) REFERENCES billing_products (org_id, id),
  FOREIGN KEY (org_id, account_id) REFERENCES accounts (org_id, id)
);
CREATE UNIQUE INDEX billing_addons_code_uq ON billing_addons (org_id, lower(code));

CREATE TABLE billing_coupons (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  product_id bigint NOT NULL,
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
  code text NOT NULL CHECK (code ~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,49}$'),
  discount_type text NOT NULL CHECK (discount_type IN ('flat','percent')),
  discount_cents bigint NOT NULL DEFAULT 0 CHECK (discount_cents BETWEEN 0 AND 100000000000000),
  discount_basis_points int NOT NULL DEFAULT 0 CHECK (discount_basis_points BETWEEN 0 AND 10000),
  redemption_type text NOT NULL CHECK (redemption_type IN ('one_time','limited','forever')),
  redemption_cycles int NOT NULL DEFAULT 0 CHECK (redemption_cycles BETWEEN 0 AND 1200),
  applicability text NOT NULL CHECK (applicability IN ('plan','addon','both')),
  plan_ids bigint[] NOT NULL DEFAULT '{}',
  addon_ids bigint[] NOT NULL DEFAULT '{}',
  expires_on date,
  max_redemptions int CHECK (max_redemptions IS NULL OR max_redemptions BETWEEN 1 AND 1000000),
  redemptions int NOT NULL DEFAULT 0 CHECK (redemptions >= 0),
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, product_id) REFERENCES billing_products (org_id, id),
  CHECK ((discount_type='flat' AND discount_cents > 0 AND discount_basis_points = 0) OR (discount_type='percent' AND discount_basis_points > 0 AND discount_cents = 0)),
  CHECK ((redemption_type='limited') = (redemption_cycles > 0)),
  CHECK (max_redemptions IS NULL OR redemptions <= max_redemptions)
);
CREATE UNIQUE INDEX billing_coupons_code_uq ON billing_coupons (org_id, lower(code));

CREATE TABLE billing_subscriptions (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  contact_id bigint NOT NULL,
  mode text NOT NULL CHECK (mode IN ('plan','items')),
  plan_id bigint,
  plan_name text NOT NULL DEFAULT '',
  quantity bigint NOT NULL DEFAULT 1 CHECK (quantity BETWEEN 1 AND 1000000),
  interval_count int NOT NULL CHECK (interval_count BETWEEN 1 AND 120),
  interval_unit text NOT NULL CHECK (interval_unit IN ('week','month','year')),
  billing_cycles int CHECK (billing_cycles IS NULL OR billing_cycles BETWEEN 1 AND 1200),
  start_date date NOT NULL,
  first_bill_date date NOT NULL CHECK (first_bill_date >= start_date),
  next_bill_date date,
  cycles_billed int NOT NULL DEFAULT 0 CHECK (cycles_billed >= 0),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','cancelled','completed')),
  first_lines jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(first_lines)='array'),
  recurring_lines jsonb NOT NULL CHECK (jsonb_typeof(recurring_lines)='array' AND jsonb_array_length(recurring_lines) > 0),
  addons jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(addons)='array'),
  coupon_id bigint,
  coupon jsonb,
  reference text NOT NULL DEFAULT '' CHECK (length(reference) <= 100),
  notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 4000),
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  cancelled_at timestamptz,
  UNIQUE (org_id, id),
  FOREIGN KEY (org_id, contact_id) REFERENCES contacts (org_id, id),
  FOREIGN KEY (org_id, plan_id) REFERENCES billing_plans (org_id, id),
  FOREIGN KEY (org_id, coupon_id) REFERENCES billing_coupons (org_id, id),
  CHECK ((mode='plan') = (plan_id IS NOT NULL)),
  CHECK ((status='active') = (next_bill_date IS NOT NULL))
);
ALTER TABLE invoices ADD COLUMN subscription_id bigint;
ALTER TABLE invoices ADD COLUMN subscription_cycle int;
ALTER TABLE invoices ADD CONSTRAINT invoices_subscription_cycle_uq UNIQUE (org_id, subscription_id, subscription_cycle);
ALTER TABLE invoices ADD FOREIGN KEY (org_id, subscription_id) REFERENCES billing_subscriptions (org_id, id);
