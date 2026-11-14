-- Document branding themes: name, footer text, one default per company. Logo upload is not supported yet.
CREATE TABLE branding_themes (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES organizations(id),
  name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 100),
  footer text NOT NULL DEFAULT '' CHECK (length(footer) <= 500),
  is_default boolean NOT NULL DEFAULT false,
  created_by bigint NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, id),
  UNIQUE (org_id, name)
);
CREATE UNIQUE INDEX branding_themes_one_default ON branding_themes (org_id) WHERE is_default;
