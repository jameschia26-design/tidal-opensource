-- Supplier bills can carry the same GST code as invoices. amount_cents stays the net line amount
-- after discount, GST is stored per line, and the header total becomes net plus GST. Existing rows
-- default to NONE and zero GST, so every earlier total still holds.
ALTER TABLE bill_lines
  ADD COLUMN gst_code text NOT NULL DEFAULT 'NONE' CHECK (gst_code IN ('NONE', 'SR')),
  ADD COLUMN gst_rate_bp bigint NOT NULL DEFAULT 0 CHECK (gst_rate_bp >= 0),
  ADD COLUMN gst_cents bigint NOT NULL DEFAULT 0 CHECK (gst_cents >= 0);
ALTER TABLE bills ADD COLUMN gst_cents bigint NOT NULL DEFAULT 0 CHECK (gst_cents >= 0);
