"use client";
// One list across customers and suppliers. Editing stays on the
// customer and supplier screens, which carry the full field set; this view is read-only.
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Contact = { id: number; name: string; email: string; role: string; phone: string; city: string };
type Kind = "all" | "customer" | "supplier";

export default function AllContacts() {
  const { selected } = useOrg();
  const [rows, setRows] = useState<Contact[]>([]);
  const [kind, setKind] = useState<Kind>("all");
  const [search, setSearch] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    if (!selected) return;
    let live = true;
    api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${selected.id}`)
      .then((r) => { if (live) setRows(r.contacts); })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [selected]);
  const q = search.trim().toLocaleLowerCase();
  const shown = rows.filter((c) => (kind === "all" || c.role === kind || c.role === "both") && (!q || c.name.toLocaleLowerCase().includes(q) || c.email.toLocaleLowerCase().includes(q)));

  return <>
    <PageHeader title="All contacts" description="Customers and suppliers in one list." actions={<>
      {selected && <a className="button-secondary" href={`/api/v1/contacts/export?org_id=${selected.id}`} download="contacts.csv">Export contacts</a>}
      <a className="button-secondary" href="/import">Import contacts</a>
      <a href="/sales/customers">New customer</a>
    </>} />
    {error && <p role="alert" className="form-error">{error}</p>}
    {!selected && <p>Choose a company first.</p>}
    {selected && <>
      <div className="form-actions" role="group" aria-label="Contact type">
        {(["all", "customer", "supplier"] as Kind[]).map((k) => <button key={k} type="button" className={k === kind ? "" : "button-secondary"} aria-pressed={k === kind} onClick={() => setKind(k)}>{k === "all" ? "All" : k === "customer" ? "Customers" : "Suppliers"}</button>)}
      </div>
      <p><label htmlFor="contact-search">Search contacts</label> <input id="contact-search" type="search" value={search} onChange={(e) => setSearch(e.target.value)} /></p>
      <div className="table-scroll"><table className="doc-table"><caption>{shown.length} contact{shown.length === 1 ? "" : "s"}</caption>
        <thead><tr><th>Name</th><th>Role</th><th>Email</th><th>Phone</th><th>City</th><th>Open</th></tr></thead>
        <tbody>{shown.map((c) => <tr key={c.id}>
          <td>{c.name}</td>
          <td>{c.role === "both" ? "Customer and supplier" : c.role}</td>
          <td>{c.email}</td><td>{c.phone}</td><td>{c.city}</td>
          <td><a href={c.role === "supplier" ? "/purchases/suppliers" : "/sales/customers"}>Open</a></td>
        </tr>)}</tbody>
      </table></div>
      {shown.length === 0 && <p>No contacts match. Add one from Customers or Suppliers.</p>}
    </>}
  </>;
}
