-- Email verification for password signups, email codes with a purpose (sign-in, signup, reset), and a
-- platform admin that can be a password account seeded from the environment instead of Google-only.

ALTER TABLE users ADD COLUMN email_verified_at timestamptz;

-- Accounts that predate verification were reachable through the emailed login code already, and Google
-- sign-in proves the address, so they count as verified. New password signups start NULL.
UPDATE users SET email_verified_at = now() WHERE email_verified_at IS NULL;

ALTER TABLE login_challenges ADD COLUMN purpose text NOT NULL DEFAULT 'login'
  CHECK (purpose IN ('login', 'signup', 'reset'));

-- One active challenge per user per purpose: a pending reset must not cancel a pending sign-in code.
DROP INDEX login_challenges_one_active;
CREATE UNIQUE INDEX login_challenges_one_active ON login_challenges (user_id, purpose) WHERE used_at IS NULL;

-- The admin/google_sub constraint was declared inline and is unnamed; find it by its definition.
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO c FROM pg_constraint
   WHERE conrelid = 'users'::regclass AND contype = 'c'
     AND pg_get_constraintdef(oid) LIKE '%is_platform_admin%';
  IF c IS NOT NULL THEN EXECUTE format('ALTER TABLE users DROP CONSTRAINT %I', c); END IF;
END $$;
