"use client";
import { useEffect, useRef, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Field = { id: number; entity_kind: string; label: string; field_type: string; active: boolean };
type Person = { id: number; name: string; email: string; active: boolean };
type Term = { id: number; name: string; days: number; builtin: boolean; active: boolean };
type Tag = { id: number; name: string; options: string[]; active: boolean };
const KINDS: [string, string][] = [["contacts", "Customers and vendors"], ["invoices", "Invoices"], ["quotes", "Quotes"], ["bills", "Bills"], ["sales-receipts", "Sales receipts"], ["credit-notes", "Credit notes"], ["payments-received", "Payments received"], ["payments-made", "Payments made"], ["purchase-orders", "Purchase orders"], ["purchase-receives", "Purchase receives"], ["expenses", "Expenses"], ["items", "Items"], ["billing-plans", "Billing plans"], ["billing-addons", "Billing addons"]];

export default function ExtrasSettings() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function Workspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const [fields, setFields] = useState<Field[]>([]);
  const [tags, setTags] = useState<Tag[]>([]);
  const [people, setPeople] = useState<Person[]>([]);
  const [terms, setTerms] = useState<Term[]>([]);
  const [modes, setModes] = useState<{ id: number; label: string; builtin: boolean; active: boolean }[]>([]);
  const [pm, setPm] = useState("");
  const [rounding, setRounding] = useState("none");
  const [projects, setProjects] = useState<{ id: number; contact_id: number; name: string; active: boolean }[]>([]);
  const [pj, setPj] = useState({ contact_id: "", name: "" });
  const [customers, setCustomers] = useState<{ id: number; name: string; role: string }[]>([]);
  const [accts, setAccts] = useState<{ id: number; code: string; name: string; type: string }[]>([]);
  const [ctl, setCtl] = useState({ receivable: "", payable: "" });
  const [sp, setSp] = useState({ name: "", email: "" });
  const [tm, setTm] = useState({ name: "", days: "30" });
  const [fk, setFk] = useState({ entity_kind: "contacts", label: "", field_type: "text" });
  const [tg, setTg] = useState({ name: "", options: "" });
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function load() {
    if (orgId == null) return;
    const [f, t, p, m, pmr, ac, ca, pr, cu, rd] = await Promise.all([api<{ custom_fields: Field[] }>(`/v1/custom-fields?org_id=${orgId}`), api<{ reporting_tags: Tag[] }>(`/v1/reporting-tags?org_id=${orgId}`), api<{ salespeople: Person[] }>(`/v1/salespeople?org_id=${orgId}`), api<{ payment_terms: Term[] }>(`/v1/payment-terms?org_id=${orgId}`), api<{ payment_modes: { id: number; label: string; builtin: boolean; active: boolean }[] }>(`/v1/payment-modes?org_id=${orgId}`), api<{ accounts: { id: number; code: string; name: string; type: string }[] }>(`/v1/accounts?org_id=${orgId}`), api<{ receivable_account_id: number; payable_account_id: number }>(`/v1/control-accounts?org_id=${orgId}`), api<{ projects: { id: number; contact_id: number; name: string; active: boolean }[] }>(`/v1/projects?org_id=${orgId}`), api<{ contacts: { id: number; name: string; role: string }[] }>(`/v1/contacts?org_id=${orgId}`), api<{ rounding: string }>(`/v1/sales-rounding?org_id=${orgId}`)]);
    if (live.current) { setFields(f.custom_fields); setTags(t.reporting_tags); setPeople(p.salespeople); setTerms(m.payment_terms); setModes(pmr.payment_modes); setAccts(ac.accounts); setProjects(pr.projects); setRounding(rd.rounding); setCustomers(cu.contacts.filter(c => c.role !== "supplier")); setCtl({ receivable: ca.receivable_account_id ? String(ca.receivable_account_id) : "", payable: ca.payable_account_id ? String(ca.payable_account_id) : "" }); }
  }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  async function send(path: string, body: object) {
    if (busy || orgId == null) return false;
    setBusy(true); setError("");
    try { await api(path, { method: "POST", body: JSON.stringify({ org_id: orgId, ...body }) }); await load(); return true; }
    catch (e) { const x = e as ApiError; if (live.current) setError(messageFor(x.error, x.status)); return false; }
    finally { if (live.current) setBusy(false); }
  }
  async function addField(e: FormEvent) { e.preventDefault(); if (await send("/v1/custom-fields", fk)) setFk({ ...fk, label: "" }); }
  async function addTag(e: FormEvent) { e.preventDefault(); if (await send("/v1/reporting-tags", { name: tg.name, options: tg.options.split(",").map(s => s.trim()).filter(Boolean) })) setTg({ name: "", options: "" }); }
  async function addPerson(e: FormEvent) { e.preventDefault(); if (await send("/v1/salespeople", sp)) setSp({ name: "", email: "" }); }
  async function addTerm(e: FormEvent) { e.preventDefault(); const days = Number(tm.days); if (!Number.isInteger(days) || days < 0 || days > 365) { setError("Days must be a whole number from 0 to 365."); return; } if (await send("/v1/payment-terms", { name: tm.name, days })) setTm({ name: "", days: "30" }); }
  async function addProject(e: FormEvent) { e.preventDefault(); if (await send("/v1/projects", { contact_id: Number(pj.contact_id), name: pj.name })) setPj({ ...pj, name: "" }); }
  async function saveRounding(e: FormEvent) {
    e.preventDefault();
    if (busy || orgId == null) return;
    setBusy(true); setError("");
    try { await api("/v1/sales-rounding", { method: "PUT", body: JSON.stringify({ org_id: orgId, rounding }) }); await load(); }
    catch (x) { const err = x as ApiError; if (live.current) setError(messageFor(err.error, err.status)); }
    finally { if (live.current) setBusy(false); }
  }
  async function addMode(e: FormEvent) { e.preventDefault(); if (await send("/v1/payment-modes", { name: pm })) setPm(""); }
  async function saveControl(e: FormEvent) {
    e.preventDefault();
    if (busy || orgId == null || !ctl.receivable || !ctl.payable) return;
    setBusy(true); setError("");
    try { await api("/v1/control-accounts", { method: "PUT", body: JSON.stringify({ org_id: orgId, receivable_account_id: Number(ctl.receivable), payable_account_id: Number(ctl.payable) }) }); await load(); }
    catch (x) { const err = x as ApiError; if (live.current) setError(messageFor(err.error, err.status)); }
    finally { if (live.current) setBusy(false); }
  }
  const kindName = (k: string) => KINDS.find(x => x[0] === k)?.[1] ?? k;

  return <>
    <PageHeader title="Custom fields, tags and lists" description="Extra fields, reporting tags, salespeople, payment terms and payment modes." />
    {!selected && <p>Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}
    <p className="notice">Fields and tags are filled in from each saved record&apos;s details, under Custom fields and tags. Definitions can be switched off but not deleted, so old values stay readable. Reports do not group by tags yet.</p>
    <h2>Custom fields</h2>
    {canWrite && <form className="record-form" onSubmit={addField}><fieldset disabled={busy}><legend>New custom field</legend><div className="form-grid">
      <div><label htmlFor="cf-kind">Applies to</label><select id="cf-kind" value={fk.entity_kind} onChange={e => setFk({ ...fk, entity_kind: e.target.value })}>{KINDS.map(([k, n]) => <option key={k} value={k}>{n}</option>)}</select></div>
      <div><label htmlFor="cf-label">Label</label><input id="cf-label" required maxLength={100} value={fk.label} onChange={e => setFk({ ...fk, label: e.target.value })} /></div>
      <div><label htmlFor="cf-type">Type</label><select id="cf-type" value={fk.field_type} onChange={e => setFk({ ...fk, field_type: e.target.value })}><option value="text">Text</option><option value="number">Number</option><option value="date">Date</option><option value="yesno">Yes / No</option></select></div>
    </div><button>Add field</button><button type="button" className="button-secondary" onClick={() => setFk({ ...fk, label: "" })}>Cancel</button></fieldset></form>}
    {fields.length === 0 ? <p>No custom fields yet.</p> : <div className="table-scroll"><table className="doc-table"><caption>Custom fields</caption><thead><tr><th>Applies to</th><th>Label</th><th>Type</th><th>Status</th><th>Action</th></tr></thead><tbody>{fields.map(f => <tr key={f.id}><td>{kindName(f.entity_kind)}</td><td>{f.label}</td><td>{f.field_type}</td><td>{f.active ? "Active" : "Off"}</td><td>{canWrite && <button className="button-secondary" disabled={busy} onClick={() => void send(`/v1/custom-fields/${f.id}/active`, { active: !f.active })}>{f.active ? "Switch off" : "Switch on"}<span className="sr-only"> {f.label}</span></button>}</td></tr>)}</tbody></table></div>}
    <h2>Reporting tags</h2>
    {canWrite && <form className="record-form" onSubmit={addTag}><fieldset disabled={busy}><legend>New reporting tag</legend><div className="form-grid">
      <div><label htmlFor="rt-name">Tag name</label><input id="rt-name" required maxLength={100} value={tg.name} onChange={e => setTg({ ...tg, name: e.target.value })} /></div>
      <div><label htmlFor="rt-options">Options (comma separated)</label><input id="rt-options" required value={tg.options} onChange={e => setTg({ ...tg, options: e.target.value })} /></div>
    </div><button>Add tag</button><button type="button" className="button-secondary" onClick={() => setTg({ name: "", options: "" })}>Cancel</button></fieldset></form>}
    {tags.length === 0 ? <p>No reporting tags yet.</p> : <div className="table-scroll"><table className="doc-table"><caption>Reporting tags</caption><thead><tr><th>Tag</th><th>Options</th><th>Status</th><th>Action</th></tr></thead><tbody>{tags.map(t => <tr key={t.id}><td>{t.name}</td><td>{t.options.join(", ")}</td><td>{t.active ? "Active" : "Off"}</td><td>{canWrite && <button className="button-secondary" disabled={busy} onClick={() => void send(`/v1/reporting-tags/${t.id}/active`, { active: !t.active })}>{t.active ? "Switch off" : "Switch on"}<span className="sr-only"> {t.name}</span></button>}</td></tr>)}</tbody></table></div>}
    <h2>Salespeople</h2>
    {canWrite && <form className="record-form" onSubmit={addPerson}><fieldset disabled={busy}><legend>New salesperson</legend><div className="form-grid">
      <div><label htmlFor="sp-name">Name</label><input id="sp-name" required maxLength={200} value={sp.name} onChange={e => setSp({ ...sp, name: e.target.value })} /></div>
      <div><label htmlFor="sp-email">Email (optional)</label><input id="sp-email" type="email" maxLength={254} value={sp.email} onChange={e => setSp({ ...sp, email: e.target.value })} /></div>
    </div><button>Add salesperson</button><button type="button" className="button-secondary" onClick={() => setSp({ name: "", email: "" })}>Cancel</button></fieldset></form>}
    {people.length === 0 ? <p>No salespeople yet. Quotes, invoices, receipts and credit notes pick from this list.</p> : <div className="table-scroll"><table className="doc-table"><caption>Salespeople</caption><thead><tr><th>Name</th><th>Email</th><th>Status</th><th>Action</th></tr></thead><tbody>{people.map(p => <tr key={p.id}><td>{p.name}</td><td>{p.email || "—"}</td><td>{p.active ? "Active" : "Off"}</td><td>{canWrite && <button className="button-secondary" disabled={busy} onClick={() => void send(`/v1/salespeople/${p.id}/active`, { active: !p.active })}>{p.active ? "Switch off" : "Switch on"}<span className="sr-only"> {p.name}</span></button>}</td></tr>)}</tbody></table></div>}
    <h2>Payment terms</h2>
    {canWrite && <form className="record-form" onSubmit={addTerm}><fieldset disabled={busy}><legend>New payment term</legend><div className="form-grid">
      <div><label htmlFor="pt-name">Name</label><input id="pt-name" required maxLength={100} value={tm.name} onChange={e => setTm({ ...tm, name: e.target.value })} /></div>
      <div><label htmlFor="pt-days">Days until due</label><input id="pt-days" required inputMode="numeric" value={tm.days} onChange={e => setTm({ ...tm, days: e.target.value })} /></div>
    </div><button>Add term</button><button type="button" className="button-secondary" onClick={() => setTm({ name: "", days: "30" })}>Cancel</button></fieldset></form>}
    <div className="table-scroll"><table className="doc-table"><caption>Payment terms</caption><thead><tr><th>Name</th><th>Days</th><th>Status</th><th>Action</th></tr></thead><tbody>{terms.map(t => <tr key={`${t.id}-${t.name}`}><td>{t.name}</td><td>{t.days}</td><td>{t.builtin ? "Built in" : t.active ? "Active" : "Off"}</td><td>{canWrite && !t.builtin && <button className="button-secondary" disabled={busy} onClick={() => void send(`/v1/payment-terms/${t.id}/active`, { active: !t.active })}>{t.active ? "Switch off" : "Switch on"}<span className="sr-only"> {t.name}</span></button>}</td></tr>)}</tbody></table></div>
    <h2>Payment modes</h2>
    {canWrite && <form className="record-form" onSubmit={addMode}><fieldset disabled={busy}><legend>New payment mode</legend>
      <label htmlFor="pm-name">Name</label><input id="pm-name" required maxLength={50} value={pm} onChange={e => setPm(e.target.value)} /><button>Add mode</button></fieldset></form>}
    <div className="table-scroll"><table className="doc-table"><caption>Payment modes</caption><thead><tr><th>Name</th><th>Status</th><th>Action</th></tr></thead><tbody>{modes.map(m => <tr key={`${m.id}-${m.label}`}><td>{m.label}</td><td>{m.builtin ? "Built in" : m.active ? "Active" : "Off"}</td><td>{canWrite && !m.builtin && <button className="button-secondary" disabled={busy} onClick={() => void send(`/v1/payment-modes/${m.id}/active`, { active: !m.active })}>{m.active ? "Switch off" : "Switch on"}<span className="sr-only"> {m.label}</span></button>}</td></tr>)}</tbody></table></div>
    <h2>Projects</h2>
    {canWrite && <form className="record-form" onSubmit={addProject}><fieldset disabled={busy}><legend>New project</legend><div className="form-grid">
      <div><label htmlFor="pj-customer">Customer</label><select id="pj-customer" required value={pj.contact_id} onChange={e => setPj({ ...pj, contact_id: e.target.value })}><option value="">Choose customer</option>{customers.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></div>
      <div><label htmlFor="pj-name">Project name</label><input id="pj-name" required maxLength={200} value={pj.name} onChange={e => setPj({ ...pj, name: e.target.value })} /></div>
    </div><button>Add project</button><button type="button" className="button-secondary" onClick={() => setPj({ ...pj, name: "" })}>Cancel</button></fieldset></form>}
    <div className="table-scroll"><table className="doc-table"><caption>Projects</caption><thead><tr><th>Project</th><th>Customer</th><th>Status</th><th>Action</th></tr></thead><tbody>{projects.map(p => <tr key={p.id}><td>{p.name}</td><td>{customers.find(c => c.id === p.contact_id)?.name ?? p.contact_id}</td><td>{p.active ? "Active" : "Off"}</td><td>{canWrite && <button className="button-secondary" disabled={busy} onClick={() => void send(`/v1/projects/${p.id}/active`, { active: !p.active })}>{p.active ? "Switch off" : "Switch on"}<span className="sr-only"> {p.name}</span></button>}</td></tr>)}</tbody></table></div>
    <h2>Rounding on sales documents</h2>
    {canWrite && <form className="record-form" onSubmit={saveRounding}><fieldset disabled={busy}><legend>Rounding rule</legend>
      <label htmlFor="rounding">Round totals to</label><select id="rounding" value={rounding} onChange={e => setRounding(e.target.value)}><option value="none">No rounding</option><option value="whole">Nearest whole dollar</option><option value="5c">Nearest 5 cents</option><option value="10c">Nearest 10 cents</option></select>
      <p className="section-help">Invoice and quote forms then offer a Round total button that puts the difference in the adjustment line. It is never applied silently, and saved documents do not change.</p><button>Save rule</button></fieldset></form>}
    <h2>Receivable and payable accounts</h2>
    {canWrite && <form className="record-form" onSubmit={saveControl}><fieldset disabled={busy}><legend>Accounts that hold customer and supplier balances</legend><div className="form-grid">
      <div><label htmlFor="ctl-ar">Accounts receivable</label><select id="ctl-ar" required value={ctl.receivable} onChange={e => setCtl({ ...ctl, receivable: e.target.value })}><option value="">Choose account</option>{accts.filter(a => a.type === "asset").map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select></div>
      <div><label htmlFor="ctl-ap">Accounts payable</label><select id="ctl-ap" required value={ctl.payable} onChange={e => setCtl({ ...ctl, payable: e.target.value })}><option value="">Choose account</option>{accts.filter(a => a.type === "liability").map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select></div>
    </div><p className="section-help">Invoices, bills, payments and credit notes post to these. You can switch only while the account now in use nets to zero, so old and new postings never split. Create the accounts first on the Accounting page.</p><button>Save accounts</button></fieldset></form>}
  </>;
}
