"use client";
import { useEffect, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Member = { id: number; email: string; role: string; permissions?: Record<string, boolean>; last_login?: string | null };
const FEATURES: [string, string][] = [["post_documents", "Post documents"], ["sales", "Sales"], ["purchases", "Purchases"], ["banking", "Banking"], ["contacts", "Contacts"], ["items", "Items"], ["reports", "Reports"], ["settings", "Settings"]];
type Invite = { id: number; email: string; role: string; first_name: string; last_name: string; expires_at: string };
const ROLES: [string, string][] = [["admin", "Admin"], ["accountant", "Accountant"], ["member", "Member"], ["viewer", "Viewer"]];
const blank = () => ({ first_name: "", last_name: "", email: "", role: "member" });

export default function CompanyUsers() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function Workspace() {
  const { selected, refresh } = useOrg();
  const orgId = selected?.id;
  const canManage = selected != null && (selected.role === "owner" || selected.role === "admin");
  const [members, setMembers] = useState<Member[] | null>(null);
  const [invites, setInvites] = useState<Invite[] | null>(null);
  const [form, setForm] = useState(blank);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [logins, setLogins] = useState<{ occurred_at: string; ip: string; user_agent: string }[]>([]);

  async function setPermission(member: Member, feature: string, allowed: boolean) {
    if (orgId == null || busy) return;
    setBusy(true); setError(""); setNotice("");
    try {
      await api(`/v1/members/${member.id}/permissions`, { method: "PATCH", body: JSON.stringify({ org_id: orgId, permissions: { [feature]: allowed } }) });
      setMembers(prev => prev?.map(m => m.id === member.id ? { ...m, permissions: { ...(m.permissions ?? {}), [feature]: allowed } } : m) ?? prev);
      setNotice(`${feature.replace(/_/g, " ")} ${allowed ? "allowed" : "withdrawn"} for ${member.email}.`);
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }
  async function load(id: number) {
    const list = await api<{ members: Member[] }>(`/v1/members?org_id=${id}`);
    setMembers(list.members);
    if (selected?.role === "owner" || selected?.role === "admin") {
      const pending = await api<{ invitations: Invite[] }>(`/v1/members/invitations?org_id=${id}`);
      setInvites(pending.invitations);
    }
  }
  useEffect(() => {
    // Sign-in history is the caller's own; it is not company data and needs no manager role.
    let liveLogins = true;
    api<{ events: { occurred_at: string; ip: string; user_agent: string }[] }>(`/v1/login-history`)
      .then(r => { if (liveLogins) setLogins(r.events); })
      .catch(() => { /* history is optional */ });
    return () => { liveLogins = false; };
  }, []);
  useEffect(() => {
    if (orgId == null || selected?.role === "viewer") return;
    let live = true;
    void Promise.resolve().then(() => load(orgId)).catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [orgId]);

  async function invite(e: FormEvent) {
    e.preventDefault();
    if (busy || orgId == null) return;
    setBusy(true); setError(""); setNotice("");
    try {
      await api<{ status: string }>("/v1/members/invite", { method: "POST", body: JSON.stringify({ org_id: orgId, ...form }) });
      setNotice("Invitation sent. The person must sign in with the invited verified email and accept it under Company invitations.");
      setForm(blank());
      await load(orgId);
    } catch (err) {
      const e2 = err as ApiError;
      setError(e2.error === "email_unavailable" ? "Invitation email could not be sent. Try again later." : e2.error === "already_member" ? "That email already has access to this company." : e2.error === "invite_pending" ? "An invitation for that email is already waiting." : messageFor(e2.error, e2.status));
    } finally { setBusy(false); }
  }

  async function revoke(id: number) {
    if (orgId == null || busy) return;
    setBusy(true); setError(""); setNotice("");
    try { await api(`/v1/members/invitations/${id}?org_id=${orgId}`, { method: "DELETE" }); await load(orgId); }
    catch (err) { const e2 = err as ApiError; setError(messageFor(e2.error, e2.status)); }
    finally { setBusy(false); }
  }

  async function changeMember(member: Member, role?: string) {
    if (orgId == null || busy) return;
    const action = role ? `Change ${member.email}'s role to ${role}?` : `Remove ${member.email}'s access to this company? All their pending invitations to this company will also be revoked.`;
    if (!window.confirm(action)) return;
    setBusy(true); setError(""); setNotice("");
    try {
      await api(`/v1/members/${member.id}?org_id=${orgId}`, role ? { method: "PATCH", body: JSON.stringify({ role }) } : { method: "DELETE" });
      setNotice(role ? "Role changed." : "Access removed.");
      await refresh();
      await load(orgId);
    } catch (err) { const e2 = err as ApiError; setError(messageFor(e2.error, e2.status)); }
    finally { setBusy(false); }
  }

  return (
    <>
      <PageHeader title="Users" description="People who can open this company, and their role." />
      <p className="notice">
        Roles: owners and admins manage people and settings; accountants and members enter, post and record
        payments; viewers can read. Per-feature access switches cover posting, sales, purchases, banking, contacts, items, reports and settings. Only owners can manage admins, and owners cannot be changed here. There is no separate approval step in this release, so anyone who can post can also record payments. See <a href="/limits">What Tidal does not do</a>.
      </p>
      <p className="notice">Company sharing grants access to the parent company and all its books, even when a book is selected. Invitations and access changes apply to the whole company.</p>
      {orgId == null && <p className="notice">Choose a company first.</p>}
      {error && <p role="alert" className="form-error">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      {orgId != null && selected?.role !== "viewer" && members == null && !error && <p role="status">Loading people…</p>}
      {selected?.role === "viewer" && <p className="notice">Your role cannot view the people list.</p>}
      {members != null && <div className="table-scroll"><table className="doc-table"><caption>People with access</caption><thead><tr><th scope="col">Email</th><th scope="col">Role</th><th scope="col">Last signed in</th>{canManage && <th scope="col">Feature access and actions</th>}</tr></thead><tbody>{members.map(m => <tr key={m.id}><th scope="row">{m.email}</th><td>{m.role}</td><td>{m.last_login ? new Date(m.last_login).toLocaleString() : "Never"}</td>{canManage && m.role !== "owner" && <td className="permission-matrix">{FEATURES.map(([key, label]) => <label key={key} className="check-label"><input aria-label={`${label} for ${m.email}`} type="checkbox" disabled={busy || m.role === "owner" || (m.role === "admin" && selected?.role !== "owner")} checked={m.permissions?.[key] === false ? false : true} onChange={e => void setPermission(m, key, e.target.checked)} />{label}</label>)}</td>}{canManage && <td>{m.role !== "owner" && (selected?.role === "owner" || m.role !== "admin") && <MemberActions key={`${m.id}-${m.role}`} member={m} owner={selected?.role === "owner"} busy={busy} change={changeMember} />}</td>}</tr>)}</tbody></table></div>}
      {canManage && <>
        <h2>Invite a user</h2>
        <form className="record-form" onSubmit={invite}>
          <fieldset disabled={busy}>
            <legend>New user</legend>
            <div className="form-grid">
              <div><label htmlFor="inv-first">First name</label><input id="inv-first" maxLength={100} value={form.first_name} onChange={e => setForm({ ...form, first_name: e.target.value })} /></div>
              <div><label htmlFor="inv-last">Last name</label><input id="inv-last" maxLength={100} value={form.last_name} onChange={e => setForm({ ...form, last_name: e.target.value })} /></div>
              <div><label htmlFor="inv-email">Email address</label><input id="inv-email" type="email" required maxLength={254} value={form.email} onChange={e => setForm({ ...form, email: e.target.value })} /></div>
              <div><label htmlFor="inv-role">Role</label><select id="inv-role" value={form.role} onChange={e => setForm({ ...form, role: e.target.value })}>{ROLES.filter(([v]) => v !== "admin" || selected?.role === "owner").map(([v, label]) => <option key={v} value={v}>{label}</option>)}</select></div>
            </div>
            <p className="section-help">The recipient must sign in with the invited verified email and accept the invitation under Company invitations. Names are used to identify pending invitations in this list.</p>
          </fieldset>
          <div className="form-actions"><button type="submit" disabled={busy}>{busy ? "Sending…" : "Send invite"}</button><button type="button" className="button-secondary" disabled={busy} onClick={() => setForm(blank())}>Cancel</button></div>
        </form>
        <h2>Pending invitations</h2>
        {invites == null ? (!error && <p role="status">Loading invitations…</p>) : invites.length === 0 ? <p className="notice">No invitations waiting.</p> : <div className="table-scroll"><table className="doc-table"><caption>Pending invitations</caption><thead><tr><th>Email</th><th>Name</th><th>Role</th><th>Expires</th><th>Action</th></tr></thead><tbody>{invites.map(v => <tr key={v.id}><th scope="row">{v.email}</th><td>{`${v.first_name} ${v.last_name}`.trim() || "—"}</td><td>{v.role}</td><td>{v.expires_at.slice(0, 10)}</td><td>{(v.role !== "admin" || selected?.role === "owner") && <button className="button-secondary" disabled={busy} onClick={() => void revoke(v.id)}>Cancel invite<span className="sr-only"> for {v.email}</span></button>}</td></tr>)}</tbody></table></div>}
      </>}
      {!canManage && orgId != null && <p className="muted small">Only owners and admins can invite people or see pending invitations.</p>}
      <h2>Your recent sign-ins</h2>
      <p className="section-help">Recorded when a sign-in succeeds. Passwords, codes and session tokens are never stored here.</p>
      {logins.length === 0 ? <p className="muted">No sign-ins recorded yet.</p> : <div className="table-scroll"><table className="doc-table"><caption>Last 20 sign-ins for your account</caption><thead><tr><th>When</th><th>Address</th><th>Browser</th></tr></thead><tbody>{logins.map((l, i) => <tr key={i}><td>{new Date(l.occurred_at).toLocaleString()}</td><td>{l.ip || "—"}</td><td>{l.user_agent || "—"}</td></tr>)}</tbody></table></div>}
    </>
  );
}

function MemberActions({ member, owner, busy, change }: { member: Member; owner: boolean; busy: boolean; change: (member: Member, role?: string) => Promise<void> }) {
  const [role, setRole] = useState(member.role);
  return <><label className="sr-only" htmlFor={`role-${member.id}`}>Role for {member.email}</label><select id={`role-${member.id}`} value={role} disabled={busy} onChange={e => setRole(e.target.value)}>{ROLES.filter(([v]) => owner || v !== "admin").map(([v, label]) => <option key={v} value={v}>{label}</option>)}</select>{" "}<button className="button-secondary" disabled={busy || role === member.role} onClick={() => void change(member, role)}>Save role<span className="sr-only"> for {member.email}</span></button>{" "}<button className="button-secondary" disabled={busy} onClick={() => void change(member)}>Remove<span className="sr-only"> {member.email}</span></button></>;
}
