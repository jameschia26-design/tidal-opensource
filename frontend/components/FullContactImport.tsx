"use client";
import { useState } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

const TEMPLATE =
  "ContactName,Salutation,FirstName,LastName,EmailAddress,PhoneNumber,MobileNumber,FaxNumber,DirectDial,Country,Website,UEN,GSTNumber,AccountNumber,Role,Notes,BankAccountName,BankAccountNumber,Particulars,BillingLine1,BillingLine2,BillingCity,BillingRegion,BillingPostalCode,BillingCountry,BillingPhone,BillingFax,DeliveryLine1,DeliveryLine2,DeliveryCity,DeliveryRegion,DeliveryPostalCode,DeliveryCountry,DeliveryPhone,DeliveryFax,Person1Salutation,Person1FirstName,Person1LastName,Person1Email,Person1WorkPhone,Person1Mobile\n";
type Preview = { created: number; updated: number; rows: number };

export default function FullContactImport() {
  const { selected } = useOrg();
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState("");
  const [step, setStep] = useState<1 | 2>(1);
  const [wipe, setWipe] = useState(false);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [text, setText] = useState("");
  const canWrite = selected && selected.role !== "viewer";
  async function upload() {
    if (!selected || !file || busy) return;
    setBusy(true);
    setError("");
    setResult("");
    setPreview(null);
    try {
      const body = await file.text();
      setText(body);
      const r = await api<Preview>(
        `/v1/contacts/import?org_id=${selected.id}&preview=1${wipe ? "&delete=1" : ""}`,
        { method: "POST", body, headers: { "content-type": "text/csv" } },
      );
      setPreview(r);
      setStep(2);
    } catch (err) {
      const x = err as ApiError;
      setError(importError(x.error, x.status));
    } finally {
      setBusy(false);
    }
  }
  function importError(code: string, status: number) {
    if (code === "invalid_import")
      return "File not accepted. Check the header row has ContactName, every row has a name, emails and websites are valid, and roles are customer, supplier or both.";
    if (code === "ambiguous_contact_name")
      return "Two contacts already share a name in this file. Rename one of them before importing.";
    return messageFor(code, status);
  }
  async function complete() {
    if (!selected || busy) return;
    setBusy(true);
    setError("");
    try {
      const r = await api<{ created: number; updated: number }>(
        `/v1/contacts/import?org_id=${selected.id}${wipe ? "&delete=1" : ""}`,
        { method: "POST", body: text, headers: { "content-type": "text/csv" } },
      );
      setResult(`${r.created} contact(s) created, ${r.updated} updated.`);
      setPreview(null);
      setStep(1);
      setFile(null);
      setText("");
    } catch (err) {
      const x = err as ApiError;
      setError(messageFor(x.error, x.status));
    } finally {
      setBusy(false);
    }
  }
  function back() {
    setStep(1);
    setPreview(null);
    setText("");
    setError("");
    setResult("");
  }
  return (
    <>
      <PageHeader
        title="Advanced contact import"
        description="Upload a CSV or text file (2 MB, up to 1,000 contacts). Existing contacts with the same name are updated; empty cells are ignored."
        actions={
          <>
            <a
              className="button-secondary"
              href={`data:text/csv;charset=utf-8,${encodeURIComponent(TEMPLATE)}`}
              download="contacts-template.csv"
            >
              Download template
            </a>
            {selected && (
              <a
                className="button-secondary"
                href={`/api/v1/contacts/export?org_id=${selected.id}`}
                download="contacts.csv"
              >
                Export contacts
              </a>
            )}
          </>
        }
      />
      {!selected && <p className="notice">Choose a company first.</p>}
      {error && (
        <p role="alert" className="form-error">
          {error}
        </p>
      )}
      {result && <p role="status">{result}</p>}
      {selected && !canWrite && (
        <p className="notice">Your role allows viewing only.</p>
      )}
      {selected && canWrite && step === 1 && (
        <form
          className="record-form"
          onSubmit={(e) => {
            e.preventDefault();
            void upload();
          }}
        >
          <fieldset disabled={busy}>
            <legend>Step 1: Upload contacts</legend>
            <label htmlFor="contacts-file">
              File to upload (.csv or .txt, required)
            </label>
            <input
              id="contacts-file"
              type="file"
              accept=".csv,.txt,text/csv,text/plain"
              required
              onChange={(e) => setFile(e.target.files?.[0] ?? null)}
            />
            <label className="check-label">
              <input
                type="checkbox"
                checked={wipe}
                onChange={(e) => setWipe(e.target.checked)}
              />{" "}
              Delete existing information
            </label>
            <p className="section-help">
              Off: empty cells in the file leave the contact&apos;s current
              values alone. On: any field the file mentions but leaves empty is
              cleared. Contact names and roles are never cleared.
            </p>
          </fieldset>
          <div className="form-actions">
            <button type="submit" disabled={busy || !file}>
              Check file
            </button>
            <a className="button-secondary" href="/import">
              Cancel
            </a>
          </div>
        </form>
      )}
      {selected && canWrite && step === 2 && preview && (
        <form
          className="record-form"
          onSubmit={(e) => {
            e.preventDefault();
            void complete();
          }}
        >
          <fieldset disabled={busy}>
            <legend>Step 2: Complete import</legend>
            <p className="section-help">
              The file holds {preview.rows} contact(s). {preview.created} will
              be created, {preview.updated} updated.{" "}
              {wipe
                ? "Existing values are cleared where the file leaves a field empty."
                : "Empty cells keep existing values."}{" "}
              Nothing has been written yet.
            </p>

          </fieldset>
          <div className="form-actions">
            <button type="submit" disabled={busy}>
              Complete import
            </button>
            <button
              type="button"
              className="button-secondary"
              disabled={busy}
              onClick={back}
            >
              Back
            </button>
            <a className="button-secondary" href="/import">
              Cancel
            </a>
          </div>
        </form>
      )}
    </>
  );
}
