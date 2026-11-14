"use client";
import ExtrasForm, { noExtras, saveExtras } from "@/components/ExtrasForm";
import { TermsSelect } from "@/components/ListSelects";
import { useEffect, useRef, useState, type FormEvent } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import PageHeader from "@/components/PageHeader";
import Attachments from "@/components/Attachments";
import ExtraAddresses from "@/components/ExtraAddresses";
import Extras from "@/components/Extras";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { parseCents, formatCents } from "@/lib/money";
import DateInput from "@/components/DateInput";

type Address = { attention: string; line1: string; line2: string; city: string; region: string; postal_code: string; country: string; phone: string; fax: string };
const address = (): Address => ({ attention: "", line1: "", line2: "", city: "Singapore", region: "", postal_code: "", country: "SG", phone: "", fax: "" });
type Person = { salutation: string; first_name: string; last_name: string; email: string; work_phone: string; mobile: string };
const person = (): Person => ({ salutation: "", first_name: "", last_name: "", email: "", work_phone: "", mobile: "" });
// Phone defaults to Singapore's +65 calling code, as in the map's New Customer form.
const blank = () => ({ name: "", email: "", discount_basis_points: 0, bill_payment_terms_days: 0, contact_type: "business", company_name: "", first_name: "", last_name: "", salutation: "", phone: "+65 ", mobile: "", website: "", uen: "", gst_number: "", account_number: "", payment_terms_days: 0, language: "en", currency: "SGD", portal_enabled: false, owner_user_id: 0, opening_balance_cents: 0, opening_balance_date: "", internal_remarks: "", sales_treatment: "", sales_tax_code: "", purchase_treatment: "", purchase_tax_code: "", fax: "", direct_dial: "", country: "", bank_account_name: "", bank_account_number: "", bank_particulars: "", sales_account_id: 0, purchase_account_id: 0, credit_limit_cents: 0, block_over_limit: false, billing_address: address(), shipping_address: address(), notes: "", contact_persons: [] as Person[] });
type Contact = ReturnType<typeof blank> & { id: number; role: string; archived: boolean; they_owe_cents: number; you_owe_cents: number };
type Group = { id: number; name: string; contact_ids: number[] };
type SmartList = { id: number; name: string; query: string };
type Activity = { invoices: { id: number; document_number: string; reference: string; date: string; due_date: string; paid_cents: number; paid_date: string; sent_date: string; lifecycle_status: string }[]; bills: { id: number; document_number: string; reference: string; date: string; due_date: string; paid_cents: number; paid_date: string; sent_date: string; lifecycle_status: string }[] };

export default function ContactsPage({ role }: { role: "customer" | "supplier" }) {
  const { selected } = useOrg();
  return <ContactsWorkspace key={`${selected?.id ?? "none"}-${role}`} role={role} />;
}

function ContactsWorkspace({ role }: { role: "customer" | "supplier" }) {
  const router = useRouter();
  const subscribeAfter = useRef(false);
  const { selected, error: orgError } = useOrg();
  const orgId = selected?.id;
  const activeCompany = useRef(orgId);
  useEffect(() => { activeCompany.current = orgId; }, [orgId]);
  const [rows, setRows] = useState<Contact[] | null>(null);
  const [form, setForm] = useState(blank);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [extras, setExtras] = useState(noExtras);
  const [open, setOpen] = useState(false);
  const [sameAddress, setSameAddress] = useState(true);
  const [query, setQuery] = useState("");
  const [archivedView, setArchivedView] = useState(false);
  const [groups, setGroups] = useState<Group[]>([]);
  const [selectedContacts, setSelectedContacts] = useState<number[]>([]);
  const [hiddenCols, setHiddenCols] = useState<string[]>([]);
  const [bulkGroup, setBulkGroup] = useState("");
  const [smartLists, setSmartLists] = useState<SmartList[]>([]);
  const [newGroup, setNewGroup] = useState("");
  const [newList, setNewList] = useState("");
  const [tab, setTab] = useState<"activity" | "details" | "financial" | "documents">("activity");
  const [activity, setActivity] = useState<Activity | null>(null);
  const [detail, setDetail] = useState<Contact | null>(null);
  const [saved, setSaved] = useState("");
  const [editingId, setEditingId] = useState<number | null>(null);
  const [bothRoles, setBothRoles] = useState(false);
  const [members, setMembers] = useState<{ id: number; email: string }[]>([]);
  const [accounts, setAccounts] = useState<{ id: number; code: string; name: string; type: string }[]>([]);
  const [limitText, setLimitText] = useState("");
  const [openingText, setOpeningText] = useState("");
  const generation = useRef(0);
  const setPerson = (i: number, patch: Partial<Person>) => setForm(f => ({ ...f, contact_persons: f.contact_persons.map((p, j) => j === i ? { ...p, ...patch } : p) }));
  const plural = role === "customer" ? "Customers" : "Suppliers";
  const canWrite = selected != null && selected.role !== "viewer";

  useEffect(() => {
    generation.current++;
    setRows(null); setError(""); setOpen(false); setDetail(null); setForm(blank()); setSaved(""); setSameAddress(true); setQuery(""); setEditingId(null); setBothRoles(false); setBusy(false); setOpeningText(""); setMembers([]); setLimitText(""); setAccounts([]); setSelectedContacts([]); setBulkGroup("");
    if (orgId == null) return;
    let live = true;
    void api<{ accounts: { id: number; code: string; name: string; type: string }[] }>(`/v1/accounts?org_id=${orgId}`).then(r => { if (live) setAccounts(r.accounts); }).catch(() => { /* defaults stay empty */ });
    api<{ contacts: Contact[] }>(`/v1/contacts?role=${role}&org_id=${orgId}${archivedView ? "&archived=1" : ""}`)
      .then(r => { if (live) setRows(r.contacts); })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    api<{ groups: Group[] }>(`/v1/contact-groups?org_id=${orgId}`)
      .then(r => { if (live) setGroups(r.groups); })
      .catch(() => { /* groups panel stays empty if the list cannot load */ });
    api<{ smart_lists: SmartList[] }>(`/v1/contact-smart-lists?org_id=${orgId}`)
      .then(r => { if (live) setSmartLists(r.smart_lists); })
      .catch(() => { /* smart lists panel stays empty */ });
    api<{ members: { id: number; email: string }[] }>(`/v1/members?org_id=${orgId}`)
      .then(r => { if (live) setMembers(r.members); })
      .catch(() => { /* owner picker stays on "No owner" if the list cannot load */ });
    return () => { live = false; };
  }, [orgId, role, archivedView]);

  // A failed pattern inside the collapsed section would block Save with nothing shown, so open it.
  const more = useRef<HTMLDetailsElement>(null);
  const field = (key: keyof ReturnType<typeof blank>, label: string, type = "text", required = false, maxLength = 200) => <div>
    <label htmlFor={`contact-${key}`}>{label}{!required && <span className="field-optional">Optional</span>}</label>
    <input id={`contact-${key}`} type={type} required={required} maxLength={maxLength} value={String(form[key])} onChange={e => setForm({ ...form, [key]: e.target.value })} />
  </div>;

  function addressFields(key: "billing_address" | "shipping_address") {
    return <div className="form-grid">{([
      ["attention", "Attention / recipient"], ["line1", "Address line 1"], ["line2", "Address line 2"],
      ["city", "City"], ["region", "State / region"], ["postal_code", "Postal code"], ["country", "Country code"], ["phone", "Phone"], ["fax", "Fax number"],
    ] as [keyof Address, string][]).map(([name, label]) => <div key={name}>
      <label htmlFor={`${key}-${name}`}>{label}</label>
      <input id={`${key}-${name}`} value={form[key][name]} maxLength={name === "country" ? 2 : name === "postal_code" ? 20 : 200}
        pattern={name === "postal_code" && form[key].country === "SG" ? "[0-9]{6}" : name === "country" ? "[A-Z]{2}" : undefined}
        onChange={e => setForm({ ...form, [key]: { ...form[key], [name]: name === "country" ? e.target.value.toUpperCase() : e.target.value } })} />
    </div>)}</div>;
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!orgId || busy) return;
    const openingBalance = openingText.trim() === "" ? 0 : parseCents(openingText);
    if (openingBalance === null || openingBalance < 0 || openingBalance > 100000000000000) { setError("Enter a valid opening balance with up to two decimal places."); return; }
    if (openingBalance > 0 && !form.opening_balance_date) { setError("Choose the opening balance date."); return; }
    const requestGeneration = generation.current;
    const current = () => activeCompany.current === orgId && requestGeneration === generation.current;
    setBusy(true); setError(""); setSaved("");
    try {
      const created = await api<Contact>(editingId ? `/v1/contacts/${editingId}` : "/v1/contacts", { method: editingId ? "PUT" : "POST", body: JSON.stringify({ ...form, opening_balance_cents: openingBalance, org_id: orgId, role: bothRoles ? "both" : role, shipping_address: sameAddress ? form.billing_address : form.shipping_address }) });
      const bad = editingId ? "" : await saveExtras("contacts", created.id, orgId, extras); setExtras(noExtras());
      if (!current()) return;
      if (subscribeAfter.current && role === "customer" && !bad) { router.push(`/billing/subscriptions?customer=${created.id}`); return; }
      setRows(prev => [...(prev ?? []).filter(c => c.id !== created.id), created].sort((a, b) => a.name.localeCompare(b.name)));
      setForm(blank()); setOpeningText(""); setLimitText(""); setSameAddress(true); setEditingId(null); setBothRoles(false); setOpen(false); setSaved(`${created.name} saved.` + (bad ? " " + bad : ""));
    } catch (e) {
      if (current()) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
    }
    finally { if (current()) setBusy(false); }
  }

  const filtered = rows?.filter(c => `${c.name} ${c.company_name} ${c.email} ${c.phone}`.toLowerCase().includes(query.toLowerCase()));
  async function toggleArchive(c: Contact) {
    if (!orgId || busy) return;
    setBusy(true); setError("");
    try {
      await api(`/v1/contacts/${c.id}/archive`, { method: "POST", body: JSON.stringify({ org_id: orgId, archived: !c.archived }) });
      setRows(prev => (prev ?? []).filter(x => x.id !== c.id));
      setDetail(null); setSaved(c.archived ? `${c.name} restored.` : `${c.name} archived. History is kept.`);
    } catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } finally { setBusy(false); }
  }
  async function addGroup(e: FormEvent) {
    e.preventDefault(); if (!orgId || !newGroup.trim() || busy) return;
    setBusy(true); setError("");
    try { const g = await api<{ id: number; name: string }>("/v1/contact-groups", { method: "POST", body: JSON.stringify({ org_id: orgId, name: newGroup }) }); setGroups(prev => [...prev, { ...g, contact_ids: [] }]); setNewGroup(""); }
    catch (x) { const a = x as ApiError; setError(messageFor(a.error, a.status)); } finally { setBusy(false); }
  }
  async function toggleMember(g: Group, contact: Contact) {
    if (!orgId || busy) return;
    setBusy(true); setError("");
    const member = g.contact_ids.includes(contact.id);
    try {
      if (member) await api(`/v1/contact-groups/${g.id}/members/${contact.id}?org_id=${orgId}`, { method: "DELETE" });
      else await api(`/v1/contact-groups/${g.id}/members`, { method: "POST", body: JSON.stringify({ org_id: orgId, contact_ids: [contact.id] }) });
      setGroups(prev => prev.map(x => x.id === g.id ? { ...x, contact_ids: member ? x.contact_ids.filter(i => i !== contact.id) : [...x.contact_ids, contact.id] } : x));
    } catch (x) { const a = x as ApiError; setError(messageFor(a.error, a.status)); } finally { setBusy(false); }
  }
  async function archiveSelected() {
    if (orgId == null || !selectedContacts.length || busy) return;
    setBusy(true); setError("");
    try {
      for (const id of selectedContacts) await api(`/v1/contacts/${id}/archive`, { method: "POST", body: JSON.stringify({ org_id: orgId, archived: true }) });
      setSaved(`${selectedContacts.length} contact(s) archived. History is kept.`);
      setSelectedContacts([]);
      const fresh = await api<{ contacts: Contact[] }>(`/v1/contacts?role=${role}&org_id=${orgId}${archivedView ? "&archived=1" : ""}`);
      setRows(fresh.contacts);
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }
  async function addSelectedToGroup() {
    if (!orgId || busy || !bulkGroup || !selectedContacts.length) return;
    setBusy(true); setError(""); setSaved("");
    try {
      await api(`/v1/contact-groups/${bulkGroup}/members`, { method: "POST", body: JSON.stringify({ org_id: orgId, contact_ids: selectedContacts }) });
      setGroups(prev => prev.map(g => g.id === Number(bulkGroup) ? { ...g, contact_ids: [...new Set([...g.contact_ids, ...selectedContacts])] } : g));
      setSaved(`${selectedContacts.length} ${role}${selectedContacts.length === 1 ? "" : "s"} added to group.`); setSelectedContacts([]);
    } catch (x) { const a = x as ApiError; setError(messageFor(a.error, a.status)); } finally { setBusy(false); }
  }
  async function renameGroup(g: Group, name: string) {
    if (!orgId || busy || !name.trim() || name === g.name) return;
    setBusy(true); setError("");
    try { await api(`/v1/contact-groups/${g.id}`, { method: "PUT", body: JSON.stringify({ org_id: orgId, name }) }); setGroups(prev => prev.map(x => x.id === g.id ? { ...x, name } : x)); }
    catch (x) { const a = x as ApiError; setError(messageFor(a.error, a.status)); } finally { setBusy(false); }
  }
  async function saveSmartList(e: FormEvent) {
    e.preventDefault(); if (!orgId || !newList.trim() || busy) return;
    setBusy(true); setError("");
    try { const l = await api<SmartList>("/v1/contact-smart-lists", { method: "POST", body: JSON.stringify({ org_id: orgId, name: newList, query }) }); setSmartLists(prev => [...prev, l]); setNewList(""); }
    catch (x) { const a = x as ApiError; setError(messageFor(a.error, a.status)); } finally { setBusy(false); }
  }
  async function removeSmartList(l: SmartList) {
    if (!orgId || busy) return;
    setBusy(true);
    try { await api(`/v1/contact-smart-lists/${l.id}?org_id=${orgId}`, { method: "DELETE" }); setSmartLists(prev => prev.filter(x => x.id !== l.id)); }
    catch (x) { const a = x as ApiError; setError(messageFor(a.error, a.status)); } finally { setBusy(false); }
  }
  async function openDetail(c: Contact) {
    setDetail(c); setOpen(false); setTab("activity"); setActivity(null);
    if (!orgId) return;
    try { setActivity(await api<Activity>(`/v1/contacts/${c.id}/documents?org_id=${orgId}`)); }
    catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); }
  }
  return <>
    <PageHeader title={plural} description={`Manage ${role} details, addresses, tax references and payment terms.`} actions={<>{selected && <a className="button-secondary" href={`/api/v1/contacts/export?org_id=${selected.id}`} download="contacts.csv">Export {plural.toLowerCase()}</a>}<a className="button-secondary" href="/import">Import contacts</a></>} />
    {orgError && <p className="form-error" role="alert">Company list could not be loaded.</p>}
    {!selected && !orgError && <p className="notice">Select or create a company first.</p>}
    {selected && <>
      <nav className="tabs" aria-label="Active or archived"><ul><li><button type="button" className="button-secondary" aria-pressed={!archivedView} onClick={() => { setArchivedView(false); setDetail(null); }}>Active</button></li><li><button type="button" className="button-secondary" aria-pressed={archivedView} onClick={() => { setArchivedView(true); setDetail(null); }}>Archived</button></li></ul></nav>
      <div className="record-toolbar"><div><label htmlFor="contact-search">Search {plural.toLowerCase()}</label><input id="contact-search" type="search" value={query} onChange={e => setQuery(e.target.value)} /></div>
        {canWrite && <button type="button" disabled={busy || rows === null} onClick={() => { setOpen(!open); setDetail(null); setEditingId(null); setForm(blank()); setExtras(noExtras()); setOpeningText(""); setSameAddress(true); setBothRoles(false); setError(""); setSaved(""); }}>{open ? "Close form" : `New ${role}`}</button>}
      </div>
      {error && <p className="form-error" role="alert">{error}</p>}
      {saved && <p role="status">{saved}</p>}
      {!archivedView && <details><summary>Groups and smart lists</summary>
        <form onSubmit={addGroup} className="form-actions"><div><label htmlFor="contact-new-group">New group</label><input id="contact-new-group" maxLength={100} value={newGroup} onChange={e => setNewGroup(e.target.value)} /></div><button disabled={busy || !newGroup.trim()}>Create group</button></form>
        {groups.length > 0 && <div className="table-scroll"><table className="doc-table"><caption>Contact groups</caption><thead><tr><th>Group</th><th>Contacts</th><th>Rename</th></tr></thead><tbody>{groups.map(g => <tr key={g.id}><td>{g.name}</td><td>{g.contact_ids.length}</td><td><input aria-label={`Rename ${g.name}`} defaultValue={g.name} maxLength={100} onBlur={e => { const v = e.target.value; if (v !== g.name) { e.target.value = g.name; void renameGroup(g, v); } }} /></td></tr>)}</tbody></table></div>}
        <form onSubmit={saveSmartList} className="form-actions"><div><label htmlFor="contact-new-list">Save current search as a smart list</label><input id="contact-new-list" maxLength={100} value={newList} onChange={e => setNewList(e.target.value)} /></div><button disabled={busy || !newList.trim() || !query.trim()}>Save smart list</button></form>
        {smartLists.length > 0 && <ul>{smartLists.map(l => <li key={l.id}><button type="button" className="button-secondary" onClick={() => setQuery(l.query)}>{l.name}</button> <button type="button" className="button-secondary" onClick={() => void removeSmartList(l)}>Delete<span className="sr-only"> {l.name}</span></button></li>)}</ul>}
      </details>}
      {open && <form onInvalidCapture={e => { if (more.current?.contains(e.target as Node)) more.current.open = true; }} onSubmit={save} className="record-form">
        <fieldset disabled={busy}><legend>{role === "customer" ? "Customer" : "Supplier"} identity</legend><p className="section-help">Use a display name you will recognise on transactions. Optional fields can be filled later.</p>
          <div className="form-grid"><div><label htmlFor="contact-type">Contact type</label><select id="contact-type" value={form.contact_type} onChange={e => setForm({ ...form, contact_type: e.target.value })}><option value="business">Business</option><option value="individual">Individual</option></select></div>
          {field("salutation", "Salutation", "text", false, 20)}{field("name", "Display name", "text", true)}{form.contact_type === "business" && field("company_name", "Legal / company name")}{field("first_name", "First name", "text", false, 100)}{field("last_name", "Last name", "text", false, 100)}</div>
          <label className="check-label"><input type="checkbox" checked={bothRoles} onChange={e => setBothRoles(e.target.checked)} />Also use as {role === "customer" ? "supplier" : "customer"}</label>
        </fieldset>
        <fieldset disabled={busy}><legend>Contact details</legend><div className="form-grid">{field("email", "Email", "email", false, 254)}{field("phone", "Phone", "tel", false, 40)}{field("mobile", "Mobile", "tel", false, 40)}{field("website", "Website (https://…)", "url", false, 300)}</div>
          <div className="form-grid"><div><label htmlFor="contact-language">Language</label><select id="contact-language" value={form.language} onChange={e => setForm({ ...form, language: e.target.value })}><option value="en">English</option><option value="zh">Chinese</option><option value="ms">Malay</option><option value="ta">Tamil</option></select></div>
          <div><label htmlFor="contact-currency">Currency</label><input id="contact-currency" value="SGD — Singapore Dollar" disabled readOnly /><p className="section-help">Tidal records everything in SGD.</p></div></div>
          <label className="check-label"><input type="checkbox" checked={form.portal_enabled} onChange={e => setForm({ ...form, portal_enabled: e.target.checked })} />Request portal access for this {role}</label><p className="section-help">Preference saved only. External portal access is not yet available.</p>
          <div><label htmlFor="contact-owner">Owner</label><select id="contact-owner" value={form.owner_user_id} onChange={e => setForm({ ...form, owner_user_id: Number(e.target.value) })}><option value={0}>No owner</option>{members.map(m => <option key={m.id} value={m.id}>{m.email}</option>)}</select><p className="section-help">Assigns responsibility only. Owner-based access restrictions are not yet available; company members can still see this record.</p></div>
        </fieldset>
        <fieldset disabled={busy}><legend>Contact persons</legend>
          {form.contact_persons.map((p, i) => <div className="form-grid" key={i}>
            <div><label htmlFor={`person-${i}-salutation`}>Salutation</label><input id={`person-${i}-salutation`} maxLength={20} value={p.salutation} onChange={e => setPerson(i, { salutation: e.target.value })} /></div>
            <div><label htmlFor={`person-${i}-first`}>First name</label><input id={`person-${i}-first`} maxLength={100} value={p.first_name} onChange={e => setPerson(i, { first_name: e.target.value })} /></div>
            <div><label htmlFor={`person-${i}-last`}>Last name</label><input id={`person-${i}-last`} maxLength={100} value={p.last_name} onChange={e => setPerson(i, { last_name: e.target.value })} /></div>
            <div><label htmlFor={`person-${i}-email`}>Email</label><input id={`person-${i}-email`} type="email" maxLength={254} value={p.email} onChange={e => setPerson(i, { email: e.target.value })} /></div>
            <div><label htmlFor={`person-${i}-work`}>Work phone</label><input id={`person-${i}-work`} type="tel" maxLength={40} value={p.work_phone} onChange={e => setPerson(i, { work_phone: e.target.value })} /></div>
            <div><label htmlFor={`person-${i}-mobile`}>Mobile</label><input id={`person-${i}-mobile`} type="tel" maxLength={40} value={p.mobile} onChange={e => setPerson(i, { mobile: e.target.value })} /></div>
            <button className="button-secondary" type="button" onClick={() => setForm({ ...form, contact_persons: form.contact_persons.filter((_, j) => j !== i) })}>Remove person<span className="sr-only"> {i + 1}</span></button>
          </div>)}
          <button className="button-secondary" type="button" disabled={form.contact_persons.length >= 20} onClick={() => setForm({ ...form, contact_persons: [...form.contact_persons, person()] })}>Add contact person</button>
        </fieldset>
        <details key={editingId ?? "new"} open={editingId != null} ref={more}><summary>{role === "customer" ? "Add more details" : "Other details"}</summary>
        <fieldset disabled={busy}><legend>Opening balance</legend><p className="section-help">What this {role} {role === "customer" ? "owes you" : "you owe them"} on the day you start using Tidal. Leave at zero if none.</p>
          <div className="form-grid"><div><label htmlFor="contact-opening">Amount (SGD)</label><input id="contact-opening" inputMode="decimal" readOnly={form.opening_balance_cents > 0} value={openingText} onChange={e => setOpeningText(e.target.value)} /></div>
          <div><label htmlFor="contact-opening-date">As at date</label><DateInput id="contact-opening-date" readOnly={form.opening_balance_cents > 0} value={form.opening_balance_date} onChange={e => setForm({ ...form, opening_balance_date: e.target.value })}/></div></div>
        </fieldset>
        <fieldset disabled={busy}><legend>Tax and payment terms</legend><div className="form-grid">{field("uen", "UEN", "text", false, 10)}{field("gst_number", "GST registration number", "text", false, 30)}{field("account_number", "Account number", "text", false, 50)}<div><label htmlFor="contact-terms">Invoice due date</label><TermsSelect id="contact-terms" orgId={orgId ?? 0} days={form.payment_terms_days} onChange={d => setForm({ ...form, payment_terms_days: d })} /><p className="section-help">Used on invoices. Currency: SGD.</p></div><div><label htmlFor="contact-bill-terms">Bill due date</label><TermsSelect id="contact-bill-terms" orgId={orgId ?? 0} days={form.bill_payment_terms_days} onChange={d => setForm({ ...form, bill_payment_terms_days: d })} /><p className="section-help">Used on bills for this supplier. Set it separately from the invoice terms.</p></div><div><label htmlFor="contact-sales-treatment">Sales amounts are</label><select id="contact-sales-treatment" value={form.sales_treatment} onChange={e => setForm({ ...form, sales_treatment: e.target.value })}><option value="">Use company default</option><option value="exclusive">Tax exclusive</option><option value="inclusive">Tax inclusive</option></select></div><div><label htmlFor="contact-sales-tax">Sales tax rate</label><input id="contact-sales-tax" maxLength={20} value={form.sales_tax_code} onChange={e => setForm({ ...form, sales_tax_code: e.target.value.toUpperCase() })} placeholder="SR" /><p className="section-help">Used on new invoices for this customer unless a line sets its own code.</p></div><div><label htmlFor="contact-purchase-treatment">Purchase amounts are</label><select id="contact-purchase-treatment" value={form.purchase_treatment} onChange={e => setForm({ ...form, purchase_treatment: e.target.value })}><option value="">Use company default</option><option value="exclusive">Tax exclusive</option><option value="inclusive">Tax inclusive</option></select></div><div><label htmlFor="contact-purchase-tax">Purchase tax rate</label><input id="contact-purchase-tax" maxLength={20} value={form.purchase_tax_code} onChange={e => setForm({ ...form, purchase_tax_code: e.target.value.toUpperCase() })} placeholder="TX" /></div><div><label htmlFor="contact-discount">Discount %</label><input id="contact-discount" inputMode="decimal" value={form.discount_basis_points ? (form.discount_basis_points / 100).toString() : ""} onChange={e => { const n = Number(e.target.value); setForm(f => ({ ...f, discount_basis_points: e.target.value.trim() !== "" && Number.isFinite(n) && n >= 0 && n <= 100 ? Math.round(n * 100) : 0 })); }} /><p className="section-help">Applied to new invoice lines unless a line gives its own discount.</p></div></div></fieldset>
        <fieldset disabled={busy}><legend>Financial details</legend><p className="section-help">Used on documents you send and in your own records. Nothing here posts to the ledger.</p>
          <div className="form-grid">{field("fax", "Fax", "tel", false, 40)}{field("direct_dial", "Direct dial", "tel", false, 40)}{field("country", "Country (2-letter code)", "text", false, 2)}</div>
          <div className="form-grid">{field("bank_account_name", "Bank account name", "text", false, 100)}{field("bank_account_number", "Bank account number", "text", false, 60)}{field("bank_particulars", "Details (particulars)", "text", false, 100)}</div>
          <div className="form-grid">
            <div><label htmlFor="contact-sales-account">Sales account</label><select id="contact-sales-account" value={form.sales_account_id} onChange={e => setForm({ ...form, sales_account_id: Number(e.target.value) })}><option value={0}>Use the item or invoice default</option>{accounts.filter(a => a.type === "revenue").map(a => <option key={a.id} value={a.id}>{a.code} {a.name}</option>)}</select></div>
            <div><label htmlFor="contact-purchase-account">Purchase account</label><select id="contact-purchase-account" value={form.purchase_account_id} onChange={e => setForm({ ...form, purchase_account_id: Number(e.target.value) })}><option value={0}>Use the item or bill default</option>{accounts.filter(a => a.type === "expense").map(a => <option key={a.id} value={a.id}>{a.code} {a.name}</option>)}</select><p className="section-help">Defaults only. Each invoice or bill line still posts to its own account.</p></div>
          </div>
          <div className="form-grid"><div><label htmlFor="contact-credit-limit">Credit limit (SGD)</label><input id="contact-credit-limit" inputMode="decimal" value={limitText} onChange={e => { setLimitText(e.target.value); const c = parseCents(e.target.value); if (c !== null) setForm(f => ({ ...f, credit_limit_cents: c, block_over_limit: c === 0 ? false : f.block_over_limit })); }} /><p className="section-help">Leave blank for no limit.</p></div>
            <label className="check-label"><input type="checkbox" checked={form.block_over_limit} disabled={form.credit_limit_cents === 0} onChange={e => setForm({ ...form, block_over_limit: e.target.checked })} />Block new invoices once the limit is reached</label></div>
        </fieldset>
        <fieldset disabled={busy}><legend>Billing address</legend>{addressFields("billing_address")}</fieldset>
        <fieldset disabled={busy}><legend>Shipping address</legend><label className="check-label"><input type="checkbox" checked={sameAddress} onChange={e => setSameAddress(e.target.checked)} />Same as billing address</label>{!sameAddress && addressFields("shipping_address")}</fieldset>
        <fieldset disabled={busy}><legend>Remarks (for internal use)</legend><label htmlFor="contact-remarks">Internal remarks</label><textarea id="contact-remarks" maxLength={4000} rows={4} value={form.internal_remarks} onChange={e => setForm({ ...form, internal_remarks: e.target.value })} /><p className="section-help">Not included in customer or supplier documents.</p></fieldset>
        <fieldset><legend>Documents</legend><input type="file" aria-label="Contact documents" multiple disabled /><p className="section-help">Save this record, then upload documents from its details. Limit: 10 files, 10 MB each.</p></fieldset>
        </details>
        {!editingId && orgId != null && <ExtrasForm kind="contacts" orgId={orgId} value={extras} onChange={setExtras} />}
        <fieldset disabled={busy}><legend>Internal notes</legend><label htmlFor="contact-notes">Notes <span className="field-optional">Optional</span></label><textarea id="contact-notes" maxLength={4000} rows={4} value={form.notes} onChange={e => setForm({ ...form, notes: e.target.value })} /></fieldset>
        <div className="form-actions"><button disabled={busy} type="submit" onClick={() => { subscribeAfter.current = false; }}>{busy ? "Saving…" : `Save ${role}`}</button>{role === "customer" && !editingId && <button disabled={busy} type="submit" onClick={() => { subscribeAfter.current = true; }}>Save and Subscribe</button>}<button disabled={busy} className="button-secondary" type="button" onClick={() => setOpen(false)}>Cancel</button></div>
      </form>}
      {detail && <section className="record-form" aria-label={`${role} details`}><h2>{detail.name}</h2>
        <nav className="tabs" aria-label="Contact tabs"><ul>{(['activity', 'details', 'financial', 'documents'] as const).map(t => <li key={t}><button type="button" className="button-secondary" aria-pressed={tab === t} onClick={() => setTab(t)}>{t === "activity" ? "Activity" : t === "details" ? "Contact details" : t === "financial" ? "Financial details" : "Documents"}</button></li>)}</ul></nav>
        {tab === "activity" && <><p className="section-help">Paid and Sent come from recorded payments and the approval date. Tidal does not email documents.</p>
          {activity === null ? <p role="status">Loading activity…</p> : <div className="table-scroll"><table className="doc-table"><caption>Invoices and bills for {detail.name}</caption><thead><tr><th>Type</th><th>Number</th><th>Reference</th><th>Date</th><th>Due date</th><th className="num">Paid</th><th>Paid date</th><th>Sent</th><th>Status</th></tr></thead><tbody>
            {[...activity.invoices.map(i => ({ ...i, kind: "Invoice" })), ...activity.bills.map(b => ({ ...b, kind: "Bill" }))].map(r => <tr key={`${r.kind}-${r.id}`}><td>{r.kind}</td><td>{r.document_number}</td><td>{r.reference || "—"}</td><td>{r.date}</td><td>{r.due_date}</td><td className="num">{formatCents(r.paid_cents)}</td><td>{r.paid_date || "—"}</td><td>{r.sent_date || "Not sent"}</td><td>{r.lifecycle_status || "—"}</td></tr>)}
          </tbody></table></div>}
          {activity !== null && activity.invoices.length + activity.bills.length === 0 && <p>No invoices or bills for this contact yet.</p>}</>}
        {tab === "financial" && <dl className="detail-grid">{Object.entries({ "Credit limit": detail.credit_limit_cents ? formatCents(detail.credit_limit_cents) : "No limit", "Block over limit": detail.block_over_limit ? "Yes" : "No", "Discount %": detail.discount_basis_points ? `${detail.discount_basis_points / 100}%` : "", "Invoice due date": `${detail.payment_terms_days} days`, "Bill due date": detail.bill_payment_terms_days != null ? `${detail.bill_payment_terms_days} days` : "Same as invoice", "Sales amounts are": detail.sales_treatment, "Sales tax rate": detail.sales_tax_code, "Purchase amounts are": detail.purchase_treatment, "Purchase tax rate": detail.purchase_tax_code, "Sales account": accounts.find(a => a.id === detail.sales_account_id)?.name ?? "", "Purchase account": accounts.find(a => a.id === detail.purchase_account_id)?.name ?? "", "Bank account name": detail.bank_account_name, "Bank account number": detail.bank_account_number, "Bank particulars": detail.bank_particulars, "You owe": formatCents(detail.you_owe_cents ?? 0), "They owe": formatCents(detail.they_owe_cents ?? 0), "Opening balance": formatCents(detail.opening_balance_cents) }).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{String(value).trim() || "Not provided"}</dd></div>)}</dl>}
        {tab !== "activity" && <>{orgId && <><Attachments key={detail.id} kind="contacts" id={detail.id} orgId={orgId} readOnly={!canWrite} /><Extras kind="contacts" id={detail.id} orgId={orgId} readOnly={!canWrite} /><ExtraAddresses key={`a${detail.id}`} contactId={detail.id} orgId={orgId} readOnly={!canWrite} /></>}<dl className="detail-grid">{Object.entries({ "Company": detail.company_name, "Contact": `${detail.first_name} ${detail.last_name}`, "Email": detail.email, "Phone": detail.phone, "Mobile": detail.mobile, "Website": detail.website, "UEN": detail.uen, "GST number": detail.gst_number, "Account number": detail.account_number, "Fax": detail.fax, "Direct dial": detail.direct_dial, "Country": detail.country, "Bank account name": detail.bank_account_name, "Bank account number": detail.bank_account_number, "Bank particulars": detail.bank_particulars, "Sales amounts are": detail.sales_treatment, "Sales tax rate": detail.sales_tax_code, "Purchase amounts are": detail.purchase_treatment, "Purchase tax rate": detail.purchase_tax_code, "Sales account": accounts.find(a => a.id === detail.sales_account_id)?.name ?? "", "Purchase account": accounts.find(a => a.id === detail.purchase_account_id)?.name ?? "", "Credit limit": detail.credit_limit_cents ? formatCents(detail.credit_limit_cents) : "", "Block over limit": detail.block_over_limit ? "Yes" : "", "Payment terms": `${detail.payment_terms_days} days`, "Billing address": Object.values(detail.billing_address).filter(Boolean).join(", "), "Shipping address": Object.values(detail.shipping_address).filter(Boolean).join(", "), "Opening balance": formatCents(detail.opening_balance_cents), "Opening balance date": detail.opening_balance_date, "Language preference": detail.language, "Internal remarks": detail.internal_remarks, "Notes": detail.notes }).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value.trim() || "Not provided"}</dd></div>)}</dl></>}<div className="form-actions">{canWrite && <button disabled={busy} onClick={() => { const { id, role: contactRole, ...values } = detail; setForm({ ...blank(), ...values, billing_address: { ...address(), ...values.billing_address }, shipping_address: { ...address(), ...values.shipping_address }, contact_persons: values.contact_persons ?? [] }); setOpeningText(values.opening_balance_cents ? (values.opening_balance_cents / 100).toFixed(2) : ""); setLimitText(values.credit_limit_cents ? (values.credit_limit_cents / 100).toFixed(2) : ""); setEditingId(id); setBothRoles(contactRole === "both"); setSameAddress(JSON.stringify(values.billing_address) === JSON.stringify(values.shipping_address)); setOpen(true); setDetail(null); setError(""); setSaved(""); }}>Edit {role}</button>}<button className="button-secondary" disabled={busy} onClick={() => void toggleArchive(detail)}>{detail.archived ? "Restore contact" : "Archive contact"}</button><Link className="button-secondary" href={`/reports/contact-activity?kind=${role}&contact=${detail.id}`}>Activity report</Link><button className="button-secondary" onClick={() => setDetail(null)}>Close details</button></div></section>}
      {rows === null && !error && <p role="status">Loading {plural.toLowerCase()}…</p>}
      {filtered?.length === 0 && <div className="empty-state" role="status"><p>{rows?.length ? "No matching records." : `No ${plural.toLowerCase()} yet. Add your first ${role} to get started.`}</p></div>}
      {!!filtered?.length && canWrite && !archivedView && groups.length > 0 && <div className="form-actions"><label htmlFor="contact-bulk-group">Add selected to group</label><select id="contact-bulk-group" value={bulkGroup} onChange={e => setBulkGroup(e.target.value)}><option value="">Choose group</option>{groups.map(g => <option key={g.id} value={g.id}>{g.name}</option>)}</select><button type="button" disabled={busy || !bulkGroup || !selectedContacts.length} onClick={() => void addSelectedToGroup()}>Add {selectedContacts.length || "selected"}</button><button type="button" className="button-secondary" disabled={!selectedContacts.length} onClick={() => setSelectedContacts([])}>Clear selection</button><button type="button" className="button-secondary" disabled={busy || !selectedContacts.length} onClick={() => void archiveSelected()}>Archive selected</button></div>}
      <details className="record-toolbar"><summary>Columns</summary>{["company", "email", "phone", "terms"].map(c => <label key={c} className="check-label"><input type="checkbox" checked={!hiddenCols.includes(c)} onChange={e => setHiddenCols(prev => e.target.checked ? prev.filter(x => x !== c) : [...prev, c])} />{c}</label>)}</details>
      {!!filtered?.length && <div className="table-scroll"><table className="doc-table"><caption className="sr-only">{plural} (up to 500 records)</caption><thead><tr>{canWrite && !archivedView && <th scope="col"><span className="visually-hidden">Select</span></th>}<th scope="col">Name</th><th scope="col">Company</th><th scope="col">Email</th><th scope="col">Phone</th><th scope="col" className="num">Terms</th><th scope="col" className="num">You owe</th><th scope="col" className="num">They owe</th><th scope="col">Details</th></tr></thead><tbody>{filtered.map(c => <tr key={c.id}>{canWrite && !archivedView && <td><input type="checkbox" aria-label={`Select ${c.name}`} checked={selectedContacts.includes(c.id)} onChange={e => setSelectedContacts(prev => e.target.checked ? [...prev, c.id] : prev.filter(id => id !== c.id))} /></td>}<th scope="row">{c.name}</th><td>{c.company_name || "—"}</td><td>{c.email || "—"}</td><td>{c.phone || "—"}</td><td className="num">{c.payment_terms_days} days</td><td className="num">{formatCents(c.you_owe_cents ?? 0)}</td><td className="num">{formatCents(c.they_owe_cents ?? 0)}</td><td><button className="button-secondary" disabled={busy} onClick={() => void openDetail(c)}>View<span className="sr-only"> {c.name}</span></button>{groups.length > 0 && <details><summary>Groups</summary>{groups.map(g => <label className="check-label" key={g.id}><input type="checkbox" checked={g.contact_ids.includes(c.id)} disabled={busy} onChange={() => void toggleMember(g, c)} /> {g.name}</label>)}</details>}</td></tr>)}</tbody></table></div>}
    </>}
  </>;
}
