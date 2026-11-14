"use client";
import { useEffect, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Theme = { id: number; name: string; footer: string; is_default: boolean };

export default function Branding() { const { selected } = useOrg(); return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />; }
function Workspace() {
  const { selected } = useOrg(); const orgId = selected?.id;
  const [themes, setThemes] = useState<Theme[]>([]); const [loaded, setLoaded] = useState(false); const [busy, setBusy] = useState(false);
  const [error, setError] = useState(""); const [notice, setNotice] = useState("");
  const [name, setName] = useState(""); const [footer, setFooter] = useState(""); const [isDefault, setIsDefault] = useState(false);
  const [editing, setEditing] = useState<Theme | null>(null);
  const [hasLogo, setHasLogo] = useState(false); const [logoRev, setLogoRev] = useState(0);
  const canWrite = selected && ["owner", "admin", "accountant"].includes(selected.role);
  // Backend canEditLogo: owners and administrators only, even though accountants may edit themes.
  const canEditLogo = selected && ["owner", "admin"].includes(selected.role);
  async function load(id: number) {
    setThemes((await api<{ themes: Theme[] }>(`/v1/branding-themes?org_id=${id}`)).themes);
    // The logo is a separate image endpoint, so probe it rather than embedding bytes in the theme list.
    // A 404 probe means no logo uploaded yet; that is an expected answer, not a page error.
    setHasLogo((await fetch(`/api/v1/organizations/${id}/logo`, { credentials: "same-origin", cache: "no-store" })).ok);
    setLoaded(true);
  }
  // Upload goes outside the JSON client because the body is a file.
  async function uploadLogo(file: File) {
    if (!orgId || busy) return;
    if (file.size > 1 << 20) { setError("Choose a PNG or JPEG no larger than 1 MB."); return; }
    setBusy(true); setError(""); setNotice("");
    try {
      const body = new FormData(); body.append("file", file);
      const res = await fetch(`/api/v1/organizations/${orgId}/logo`, { method: "POST", body, credentials: "same-origin" });
      if (!res.ok) { const b = await res.json().catch(() => ({})); throw { error: b.error ?? "invalid_upload", status: res.status } as ApiError; }
      setHasLogo(true); setLogoRev(n => n + 1); setNotice("Logo saved. It now prints on every document.");
    } catch (e) { const x = e as ApiError; setError(x.error === "logo_type_invalid" ? "Choose a PNG or JPEG image." : messageFor(x.error, x.status)); }
    finally { setBusy(false); }
  }
  useEffect(() => { if (orgId != null) void load(orgId).catch((e: ApiError) => setError(messageFor(e.error, e.status))); }, [orgId]);
  async function act(fn: () => Promise<void>) { setBusy(true); setError(""); setNotice(""); try { await fn(); } catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } finally { setBusy(false); } }
  const saveEdit = (e: FormEvent) => { e.preventDefault(); if (!orgId || !editing || busy) return; void act(async () => {
    await api(`/v1/branding-themes/${editing.id}`, { method: "PUT", body: JSON.stringify({ org_id: orgId, name: editing.name, footer: editing.footer }) });
    setEditing(null); setNotice("Theme updated."); await load(orgId);
  }); };
  const create = (e: FormEvent) => { e.preventDefault(); if (!orgId || busy) return; void act(async () => { await api("/v1/branding-themes", { method: "POST", body: JSON.stringify({ org_id: orgId, name, footer, is_default: isDefault }) }); setName(""); setFooter(""); setIsDefault(false); setNotice("Theme saved."); await load(orgId); }); };
  return <><PageHeader title="Branding themes" description="The company logo and the default theme's footer print on every PDF document." />
    {!selected && <p className="notice">Choose a company first.</p>}{error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    <section className="record-form"><h2>Example layout</h2><p className="section-help">Invoices, quotes, bills, purchase orders and receipts use the same header: logo top right, company details top left, document title and number below, then lines, totals and the theme footer.</p><p className="section-help">Supported documents: invoice, quote, bill, purchase order, customer statement, receipt and credit note.</p></section>
    <section className="record-form"><h2>Company logo</h2>
      <p className="section-help">One PNG or JPEG up to 1 MB, drawn in the top-right corner of every invoice, quote, bill, order and receipt. It is not cropped, so upload an image that already suits a wide, short box.</p>
      {hasLogo ? <img src={`/api/v1/organizations/${orgId}/logo?v=${logoRev}`} alt="Current company logo" width={180} height={60} style={{ objectFit: "contain", border: "1px solid #ccc" }} /> : <p className="notice">No logo uploaded. Documents print without one.</p>}
      {!canEditLogo && <p className="section-help">Only an owner or administrator can change the logo.</p>}
      {canEditLogo && <div className="form-grid">
        <div><label htmlFor="logo-file">Logo image (PNG or JPEG)</label><input id="logo-file" type="file" accept="image/png,image/jpeg" disabled={busy} onChange={e => { const f = e.target.files?.[0]; if (f) void uploadLogo(f); e.target.value = ""; }} /></div>
        {hasLogo && <div><button type="button" className="button-secondary" disabled={busy} onClick={() => { if (window.confirm("Remove the company logo? Documents will print without one.")) void act(async () => {
          const res = await fetch(`/api/v1/organizations/${orgId}/logo`, { method: "DELETE", credentials: "same-origin" });
          if (!res.ok) throw { error: "request_failed", status: res.status } as ApiError;
          setHasLogo(false); setNotice("Logo removed.");
        }); }}>Remove logo</button></div>}
      </div>}
    </section>
    {canWrite && <form className="record-form" onSubmit={create}><fieldset disabled={busy}><legend>New branding theme</legend><div className="form-grid">
      <div><label htmlFor="theme-name">Theme name</label><input id="theme-name" required maxLength={100} value={name} onChange={e => setName(e.target.value)} /></div>
      <div><label htmlFor="theme-footer">Footer text</label><textarea id="theme-footer" maxLength={500} rows={3} value={footer} onChange={e => setFooter(e.target.value)} /></div>
      <div><label><input type="checkbox" checked={isDefault} onChange={e => setIsDefault(e.target.checked)} /> Use as default theme</label></div>
    </div></fieldset><button disabled={busy} type="submit">Save theme</button></form>}
    {editing && <form className="record-form" onSubmit={saveEdit}><fieldset disabled={busy}><legend>Edit {editing.name}</legend><div className="form-grid">
      <div><label htmlFor="edit-theme-name">Theme name</label><input id="edit-theme-name" required maxLength={100} value={editing.name} onChange={e => setEditing({ ...editing, name: e.target.value })} /></div>
      <div><label htmlFor="edit-theme-footer">Footer text</label><textarea id="edit-theme-footer" maxLength={500} rows={3} value={editing.footer} onChange={e => setEditing({ ...editing, footer: e.target.value })} /></div>
    </div></fieldset><div className="form-actions"><button disabled={busy} type="submit">Save changes</button>{" "}<button type="button" className="button-secondary" disabled={busy} onClick={() => setEditing(null)}>Cancel</button></div></form>}
    {loaded && (themes.length ? <div className="table-scroll"><table className="doc-table"><caption>Branding themes</caption><thead><tr><th>Name</th><th>Footer</th><th>Default</th>{canWrite && <th>Actions</th>}</tr></thead><tbody>{themes.map(t => <tr key={t.id}><th scope="row">{t.name}</th><td>{t.footer}</td><td>{t.is_default ? "Default" : ""}</td>
      {canWrite && <td><button className="button-secondary" disabled={busy} onClick={() => setEditing(t)}>Edit</button>{" "}
        {!t.is_default && <button className="button-secondary" disabled={busy} onClick={() => void act(async () => { await api("/v1/branding-themes/default", { method: "POST", body: JSON.stringify({ org_id: orgId, id: t.id }) }); await load(orgId!); })}>Make default</button>}{" "}
        {!t.is_default && <button className="button-secondary" disabled={busy} onClick={() => { if (window.confirm(`Delete the theme ${t.name}? Documents keep the current default footer.`)) void act(async () => {
          await api(`/v1/branding-themes/${t.id}?org_id=${orgId}`, { method: "DELETE" }); setNotice("Theme deleted."); await load(orgId!);
        }); }}>Delete</button>}</td>}</tr>)}</tbody></table></div> : <p className="notice">No branding themes yet.</p>)}
  </>;
}
