"use client";
import { useEffect, useRef, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import Tabs from "@/components/Tabs";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { parseCents, formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

const TABS = [{ label: "Leads", href: "/crm" }, { label: "Deals", href: "/crm/deals" }];
const blank = () => ({ title: "", amount: "", lead_id: "", contact_id: "", expected_close_date: "", probability: "0", deal_type: "", source: "", next_step: "", notes: "" });
type Deal = { id: number; lead_id: number | null; contact_id: number | null; title: string; amount_cents: number; stage: string; expected_close_date: string; probability: number; deal_type: string; source: string; next_step: string; notes: string };
type Lead = { id: number; name: string; contact_id: number | null };
type Contact = { id: number; name: string };

export default function DealsPage() {
  const { selected } = useOrg();
  return <DealsWorkspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function DealsWorkspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const [rows, setRows] = useState<Deal[] | null>(null);
  const [leads, setLeads] = useState<Lead[]>([]);
  const [customers, setCustomers] = useState<Contact[]>([]);
  const [form, setForm] = useState(blank);
  const [editing, setEditing] = useState<number | null>(null);
  const [open, setOpen] = useState(false);
  const [detail, setDetail] = useState<Deal | null>(null);
  const [confirmStage, setConfirmStage] = useState("");
  const [query, setQuery] = useState("");
  const [stage, setStage] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  async function load() {
    if (orgId == null) return;
    const [d, l, c] = await Promise.all([
      api<{ deals: Deal[] }>(`/v1/deals?org_id=${orgId}`),
      api<{ leads: Lead[] }>(`/v1/leads?org_id=${orgId}`),
      api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${orgId}&role=customer`),
    ]);
    if (!live.current) return;
    setRows(d.deals); setLeads(l.leads); setCustomers(c.contacts);
    setDetail(prev => prev ? d.deals.find(v => v.id === prev.id) ?? null : null);
  }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // Company-keyed workspace prevents records and unfinished forms crossing companies.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  async function action(work: () => Promise<void>) {
    if (busy || !canWrite || orgId == null) return;
    setBusy(true); setError(""); setNotice("");
    try { await work(); } catch (e) { if (live.current) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } }
    finally { if (live.current) setBusy(false); }
  }
  async function save(e: FormEvent) {
    e.preventDefault();
    const cents = parseCents(form.amount);
    const probability = Number(form.probability);
    if (!form.title.trim() || cents === null || cents > 100000000000000 || !form.probability.trim() || !Number.isInteger(probability) || probability < 0 || probability > 100) { setError("Enter a title, valid amount and probability from 0 to 100."); return; }
    const { amount: _amount, ...fields } = form;
    await action(async () => {
      await api(editing === null ? "/v1/deals" : `/v1/deals/${editing}`, { method: editing === null ? "POST" : "PUT", body: JSON.stringify({ ...fields, lead_id: Number(form.lead_id), contact_id: form.contact_id ? Number(form.contact_id) : null, probability, amount_cents: cents, org_id: orgId }) });
      if (!live.current) return;
      setOpen(false); setEditing(null); setForm(blank()); setNotice("Deal saved. No invoice or income was recorded."); await load();
    });
  }
  function edit(d: Deal) {
    setForm({ title: d.title, amount: (d.amount_cents / 100).toFixed(2), lead_id: d.lead_id?.toString() ?? "", contact_id: d.contact_id?.toString() ?? "", expected_close_date: d.expected_close_date, probability: d.probability.toString(), deal_type: d.deal_type, source: d.source, next_step: d.next_step, notes: d.notes });
    setEditing(d.id); setOpen(true); setDetail(null); setConfirmStage("");
  }
  const field = (key: "title" | "deal_type" | "source" | "next_step", label: string, maxLength: number) => <div><label htmlFor={`deal-${key}`}>{label}</label><input id={`deal-${key}`} required={key === "title"} maxLength={maxLength} value={form[key]} onChange={e => setForm({ ...form, [key]: e.target.value })} /></div>;
  const leadName = (id: number | null) => id ? leads.find(l => l.id === id)?.name ?? `Lead ${id}` : "Not linked";
  const customerName = (id: number | null) => id ? customers.find(c => c.id === id)?.name ?? `Customer ${id}` : "Not linked";
  const filtered = rows?.filter(d => (!stage || d.stage === stage) && `${d.title} ${leadName(d.lead_id)} ${customerName(d.contact_id)} ${d.source}`.toLowerCase().includes(query.toLowerCase()));
  return <>
    <PageHeader title="CRM" description="Track sales opportunities, expected closing dates and next actions." /><Tabs items={TABS} current="/crm/deals" />
    <p className="notice">Deal values are forecasts, not invoices. Won and lost are final; review details before closing.</p>
    {!selected && <p>Choose a company first.</p>}{error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {selected && <>
      <div className="record-toolbar"><div><label htmlFor="deal-search">Search deals</label><input id="deal-search" type="search" value={query} onChange={e => setQuery(e.target.value)} /></div><div><label htmlFor="deal-stage">Stage</label><select id="deal-stage" value={stage} onChange={e => setStage(e.target.value)}><option value="">All stages</option>{["open", "won", "lost"].map(s => <option key={s}>{s}</option>)}</select></div>{canWrite && <button disabled={busy || open} onClick={() => { setEditing(null); setForm(blank()); setOpen(true); setDetail(null); setConfirmStage(""); }}>New deal</button>}</div>
      {open && canWrite && <form className="record-form" onSubmit={save}>
        <h2>{editing === null ? "New deal" : "Edit deal"}</h2>
        <fieldset disabled={busy}><legend>Opportunity and customer</legend><div className="form-grid">
          {field("title", "Deal title", 200)}
          <div><label htmlFor="deal-lead">Lead (optional)</label><select id="deal-lead" value={form.lead_id} onChange={e => { const id = e.target.value; const linked = leads.find(l => l.id === Number(id))?.contact_id; setForm({ ...form, lead_id: id, contact_id: linked ? String(linked) : form.contact_id }); }}><option value="">Not linked</option>{form.lead_id && !leads.some(l => l.id === Number(form.lead_id)) && <option value={form.lead_id}>Lead {form.lead_id}</option>}{leads.map(l => <option key={l.id} value={l.id}>{l.name}</option>)}</select></div>
          <div><label htmlFor="deal-customer">Customer (optional)</label><select id="deal-customer" value={form.contact_id} onChange={e => setForm({ ...form, contact_id: e.target.value })}><option value="">Not linked</option>{form.contact_id && !customers.some(c => c.id === Number(form.contact_id)) && <option value={form.contact_id}>Customer {form.contact_id}</option>}{customers.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></div>
          {field("deal_type", "Deal type (optional)", 100)}{field("source", "Source (optional)", 100)}
        </div></fieldset>
        <fieldset disabled={busy}><legend>Value and closing forecast</legend><div className="form-grid">
          <div><label htmlFor="deal-amount">Amount (SGD)</label><input id="deal-amount" required inputMode="decimal" value={form.amount} onChange={e => setForm({ ...form, amount: e.target.value })} /></div>
          <div><label htmlFor="deal-close">Expected closing date (optional)</label><DateInput id="deal-close" value={form.expected_close_date} onChange={e => setForm({ ...form, expected_close_date: e.target.value })}/></div>
          <div><label htmlFor="deal-probability">Probability (%)</label><input id="deal-probability" type="number" min={0} max={100} step={1} required value={form.probability} onChange={e => setForm({ ...form, probability: e.target.value })} /></div>
        </div></fieldset>
        <fieldset disabled={busy}><legend>Follow-up and background</legend>{field("next_step", "Next step (optional)", 1000)}<label htmlFor="deal-notes">Notes (optional)</label><textarea id="deal-notes" rows={4} maxLength={4000} value={form.notes} onChange={e => setForm({ ...form, notes: e.target.value })} /></fieldset>
        <div className="form-actions"><button disabled={busy}>Save deal</button><button type="button" disabled={busy} className="button-secondary" onClick={() => setOpen(false)}>Cancel</button></div>
      </form>}
      {detail && <section className="record-form"><h2>{detail.title}</h2><dl className="detail-grid">{Object.entries({ Lead: leadName(detail.lead_id), Customer: customerName(detail.contact_id), Amount: formatCents(detail.amount_cents), Stage: detail.stage, "Expected closing": detail.expected_close_date || "Not set", Probability: `${detail.probability}%`, Type: detail.deal_type, Source: detail.source, "Next step": detail.next_step, Notes: detail.notes }).map(([label, value]) => <div key={label}><dt>{label}</dt><dd className="long-text">{value || "Not provided"}</dd></div>)}</dl>
        {canWrite && detail.stage === "open" && <div className="form-actions"><button disabled={busy} onClick={() => edit(detail)}>Edit deal</button><button disabled={busy} className="button-secondary" onClick={() => setConfirmStage("won")}>Mark won</button><button disabled={busy} className="button-secondary" onClick={() => setConfirmStage("lost")}>Mark lost</button></div>}
        {confirmStage && canWrite && <div role="group" aria-label="Confirm deal closure"><p>Mark this deal {confirmStage}? This is final and prevents further editing. No invoice or income will be created.</p><button disabled={busy} onClick={() => action(async () => { await api(`/v1/deals/${detail.id}/stage`, { method: "POST", body: JSON.stringify({ org_id: orgId, stage: confirmStage }) }); if (!live.current) return; setConfirmStage(""); setNotice("Deal closed."); await load(); })}>Confirm {confirmStage}</button><button className="button-secondary" disabled={busy} onClick={() => setConfirmStage("")}>Cancel</button></div>}
        <button className="button-secondary" disabled={busy} onClick={() => { setDetail(null); setConfirmStage(""); }}>Close details</button>
      </section>}
      {rows === null && !error && <p role="status">Loading deals…</p>}{filtered?.length === 0 && <p>No matching deals.</p>}
      {!!filtered?.length && <div className="table-scroll"><table className="doc-table"><caption>Latest 200 deals</caption><thead><tr><th>Title</th><th>Customer / lead</th><th className="num">Amount</th><th>Expected closing</th><th>Probability</th><th>Stage</th><th>Actions</th></tr></thead><tbody>{filtered.map(d => <tr key={d.id}><th scope="row">{d.title}</th><td>{d.contact_id ? customerName(d.contact_id) : leadName(d.lead_id)}</td><td className="num">{formatCents(d.amount_cents)}</td><td>{d.expected_close_date || "—"}</td><td>{d.probability}%</td><td>{d.stage}</td><td><button className="button-secondary" disabled={busy || open} onClick={() => { setDetail(d); setConfirmStage(""); }}>View</button></td></tr>)}</tbody></table></div>}
    </>}
  </>;
}
