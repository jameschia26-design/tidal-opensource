-- R24 needs zero-rated, exempt and out-of-scope sales to keep their F5 code, so invoice lines accept them.
-- The constraint is dropped by name from 058 (invoice_lines_gst_code_check) and widened to match bills.
ALTER TABLE invoice_lines DROP CONSTRAINT invoice_lines_gst_code_check;
ALTER TABLE invoice_lines ADD CONSTRAINT invoice_lines_gst_code_check
  CHECK (gst_code IN ('NONE', 'SR', 'ZR', 'ES', 'OS'));
