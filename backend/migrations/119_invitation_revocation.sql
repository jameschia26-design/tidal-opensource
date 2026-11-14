ALTER TABLE invitations ADD COLUMN revoked_at timestamptz;

-- Legacy book invitations did not authorize access to the entire company.
WITH revoked AS (
 UPDATE invitations i SET revoked_at=now()
 FROM organizations o
 WHERE i.org_id=o.id AND o.parent_company_id IS NOT NULL
   AND i.accepted_at IS NULL AND i.expires_at>now()
 RETURNING i.id, o.parent_company_id
)
INSERT INTO audit_events(org_id,action,detail)
SELECT parent_company_id,'member.legacy_invitation_revoked',jsonb_build_object('invitation',id) FROM revoked;
