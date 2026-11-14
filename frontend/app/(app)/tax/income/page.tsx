"use client";
import Link from "next/link";
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import PreviewNotice from "@/components/PreviewNotice";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

// Years come from the server; nothing is hard-coded here.
export default function IncomeTaxPage() {
  const [years, setYears] = useState<{ cit: number[]; pit: number[] } | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api<{ cit: number[]; pit: number[] }>("/v1/tax/years")
      .then(setYears)
      .catch((e: ApiError) => setError(messageFor(e.error, e.status)));
  }, []);

  return (
    <>
      <p className="notice">
        These are worksheets, not filed returns. Years follow the published IRAS rule tables; they have
        not each been cross-checked against an IRAS worked example, so have an accountant review the
        figures before filing.
      </p>
      <PageHeader title="Income tax worksheets" description="Company (Form C-S) and individual (Form B) worksheets, by rule-table year." />
      <PreviewNotice>Worksheets only. Not a filed return and not certified. Full Form C and complex cases need professional review.</PreviewNotice>
      {error && <p role="alert" className="form-error">{error}</p>}
      <ul className="task-list">
        <li><Link href="/tax/income/cit">Company (Form C-S) worksheet</Link>{years && <span className="muted small"> · rule-table years {years.cit.join(", ")}</span>}</li>
        <li><Link href="/tax/income/pit">Individual (Form B) worksheet</Link>{years && <span className="muted small"> · rule-table years {years.pit.join(", ")}</span>}</li>
      </ul>
    </>
  );
}
