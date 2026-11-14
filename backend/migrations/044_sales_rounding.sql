-- Company rounding rule for sales documents: none, whole dollar, or nearest 5 or 10 cents.
ALTER TABLE organizations ADD COLUMN sales_rounding text NOT NULL DEFAULT 'none' CHECK (sales_rounding IN ('none','whole','5c','10c'));
