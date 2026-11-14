-- Company logo shown at the top of every document PDF.
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS logo_bytes bytea;
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS logo_type text;
ALTER TABLE organizations DROP CONSTRAINT IF EXISTS organizations_logo_type_check;
ALTER TABLE organizations ADD CONSTRAINT organizations_logo_type_check CHECK (logo_type IS NULL OR logo_type IN ('png','jpeg'));
