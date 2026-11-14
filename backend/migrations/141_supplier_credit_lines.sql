-- Standalone supplier credits can carry a Xero-style line grid: one net amount per expense
-- account instead of a single expense account. Absent (NULL) keeps the old single-account path;
-- GST stays header-level, never per line.
ALTER TABLE supplier_credits ADD COLUMN lines jsonb
  CHECK (lines IS NULL OR jsonb_typeof(lines) = 'array');
