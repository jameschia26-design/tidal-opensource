"use client";
import { useEffect, useState } from "react";
import RecordImport from "@/components/RecordImport";
import { loadLookups } from "@/lib/import-lookups";
import { importSpecs, type LookupMaps } from "@/lib/imports";

// Loads the lookup lists for one record type, then shows its CSV importer. Mount only for company editors.
export default function RecordImportPanel({ kind, orgId, withInvoices = false, onDone }: { kind: string; orgId: number; withInvoices?: boolean; onDone: () => Promise<void> }) {
  const [lookups, setLookups] = useState<LookupMaps | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    setLookups(null); setError("");
    loadLookups(orgId, withInvoices)
      .then(l => { if (live) setLookups(l); })
      .catch(() => { if (live) setError("Lists for import could not be loaded. Reload the page to try again."); });
    return () => { live = false; };
  }, [orgId, withInvoices]);
  if (error) return <p role="alert" className="form-error">{error}</p>;
  if (!lookups) return <p className="muted">Loading import lists…</p>;
  const spec = importSpecs(lookups)[kind];
  return spec ? <RecordImport spec={spec} orgId={orgId} onDone={onDone} /> : null;
}
