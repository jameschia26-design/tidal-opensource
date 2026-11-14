"use client";
import Link from "next/link";
import { useEffect, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import Tabs from "@/components/Tabs";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

const TABS = [{ label: "Leads", href: "/crm" }, { label: "Deals", href: "/crm/deals" }];
const NEXT: Record<string, string[]> = { new: ["contacted", "qualified", "lost"], contacted: ["qualified", "lost"], qualified: ["lost"], lost: [] };
const blank = () => ({ name: "", email: "", source: "", company_name: "", phone: "", job_title: "", website: "", notes: "" });
type Lead = ReturnType<typeof blank> & { id: number; stage: string; contact_id: number | null };
type Activity = { id: number; subject: string; kind: string; due_date: string; notes: string; completed: boolean };

export default function LeadsPage() {
  const { selected } = useOrg();
  return <LeadsWorkspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function LeadsWorkspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const [rows, setRows] = useState<Lead[] | null>(null);
  const [form, setForm] = useState(blank);
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<number | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [query, setQuery] = useState("");
  const [detail, setDetail] = useState<Lead | null>(null);
  const [activities, setActivities] = useState<Activity[]>([]);
  const [activity, setActivity] = useState({ kind: "task", subject: "", due_date: "", notes: "" });
  const [customers, setCustomers] = useState<{ id: number; name: string }[]>([]);
  const [linkId, setLinkId] = useState("");
  async function load() {
    if (orgId == null) return;
    const result = await api<{ leads: Lead[] }>(`/v1/leads?org_id=${orgId}`);
    setRows(result.leads);
    setDetail(prev => prev ? result.leads.find(l => l.id === prev.id) ?? null : null);
  }
  useEffect(() => {
    if (orgId == null) return;
    let live = true;
    api<{ leads: Lead[] }>(`/v1/leads?org_id=${orgId}`).then(r => { if (live) setRows(r.leads); }).catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId]);
  useEffect(() => {
    setActivities([]); setCustomers([]); setLinkId(""); setActivity({ kind: "task", subject: "", due_date: "", notes: "" });
    if (!detail || orgId == null) return;
    let live = true;
    Promise.all([api<{ activities: Activity[] }>(`/v1/activities?org_id=${orgId}&lead_id=${detail.id}`), api<{ contacts: { id: number; name: string }[] }>(`/v1/contacts?org_id=${orgId}&role=customer`), api<{ contacts: { id: number; name: string }[] }>(`/v1/contacts?org_id=${orgId}&role=supplier`)])
      .then(([a, c, s]) => { if (live) { setActivities(a.activities); setCustomers([...new Map([...c.contacts, ...s.contacts].map(v => [v.id, v])).values()].sort((a, b) => a.name.localeCompare(b.name))); } })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [detail?.id, orgId]);
  async function action(work: () => Promise<void>) {
    if (busy || orgId == null) return;
    setBusy(true); setError(""); setNotice("");
    try { await work(); } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); }
  }
  async function add(e: FormEvent) {
    e.preventDefault();
    await action(async () => { await api(editing === null ? "/v1/leads" : `/v1/leads/${editing}`, { method: editing === null ? "POST" : "PUT", body: JSON.stringify({ ...form, org_id: orgId }) }); setForm(blank()); setOpen(false); setEditing(null); setNotice("Lead saved. Linked customer details are managed separately under Customers."); await load(); });
  }
  const field = (key: keyof ReturnType<typeof blank>, label: string, maxLength: number, type = "text") => <div><label htmlFor={`lead-${key}`}>{label}{key !== "name" && <span className="field-optional">Optional</span>}</label><input id={`lead-${key}`} required={key === "name"} type={type} maxLength={maxLength} value={form[key]} onChange={e => setForm({ ...form, [key]: e.target.value })} /></div>;
  const filtered = rows?.filter(l => `${l.name} ${l.email} ${l.company_name}`.toLowerCase().includes(query.toLowerCase()));
  return <>
    <PageHeader title="CRM" description="Track leads, follow-ups and the customer relationship in one place." /><Tabs items={TABS} current="/crm" />
    {!selected && <p className="notice">Choose a company first.</p>}{error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {selected && <><div className="record-toolbar"><div><label htmlFor="lead-search">Search leads</label><input id="lead-search" type="search" value={query} onChange={e => setQuery(e.target.value)} /></div>{canWrite && <button disabled={busy || open} onClick={() => { setEditing(null); setForm(blank()); setOpen(true); setDetail(null); }}>New lead</button>}</div>
      {open && canWrite && <form className="record-form" onSubmit={add}><h2>{editing === null ? "New lead" : "Edit lead"}</h2><fieldset disabled={busy}><legend>Person and business</legend><div className="form-grid">{field("name", "Contact name", 200)}{field("company_name", "Company name", 200)}{field("job_title", "Job title", 100)}{field("source", "Lead source", 100)}</div></fieldset><fieldset disabled={busy}><legend>Contact details</legend><div className="form-grid">{field("email", "Email", 254, "email")}{field("phone", "Phone", 40, "tel")}{field("website", "Website (https://…)", 300, "url")}</div></fieldset><fieldset disabled={busy}><legend>Background notes</legend><label htmlFor="lead-notes">Notes</label><textarea id="lead-notes" maxLength={4000} rows={4} value={form.notes} onChange={e => setForm({ ...form, notes: e.target.value })} /></fieldset><div className="form-actions"><button disabled={busy}>Save lead</button><button disabled={busy} type="button" className="button-secondary" onClick={() => setOpen(false)}>Cancel</button></div></form>}
      {detail && <section className="record-form"><h2>{detail.name}</h2>{canWrite && <button className="button-secondary" disabled={busy} onClick={() => { setEditing(detail.id); setForm(Object.fromEntries(Object.keys(blank()).map(k => [k, detail[k as keyof ReturnType<typeof blank>]])) as ReturnType<typeof blank>); setOpen(true); setDetail(null); }}>Edit lead</button>}<dl className="detail-grid">{Object.entries({ Company: detail.company_name, "Job title": detail.job_title, Email: detail.email, Phone: detail.phone, Website: detail.website, Source: detail.source, Notes: detail.notes }).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value || "Not provided"}</dd></div>)}</dl>
        {detail.contact_id ? <p className="notice">Linked to customer {detail.contact_id}. <Link href="/sales/customers">Open customers</Link></p> : canWrite && detail.stage === "qualified" && <fieldset disabled={busy}><legend>Convert to customer</legend><p className="section-help">Link an existing customer or supplier, or create a customer from this lead. Repeating conversion never creates a second customer.</p><label htmlFor="lead-customer-link">Customer identity</label><select id="lead-customer-link" value={linkId} onChange={e => setLinkId(e.target.value)}><option value="">Create new customer</option>{customers.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select><button onClick={() => action(async () => { await api(`/v1/leads/${detail.id}/convert`, { method: "POST", body: JSON.stringify({ org_id: orgId, contact_id: Number(linkId) }) }); setNotice("Lead linked to customer."); await load(); })}>Convert lead</button></fieldset>}
        <h2>Activities and follow-ups</h2>{canWrite && <form onSubmit={e => { e.preventDefault(); action(async () => { await api("/v1/activities", { method: "POST", body: JSON.stringify({ ...activity, org_id: orgId, lead_id: detail.id }) }); const result = await api<{ activities: Activity[] }>(`/v1/activities?org_id=${orgId}&lead_id=${detail.id}`); setActivities(result.activities); setActivity({ kind: "task", subject: "", due_date: "", notes: "" }); setNotice("Activity saved."); }); }}><fieldset disabled={busy}><legend>New activity</legend><div className="form-grid"><div><label htmlFor="activity-kind">Type</label><select id="activity-kind" value={activity.kind} onChange={e => setActivity({ ...activity, kind: e.target.value })}>{["task", "call", "meeting", "email", "note"].map(k => <option key={k}>{k}</option>)}</select></div><div><label htmlFor="activity-subject">Subject</label><input id="activity-subject" required maxLength={200} value={activity.subject} onChange={e => setActivity({ ...activity, subject: e.target.value })} /></div><div><label htmlFor="activity-due">Due date (optional)</label><DateInput id="activity-due" value={activity.due_date} onChange={e => setActivity({ ...activity, due_date: e.target.value })}/></div></div><label htmlFor="activity-notes">Notes (optional)</label><textarea id="activity-notes" maxLength={4000} rows={3} value={activity.notes} onChange={e => setActivity({ ...activity, notes: e.target.value })} /><button disabled={busy}>Save activity</button></fieldset></form>}
        {!activities.length && <p>No activities yet.</p>}<ul className="task-list">{activities.map(a => <li key={a.id}><strong>{a.subject}</strong> · {a.kind} · {a.completed ? "Completed" : a.due_date || "No due date"}{a.notes && <p>{a.notes}</p>}{canWrite && !a.completed && <button disabled={busy} className="button-secondary" onClick={() => action(async () => { await api(`/v1/activities/${a.id}/complete`, { method: "POST", body: JSON.stringify({ org_id: orgId }) }); setActivities(prev => prev.map(v => v.id === a.id ? { ...v, completed: true } : v)); })}>Mark complete</button>}</li>)}</ul><button className="button-secondary" disabled={busy} onClick={() => setDetail(null)}>Close lead</button>
      </section>}
      {rows === null && !error && <p role="status">Loading leads…</p>}{filtered?.length === 0 && <p className="notice">No matching leads.</p>}{!!filtered?.length && <div className="table-scroll"><table className="doc-table"><caption>Latest 200 leads</caption><thead><tr><th>Name</th><th>Company</th><th>Source</th><th>Stage</th><th>Actions</th></tr></thead><tbody>{filtered.map(l => <tr key={l.id}><th scope="row">{l.name}</th><td>{l.company_name || "—"}</td><td>{l.source || "—"}</td><td>{l.contact_id ? "Customer linked" : l.stage}</td><td><button disabled={busy} className="button-secondary" onClick={() => { setDetail(l); setOpen(false); }}>View</button>{canWrite && !l.contact_id && !!NEXT[l.stage]?.length && <select aria-label={`Move ${l.name}`} value="" disabled={busy} onChange={e => { const stage = e.target.value; if (stage) action(async () => { await api(`/v1/leads/${l.id}/stage`, { method: "POST", body: JSON.stringify({ org_id: orgId, stage }) }); await load(); }); }}><option value="">Move stage…</option>{NEXT[l.stage].map(s => <option key={s}>{s}</option>)}</select>}</td></tr>)}</tbody></table></div>}
    </>}
  </>;
}
