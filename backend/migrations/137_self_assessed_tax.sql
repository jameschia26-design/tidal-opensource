-- Self-assessed purchase tax (imported services, low-value goods and reverse charge) is not paid to
-- the supplier, so it is kept apart from the ordinary input tax on the same bill. The bill total is
-- the supplier's net; the GST control carries matching input and output legs when the bill posts.
ALTER TABLE bills ADD COLUMN self_gst_cents bigint NOT NULL DEFAULT 0 CHECK (self_gst_cents >= 0);
