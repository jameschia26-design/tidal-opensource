-- purge_demo_data.sql
-- Purges demo data older than 7 days from Tidal demo database.
BEGIN;
SET session_replication_role = 'replica';

-- Find all expired organization IDs
CREATE TEMP TABLE _expired_orgs ON COMMIT DROP AS
SELECT id FROM organizations WHERE created_at < now() - interval '7 days';

-- Delete from all tables containing org_id
DO $$
DECLARE
    rec RECORD;
BEGIN
    FOR rec IN 
        SELECT table_name 
        FROM information_schema.columns 
        WHERE column_name = 'org_id' 
          AND table_schema = 'public' 
          AND table_name NOT IN ('organizations')
        ORDER BY table_name
    LOOP
        EXECUTE format('DELETE FROM %I WHERE org_id IN (SELECT id FROM _expired_orgs)', rec.table_name);
    END LOOP;
END $$;

-- Delete expired organizations
DELETE FROM organizations WHERE id IN (SELECT id FROM _expired_orgs);

-- Delete orphaned demo users older than 7 days (preserve platform admins)
DELETE FROM users
WHERE created_at < now() - interval '7 days'
  AND NOT is_platform_admin
  AND id NOT IN (SELECT user_id FROM memberships);

-- Clean up expired sessions, challenges, and transient auth rows
DELETE FROM sessions WHERE user_id NOT IN (SELECT id FROM users);
DELETE FROM sessions WHERE created_at < now() - interval '7 days';
DELETE FROM login_challenges WHERE created_at < now() - interval '7 days';
DELETE FROM login_failures WHERE created_at < now() - interval '7 days';
DELETE FROM login_send_log WHERE created_at < now() - interval '7 days';
DELETE FROM oauth_states WHERE created_at < now() - interval '7 days';
DELETE FROM password_reset_requests WHERE created_at < now() - interval '7 days';
DELETE FROM signup_requests WHERE created_at < now() - interval '7 days';

SET session_replication_role = 'origin';
COMMIT;
VACUUM ANALYZE;
