-- Overlapping GST registration periods were only checked by a trigger, so two concurrent saves could both
-- pass the check. A GiST exclusion constraint makes the overlap impossible in the database itself (B13).
CREATE EXTENSION IF NOT EXISTS btree_gist;
ALTER TABLE gst_registrations ADD CONSTRAINT gst_registrations_period_excl
  EXCLUDE USING gist (org_id WITH =, daterange(effective_from, effective_to, '[]') WITH &&);
DROP TRIGGER gst_registrations_no_overlap ON gst_registrations;
DROP FUNCTION check_gst_registration_period();
