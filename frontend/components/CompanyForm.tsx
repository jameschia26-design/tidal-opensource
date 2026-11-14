"use client";
import { useEffect, useState, type FormEvent } from "react";
import { sameCompanyAddress } from "@/lib/company-address";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

const blankAddress = () => ({ attention: "", line1: "", line2: "", city: "Singapore", region: "", postal_code: "", country: "SG" });
export const blankCompany = () => ({ name: "", legal_name: "", uen: "", business_type: "", industry: "", description: "", contact_email: "", phone: "", mobile: "", fax: "", website: "", facebook: "", twitter: "", linkedin: "", fiscal_year_end: "12-31", gst_registered: false, gst_effective_from: "", gst_number: "", address: blankAddress(), physical_address: blankAddress() });
export type Company = ReturnType<typeof blankCompany>;
const months = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];

export default function CompanyForm({ initial, companyId, bookCompanyId, readOnly = false, onSaved }: { initial: Company; companyId?: number; bookCompanyId?: number; readOnly?: boolean; onSaved: () => Promise<void> }) {
  const [form, setForm] = useState(initial);
  const [samePhysical, setSamePhysical] = useState(() => sameCompanyAddress(initial.address, initial.physical_address));
  const [physicalBackup, setPhysicalBackup] = useState<Company["address"] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const [timezone, setTimezone] = useState("Asia/Singapore");
  useEffect(() => {
    const id = companyId ?? bookCompanyId;
    if (!id) return;
    let live = true;
    api<{ timezone: string }>(`/v1/company-timezone?org_id=${id}`).then(r => { if (live) setTimezone(r.timezone); }).catch(() => { /* keep the Singapore default */ });
    return () => { live = false; };
  }, [companyId, bookCompanyId]);
  const month = Number(form.fiscal_year_end.slice(0, 2));
  const day = Number(form.fiscal_year_end.slice(3));
  const days = new Date(Date.UTC(2000, month, 0)).getUTCDate();
  const setRegistered = (key: keyof Company["address"], value: string) => {
    const address = { ...form.address, [key]: value };
    setForm({ ...form, address, ...(samePhysical ? { physical_address: address } : {}) });
  };
  function toggleSamePhysical(checked: boolean) {
    if (checked) setPhysicalBackup(form.physical_address);
    setSamePhysical(checked);
    setForm({ ...form, physical_address: checked ? form.address : physicalBackup ?? form.physical_address });
  }
  const text = (key: "name" | "legal_name" | "uen" | "industry" | "contact_email" | "phone" | "mobile" | "fax" | "website" | "gst_number" | "facebook" | "twitter" | "linkedin", label: string, maxLength: number, type = "text") => <div><label htmlFor={`company-${key}`}>{label}{key !== "name" && <span className="field-optional">Optional</span>}</label><input id={`company-${key}`} required={key === "name"} maxLength={maxLength} type={type} value={form[key]} onChange={e => setForm({ ...form, [key]: key === "uen" ? e.target.value.toUpperCase() : e.target.value })} /></div>;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (busy || readOnly) return;
    setBusy(true); setError(""); setSaved(false);
    try {
      const made = await api<{ id?: number }>(companyId ? `/v1/organizations/${companyId}` : bookCompanyId ? `/v1/organizations/${bookCompanyId}/books` : "/v1/organizations", { method: companyId ? "PUT" : "POST", body: JSON.stringify({ ...form, physical_address: samePhysical ? form.address : form.physical_address }) });
      const id = companyId ?? bookCompanyId ?? made.id;
      if (id) await api("/v1/company-timezone", { method: "PUT", body: JSON.stringify({ org_id: id, timezone }) });
      setSaved(true);
      await onSaved();
    } catch (e) {
      const err = e as ApiError;
      setError(err.error === "organization_limit" ? "Your account already owns a company. Multiple companies require Premier." : messageFor(err.error, err.status));
    } finally { setBusy(false); }
  }

  return <form className="record-form" onSubmit={submit}>
    {readOnly && <p className="notice">Only company owners and administrators can change this profile.</p>}
    <fieldset disabled={busy || readOnly}><legend>Business identity</legend><div className="form-grid">
      {text("name", "Display name", 200)}{text("legal_name", "Registered legal name", 200)}{text("uen", "UEN", 10)}
      <div><label htmlFor="company-business-type">Business type</label><select id="company-business-type" value={form.business_type} onChange={e => setForm({ ...form, business_type: e.target.value })}><option value="">Not specified</option><option value="company">Company</option><option value="sole_proprietor">Sole proprietor</option><option value="partnership">Partnership</option><option value="llp">Limited liability partnership</option><option value="other">Other</option></select></div>
      {text("industry", "Industry", 100)}
    </div>
    <div><label htmlFor="company-description">Company description <span className="field-optional">Optional</span></label><textarea id="company-description" maxLength={500} rows={3} value={form.description} onChange={e => setForm({ ...form, description: e.target.value })} /><p className="section-help">Shown on documents when a branding theme uses the description.</p></div>
    </fieldset>
    <fieldset disabled={busy || readOnly}><legend>Business contact</legend><div className="form-grid">{text("contact_email", "Contact email", 254, "email")}{text("phone", "Phone", 40, "tel")}{text("mobile", "Mobile", 40, "tel")}{text("fax", "Fax", 40, "tel")}{text("website", "Website (https://…)", 300, "url")}</div></fieldset>
    <fieldset disabled={busy || readOnly}><legend>Social links</legend><div className="form-grid">{text("facebook", "Facebook", 300, "url")}{text("twitter", "X (Twitter)", 300, "url")}{text("linkedin", "LinkedIn", 300, "url")}</div></fieldset>
    <fieldset disabled={busy || readOnly}><legend>Registered address</legend><div className="form-grid">{(Object.keys(form.address) as (keyof Company["address"])[]).map(key => <div key={key}><label htmlFor={`company-address-${key}`}>{{ attention: "Attention / recipient", line1: "Address line 1", line2: "Address line 2", city: "City", region: "State / region", postal_code: "Postal code", country: "Country code" }[key]}</label><input id={`company-address-${key}`} maxLength={key === "country" ? 2 : key === "postal_code" ? 20 : 200} pattern={key === "country" ? "[A-Z]{2}" : key === "postal_code" && form.address.country === "SG" ? "[0-9]{6}" : undefined} value={form.address[key]} onChange={e => setRegistered(key, key === "country" ? e.target.value.toUpperCase() : e.target.value)} /></div>)}</div></fieldset>
    <fieldset disabled={busy || readOnly}><legend>Physical address</legend><p className="section-help">Where the business actually operates, when it differs from the registered address.</p><label className="check-label"><input type="checkbox" checked={samePhysical} onChange={e => toggleSamePhysical(e.target.checked)} />Same as registered address</label>{!samePhysical && <div className="form-grid">{(Object.keys(form.physical_address) as (keyof Company["physical_address"])[]).map(key => <div key={key}><label htmlFor={`company-physical-${key}`}>{{ attention: "Attention / recipient", line1: "Address line 1", line2: "Address line 2", city: "City", region: "State / region", postal_code: "Postal code", country: "Country code" }[key]}</label><input id={`company-physical-${key}`} maxLength={key === "country" ? 2 : key === "postal_code" ? 20 : 200} pattern={key === "country" ? "[A-Z]{2}" : key === "postal_code" && form.physical_address.country === "SG" ? "[0-9]{6}" : undefined} value={form.physical_address[key]} onChange={e => setForm({ ...form, physical_address: { ...form.physical_address, [key]: key === "country" ? e.target.value.toUpperCase() : e.target.value } })} /></div>)}</div>}</fieldset>
    <fieldset disabled={busy || readOnly}><legend>Accounting and tax</legend><p className="section-help">Currency: SGD.</p><div className="form-grid">
      <div><label htmlFor="company-timezone">Time zone</label><select id="company-timezone" value={timezone} onChange={e => setTimezone(e.target.value)}><option value="Asia/Singapore">Asia/Singapore</option><option value="Asia/Kuala_Lumpur">Asia/Kuala_Lumpur</option><option value="Asia/Hong_Kong">Asia/Hong_Kong</option><option value="Asia/Tokyo">Asia/Tokyo</option><option value="Australia/Sydney">Australia/Sydney</option><option value="Europe/London">Europe/London</option><option value="America/New_York">America/New_York</option><option value="UTC">UTC</option></select></div>
      <div><label htmlFor="company-fye-month">Financial year-end month</label><select id="company-fye-month" disabled={companyId != null} value={month} onChange={e => { const next = Number(e.target.value); const last = new Date(Date.UTC(2000, next, 0)).getUTCDate(); setForm({ ...form, fiscal_year_end: `${String(next).padStart(2, "0")}-${String(Math.min(day, last)).padStart(2, "0")}` }); }}>{months.map((name, i) => <option key={name} value={i + 1}>{name}</option>)}</select></div>
      <div><label htmlFor="company-fye-day">Financial year-end day</label><select id="company-fye-day" disabled={companyId != null} value={day} onChange={e => setForm({ ...form, fiscal_year_end: `${String(month).padStart(2, "0")}-${e.target.value.padStart(2, "0")}` })}>{Array.from({ length: days }, (_, i) => <option key={i} value={i + 1}>{i + 1}</option>)}</select></div>
      <div><label htmlFor="company-gst">Registered for GST?</label><select id="company-gst" value={form.gst_registered ? "yes" : "no"} onChange={e => setForm({ ...form, gst_registered: e.target.value === "yes", gst_effective_from: e.target.value === "no" ? (form.gst_registered ? new Date().toISOString().slice(0, 10) : "") : form.gst_effective_from })}><option value="no">No</option><option value="yes">Yes</option></select><p className="section-help">Choose your IRAS registration status. Tidal does not register your business.</p></div>{(form.gst_registered || (companyId != null && initial.gst_registered)) && <div><label htmlFor="company-gst-effective-from">GST {form.gst_registered ? "registration" : "deregistration"} effective from</label><input id="company-gst-effective-from" type="date" required={Boolean(form.gst_effective_from) || (form.gst_registered && !initial.gst_registered) || (!form.gst_registered && initial.gst_registered)} value={form.gst_effective_from} onChange={e => setForm({ ...form, gst_effective_from: e.target.value })} /></div>}{text("gst_number", "GST registration number", 30)}
    </div>{companyId != null && <p className="section-help">Year-end remains fixed. Set registration effective date when registering or deregistering. Deregistration applies from that date; records retain prior history.</p>}</fieldset>
    {error && <p role="alert" className="form-error">{error}</p>}{saved && <p role="status">{bookCompanyId ? "Books created." : "Company saved."}</p>}
    {!readOnly && <div className="form-actions"><button disabled={busy} type="submit">{busy ? "Saving…" : companyId ? "Save company profile" : bookCompanyId ? "Create books" : "Create company"}</button></div>}
  </form>;
}
