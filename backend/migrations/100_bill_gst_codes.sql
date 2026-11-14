-- Bills gain the zero-rated and exempt GST codes that invoices already had. The 087 migration is left as
-- released; widening the check constraint belongs here so databases that ran 087 are upgraded too (B12).
ALTER TABLE bill_lines DROP CONSTRAINT bill_lines_gst_code_check;
ALTER TABLE bill_lines ADD CONSTRAINT bill_lines_gst_code_check
  CHECK (gst_code IN ('NONE', 'SR', 'ZR', 'ES', 'OS'));
