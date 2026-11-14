-- Recurring invoice templates can carry optional GST. Lines keep the typed amounts plus a gst_code;
-- GST itself is worked out on every run from that run date's rate. Existing templates default to
-- tax exclusive with no GST codes, so they keep generating the same drafts.
ALTER TABLE recurring_templates ADD COLUMN amounts_are text NOT NULL DEFAULT 'exclusive' CHECK (amounts_are IN ('exclusive','inclusive'));
