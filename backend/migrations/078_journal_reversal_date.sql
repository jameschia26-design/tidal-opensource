-- Auto-reversing manual journals: the reversal is written when the journal is created, dated here.
-- Null keeps the ordinary behaviour; the reversal itself stays a normal journal row.
ALTER TABLE journals ADD COLUMN reversal_date date;
