-- Tax exclusive/inclusive flag shown on the invoice form. The posted lines and totals stay
-- untouched; the flag only tells future edits how the typed prices were first read.
ALTER TABLE invoices ADD COLUMN amounts_are text NOT NULL DEFAULT 'exclusive' CHECK (amounts_are IN ('exclusive','inclusive'));
