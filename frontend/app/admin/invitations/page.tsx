"use client";
import Link from "next/link";
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

const TIERS = ["essential", "growth", "premier"] as const;

// Invitation is an allow-list entry for Google sign-in only: no token, no email with a password.
export default function AdminInvitations() {
  const [allowed, setAllowed] = useState<boolean | null>(null);
  const [email, setEmail] = useState("");
  const [tier, setTier] = useState<(typeof TIERS)[number]>("essential");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<{ kind: "ok" | "error"; text: string } | null>(null);

  useEffect(() => {
    api<{ is_platform_admin: boolean }>("/v1/me")
      .then((m) => setAllowed(m.is_platform_admin))
      .catch(() => setAllowed(false));
  }, []);

  async function send(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setNotice(null);
    try {
      await api("/v1/admin/invitations", { method: "POST", body: JSON.stringify({ email: email.trim(), tier }) });
      setNotice({ kind: "ok", text: `Saved. ${email.trim()} can sign in with Google and will get the ${tier} plan. The invitation is optional: anyone with a verified Google email can sign up.` });
      setEmail("");
    } catch (err) {
      const e2 = err as ApiError;
      setNotice({ kind: "error", text: e2.error === "account_exists" ? "An account already exists for this email, so the plan was not changed here. Use the users page." : e2.error === "email_unavailable" ? "Email is not configured, so the invitation was not saved." : messageFor(e2.error, e2.status) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <p className="small"><Link href="/overview">Back to Tidal</Link></p>
      <PageHeader title="Platform admin: invitations" description="Give a Google address a plan before its first sign-in." />
      {allowed === null && <p className="muted">Checking access…</p>}
      {allowed === false && <div className="notice" role="status">This area is only for platform administrators.</div>}
      {allowed === true && (
        <>
          <form onSubmit={send} className="stack-form" noValidate>
            <label htmlFor="inv-email">Google email address</label>
            <input id="inv-email" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
            <label htmlFor="inv-tier">Plan</label>
            <select id="inv-tier" value={tier} onChange={(e) => setTier(e.target.value as (typeof TIERS)[number])}>
              {TIERS.map((t) => <option key={t} value={t}>{t[0].toUpperCase() + t.slice(1)}</option>)}
            </select>
            <button type="submit" disabled={busy || !email.trim()}>{busy ? "Saving…" : "Save invitation"}</button>
          </form>
          {notice && <p role={notice.kind === "error" ? "alert" : "status"} className={notice.kind === "error" ? "form-error" : "muted"}>{notice.text}</p>}
        </>
      )}
    </>
  );
}
