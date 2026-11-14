"use client";
import { useEffect, useState } from "react";
import Link from "next/link";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Invitation = { id: number; org_id: number; org_name: string; role: string; expires_at: string };

export default function Invitations() {
  const { refresh, select } = useOrg();
  const [invitations, setInvitations] = useState<Invitation[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [signInRequired, setSignInRequired] = useState(false);

  useEffect(() => {
    let live = true;
    api<{ invitations: Invitation[] }>("/v1/invitations").then(r => { if (live) setInvitations(r.invitations); }).catch((e: ApiError) => { if (live) { if (e.status === 401) setSignInRequired(true); else setError(messageFor(e.error, e.status)); } });
    return () => { live = false; };
  }, []);

  async function accept(invitation: Invitation) {
    if (busy) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const result = await api<{ status: string; org_id: number }>(`/v1/invitations/${invitation.id}/accept`, { method: "POST" });
      setInvitations(current => current?.filter(i => i.id !== invitation.id) ?? []);
      await refresh();
      select(result.org_id);
      setNotice(`Invitation to ${invitation.org_name} accepted. Check the company switcher to open it.`);
    } catch (err) {
      const e = err as ApiError;
      setError(e.error === "not_found" ? "This invitation is no longer available. It may have expired or been cancelled. Refresh this page." : e.error === "already_member" ? "You already have access to this company. Refresh this page." : messageFor(e.error, e.status));
    } finally { setBusy(false); }
  }

  return <>
    <PageHeader title="Company invitations" description="Review invitations sent to your verified email address." />
    <p>Accept an invitation to join a company. Signing in alone does not grant access.</p>
    {signInRequired && <p role="alert">Sign in or create an account with the invited email, then return here to accept. <Link href="/login?next=%2Finvitations">Sign in</Link> · <Link href="/signup?next=%2Finvitations">Create account</Link></p>}
    {error && <p role="alert" className="form-error">{error}</p>}
    {notice && <p role="status">{notice}</p>}
    {invitations == null && !error && !signInRequired && <p role="status">Loading invitations…</p>}
    {invitations?.length === 0 && <p className="notice">No pending company invitations for your verified email.</p>}
    {invitations != null && invitations.length > 0 && <div className="table-scroll"><table className="doc-table"><caption>Pending company invitations</caption><thead><tr><th scope="col">Company</th><th scope="col">Role</th><th scope="col">Expires</th><th scope="col">Action</th></tr></thead><tbody>{invitations.map(i => <tr key={i.id}><th scope="row">{i.org_name}</th><td>{i.role}</td><td>{i.expires_at.slice(0, 10)}</td><td><button disabled={busy} onClick={() => void accept(i)}>Accept<span className="sr-only"> invitation to {i.org_name}</span></button></td></tr>)}</tbody></table></div>}
  </>;
}
