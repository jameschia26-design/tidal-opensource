-- Persist company-scoped favourites for saved reports.
ALTER TABLE saved_reports ADD COLUMN favourite boolean NOT NULL DEFAULT false;
