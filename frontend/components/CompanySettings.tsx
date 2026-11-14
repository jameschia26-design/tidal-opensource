"use client";
import { useEffect, useState } from "react";
import { useOrg } from "@/components/OrgProvider";
import CompanyForm, { blankCompany, type Company } from "@/components/CompanyForm";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

export default function CompanySettings() {
  const { selected, refresh } = useOrg();
  const [loaded, setLoaded] = useState<{ id: number; profile: Company } | null>(null);
  const [error, setError] = useState("");
  const [newBook, setNewBook] = useState(false);
  const id = selected?.id;
  useEffect(() => {
    setError(""); setLoaded(null); setNewBook(false);
    if (id == null) return;
    let live = true;
    api<Company & { id: number }>(`/v1/organizations/${id}`).then(response => {
      if (live) { const defaults = blankCompany(); const { id: companyId, ...profile } = response; setLoaded({ id: companyId, profile: { ...defaults, ...profile, address: { ...defaults.address, ...profile.address }, physical_address: { ...defaults.physical_address, ...profile.physical_address } } }); }
    }).catch((err: ApiError) => { if (live) setError(messageFor(err.error, err.status)); });
    return () => { live = false; };
  }, [id]);
  if (!selected) return <p className="notice">Select or create a company to see its profile.</p>;
  if (error) return <p className="form-error" role="alert">{error}</p>;
  if (!loaded || loaded.id !== id) return <p role="status">Loading company profile…</p>;
  const readOnly = selected.role !== "owner" && selected.role !== "admin";
  return <><CompanyForm key={id} initial={loaded.profile} companyId={id} readOnly={readOnly} onSaved={refresh} />
    {!readOnly && <section className="record-form"><h2>Separate books within this company</h2><p>Each set keeps its own balances, customers and documents. Company team permissions apply to every set. Available on every tier.</p><button type="button" className="button-secondary" onClick={() => setNewBook(!newBook)}>{newBook ? "Cancel new books" : "Open another set of books"}</button>
      {newBook && <CompanyForm key={`books-${id}`} initial={{ ...loaded.profile, name: "", fiscal_year_end: loaded.profile.fiscal_year_end }} bookCompanyId={selected.company_id || selected.id} onSaved={async () => { await refresh(); setNewBook(false); }} />}
    </section>}
  </>;
}
