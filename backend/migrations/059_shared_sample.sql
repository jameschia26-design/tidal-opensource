-- An operator must explicitly mark an existing seeded company; names confer no trust.
ALTER TABLE organizations ADD COLUMN is_shared_sample boolean NOT NULL DEFAULT false;
ALTER TABLE organizations ADD CONSTRAINT shared_sample_is_company CHECK (NOT is_shared_sample OR parent_company_id IS NULL);
CREATE UNIQUE INDEX one_shared_sample ON organizations (is_shared_sample) WHERE is_shared_sample;
