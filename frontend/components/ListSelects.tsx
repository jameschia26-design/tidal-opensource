"use client";
import { useEffect, useState } from "react";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Person = { name: string; active: boolean };
type Account = { id: number; code: string; name: string; type: string };

export const cashBankAccounts = (accounts: Account[]) => accounts.filter(a => a.type === "asset" && !["1100", "2310"].includes(a.code));
type Term = { name: string; days: number; active: boolean };

// Salespeople are stored on documents as plain names, so old documents stay readable if a person is switched off.
export function SalespersonSelect({ id, orgId, value, onChange }: { id: string; orgId: number; value: string; onChange: (v: string) => void }) {
  const [list, setList] = useState<Person[]>([]);
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    api<{ salespeople: Person[] }>(`/v1/salespeople?org_id=${orgId}`).then(r => { if (live) setList(r.salespeople); }).catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId]);
  const names = list.filter(p => p.active).map(p => p.name);
  if (value && !names.includes(value)) names.push(value);
  async function add() {
    const trimmed = name.trim();
    if (!trimmed || busy) return;
    setBusy(true); setError("");
    try {
      await api("/v1/salespeople", { method: "POST", body: JSON.stringify({ org_id: orgId, name: trimmed }) });
      setList(prev => [...prev, { name: trimmed, active: true }]);
      onChange(trimmed); setName(""); setAdding(false);
    } catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); }
    finally { setBusy(false); }
  }
  return <><select id={id} value={value} onChange={e => onChange(e.target.value)}><option value="">No salesperson</option>{names.map(n => <option key={n} value={n}>{n}</option>)}</select> <button type="button" className="button-secondary" onClick={() => { setAdding(!adding); setError(""); }}>{adding ? "Cancel" : "Add salesperson"}</button>{adding && <div><label htmlFor={`${id}-new`}>New salesperson name</label><input id={`${id}-new`} maxLength={200} value={name} onChange={e => setName(e.target.value)} /><button type="button" disabled={busy || !name.trim()} onClick={() => void add()}>{busy ? "Adding…" : "Add"}</button></div>}{error && <p role="alert" className="form-error">{error}</p>}</>;
}

// Documents store days, so a named term is just a label for a number of days.
export function TermsSelect({ id, orgId, days, onChange }: { id: string; orgId: number; days: number; onChange: (days: number) => void }) {
  const [list, setList] = useState<Term[]>([]);
  useEffect(() => {
    let live = true;
    api<{ payment_terms: Term[] }>(`/v1/payment-terms?org_id=${orgId}`).then(r => { if (live) setList(r.payment_terms); }).catch(() => {});
    return () => { live = false; };
  }, [orgId]);
  const options = list.filter(t => t.active);
  const known = options.some(t => t.days === days);
  return <select id={id} value={days} onChange={e => onChange(Number(e.target.value))}>
    {options.map(t => <option key={`${t.name}-${t.days}`} value={t.days}>{t.name}{t.days > 0 ? ` (${t.days} days)` : ""}</option>)}
    {!known && <option value={days}>{days === 0 ? "Due on Receipt" : `${days} days`}</option>}
  </select>;
}

type Mode = { value: string; label: string; active: boolean };

// Built-in modes are stored by key and company modes by name. A mode switched off stays shown while a document still holds it.
export function PaymentModeSelect({ id, orgId, value, onChange }: { id: string; orgId: number; value: string; onChange: (v: string) => void }) {
  const [list, setList] = useState<Mode[]>([]);
  useEffect(() => {
    let live = true;
    api<{ payment_modes: Mode[] }>(`/v1/payment-modes?org_id=${orgId}`).then(r => { if (live) setList(r.payment_modes); }).catch(() => {});
    return () => { live = false; };
  }, [orgId]);
  const options = list.filter(m => m.active);
  const known = options.some(m => m.value === value);
  return <select id={id} value={value} onChange={e => onChange(e.target.value)}>
    {options.map(m => <option key={m.value} value={m.value}>{m.label}</option>)}
    {!known && value && <option value={value}>{value}</option>}
  </select>;
}

type Project = { id: number; name: string; active: boolean };

// A project belongs to one customer. Documents store its name, so a switched-off project stays shown on old documents.
export function ProjectSelect({ id, orgId, contactId, value, onChange }: { id: string; orgId: number; contactId: string; value: string; onChange: (v: string) => void }) {
  const [list, setList] = useState<Project[]>([]);
  useEffect(() => {
    let live = true;
    if (!contactId) { setList([]); return; }
    api<{ projects: Project[] }>(`/v1/projects?org_id=${orgId}&contact_id=${contactId}`).then(r => { if (live) setList(r.projects); }).catch(() => {});
    return () => { live = false; };
  }, [orgId, contactId]);
  const names = list.filter(p => p.active).map(p => p.name);
  if (value && !names.includes(value)) names.push(value);
  return <select id={id} value={value} disabled={!contactId} onChange={e => onChange(e.target.value)}><option value="">{contactId ? "No project" : "Select a customer first"}</option>{names.map(n => <option key={n} value={n}>{n}</option>)}</select>;
}

// Same list as ProjectSelect, but keeps the project id (expenses link by id).
export function ProjectIdSelect({ id, orgId, contactId, value, onChange }: { id: string; orgId: number; contactId: string; value: string; onChange: (v: string) => void }) {
  const [list, setList] = useState<Project[]>([]);
  useEffect(() => {
    let live = true;
    if (!contactId) { setList([]); return; }
    api<{ projects: Project[] }>(`/v1/projects?org_id=${orgId}&contact_id=${contactId}`).then(r => { if (live) setList(r.projects); }).catch(() => {});
    return () => { live = false; };
  }, [orgId, contactId]);
  return <select id={id} value={value} disabled={!contactId} onChange={e => onChange(e.target.value)}><option value="">{contactId ? "No project" : "Select a customer first"}</option>{list.filter(p => p.active).map(p => <option key={p.id} value={p.id}>{p.name}</option>)}</select>;
}
