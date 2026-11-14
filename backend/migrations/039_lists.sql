CREATE TABLE salespeople (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 200),
  email text NOT NULL DEFAULT '' CHECK (length(email) <= 254),
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX salespeople_name ON salespeople (org_id, lower(name));

CREATE TABLE payment_terms (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
  days integer NOT NULL CHECK (days BETWEEN 0 AND 365),
  active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX payment_terms_name ON payment_terms (org_id, lower(name));
