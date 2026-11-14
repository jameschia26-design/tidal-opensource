"use client";
import Link from "next/link";
import { useEffect, useState } from "react";
import { useOrg } from "@/components/OrgProvider";
import { api } from "@/lib/api";

type Results = {
  contacts: { id: number; name: string; role: string }[];
  invoices: { id: number; number: string; contact: string; total_cents: number; date: string }[];
  bills: { id: number; reference: string; contact: string; total_cents: number; date: string }[];
  items: { id: number; name: string; sku: string; selling_price_cents: number }[];
};

// Header search: one term, short lists per record type. It links to the record's list page rather than
// guessing a detail route, so a result can never point at a page that does not exist.
export default function GlobalSearch() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const [term, setTerm] = useState("");
  const [results, setResults] = useState<Results | null>(null);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (!orgId || term.trim().length < 2) { setResults(null); return; }
    let live = true;
    const id = setTimeout(() => {
      api<Results>(`/v1/search?org_id=${orgId}&q=${encodeURIComponent(term.trim())}`)
        .then(r => { if (live) setResults(r); })
        .catch(() => { if (live) setResults(null); });
    }, 200);
    return () => { live = false; clearTimeout(id); };
  }, [orgId, term]);

  if (!orgId) return null;
  const count = results ? results.contacts.length + results.invoices.length + results.bills.length + results.items.length : 0;
  return <div className="global-search">
    <label className="visually-hidden" htmlFor="global-search-term">Search contacts, invoices, bills and items</label>
    <input id="global-search-term" type="search" placeholder="Search" value={term} onChange={e => { setTerm(e.target.value); setOpen(true); }} onFocus={() => setOpen(true)} />
    {open && results && term.trim().length >= 2 && (
      <div className="global-search-results" role="region" aria-label="Search results">
        {count === 0 && <p className="muted">No matches.</p>}
        {results.contacts.length > 0 && <><h3>Contacts</h3><ul>{results.contacts.map(c => <li key={c.id}><Link href="/contacts" onClick={() => setOpen(false)}>{c.name} <span className="muted">({c.role})</span></Link></li>)}</ul></>}
        {results.invoices.length > 0 && <><h3>Invoices</h3><ul>{results.invoices.map(i => <li key={i.id}><Link href="/sales/invoices" onClick={() => setOpen(false)}>{i.number} · {i.contact} · ${(i.total_cents / 100).toFixed(2)}</Link></li>)}</ul></>}
        {results.bills.length > 0 && <><h3>Bills</h3><ul>{results.bills.map(b => <li key={b.id}><Link href="/purchases/bills" onClick={() => setOpen(false)}>{b.reference} · {b.contact} · ${(b.total_cents / 100).toFixed(2)}</Link></li>)}</ul></>}
        {results.items.length > 0 && <><h3>Items</h3><ul>{results.items.map(i => <li key={i.id}><Link href="/items" onClick={() => setOpen(false)}>{i.name}{i.sku ? ` · ${i.sku}` : ""}</Link></li>)}</ul></>}
      </div>
    )}
  </div>;
}
