-- Recurring templates can bill to a fixed day of the month after the invoice month, like Xero's
-- "of the following month" due date. Blank keeps the day-count term; a set day is clamped to the
-- month length (31 becomes the last day of a short February).
ALTER TABLE recurring_templates ADD COLUMN due_day int CHECK (due_day IS NULL OR (due_day BETWEEN 1 AND 31));
