"use client";
import Link from "next/link";
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import PreviewNotice from "@/components/PreviewNotice";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type User = { id: number; email: string; name: string; tier: string; is_platform_admin: boolean; disabled: boolean; created_at: string };
const TIERS = ["essential", "growth", "premier"] as const;

// Display gate only: the backend returns 403 to anyone who is not a platform admin.
export default function AdminUsers() {
  const [allowed, setAllowed] = useState<boolean | null>(null);
  const [users, setUsers] = useState<User[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api<{ is_platform_admin: boolean }>("/v1/me")
      .then((m) => {
        setAllowed(m.is_platform_admin);
        if (m.is_platform_admin) return api<{ users: User[] }>("/v1/admin/users").then((r) => setUsers(r.users));
      })
      .catch((e: ApiError) => { setAllowed(false); setError(messageFor(e.error, e.status)); });
  }, []);

  async function setTier(u: User, tier: string) {
    try {
      const r = await api<{ id: number; tier: string }>(`/v1/admin/users/${u.id}`, { method: "PATCH", body: JSON.stringify({ tier }) });
      setUsers((prev) => (prev ?? []).map((x) => (x.id === r.id ? { ...x, tier: r.tier } : x)));
    } catch (err) {
      setError(messageFor((err as ApiError).error, (err as ApiError).status));
    }
  }

  return (
    <>
      <p className="small"><Link href="/overview">Back to Tidal</Link></p>
      <PageHeader title="Platform admin: users" description="Set each user's plan." />
      {allowed === true && <p><Link href="/admin/invitations">Invite a user</Link></p>}
      <PreviewNotice>Changing a plan does not charge anyone. Prices are not set in this release.</PreviewNotice>
      {allowed === null && <p className="muted">Checking access…</p>}
      {allowed === false && <div className="notice" role="status">This area is only for platform administrators.</div>}
      {error && <p role="alert" className="form-error">{error}</p>}
      {users && (
        <table className="doc-table">
          <caption className="sr-only">Users</caption>
          <thead><tr><th scope="col">Email</th><th scope="col">Name</th><th scope="col">Plan</th><th scope="col">Status</th></tr></thead>
          <tbody>
            {users.map((u) => (
              <tr key={u.id}>
                <td>{u.email}</td>
                <td>{u.name || "—"}</td>
                <td>
                  <select aria-label={`Plan for ${u.email}`} value={u.tier} onChange={(e) => setTier(u, e.target.value)}>
                    {TIERS.map((t) => <option key={t} value={t}>{t}</option>)}
                  </select>
                </td>
                <td>{u.disabled ? "Deactivated" : u.is_platform_admin ? "Platform admin" : "Active"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
