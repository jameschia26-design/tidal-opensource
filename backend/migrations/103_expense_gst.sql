-- Paid expenses carry GST per line (D6): a code, the rate applied and the tax amount. Existing rows stay
-- NONE with zero tax, so every earlier total still holds.
ALTER TABLE expenses
  ADD COLUMN gst_code text NOT NULL DEFAULT 'NONE' CHECK (gst_code IN ('NONE','SR','ZR','ES','OS')),
  ADD COLUMN gst_rate_bp bigint NOT NULL DEFAULT 0 CHECK (gst_rate_bp >= 0),
  ADD COLUMN gst_cents bigint NOT NULL DEFAULT 0 CHECK (gst_cents >= 0);
