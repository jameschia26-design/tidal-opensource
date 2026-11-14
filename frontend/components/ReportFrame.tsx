import type { ReactNode } from "react";
import PageHeader from "./PageHeader";

// Common report page frame: title, plain-language description, the period covered, page actions,
// filter controls, then the body.
export default function ReportFrame({ title, description, period, actions, filters, children }: {
  title: string; description?: string; period?: ReactNode; actions?: ReactNode; filters?: ReactNode; children: ReactNode;
}) {
  return (
    <>
      <PageHeader title={title} description={description} period={period} actions={actions} />
      {filters && <div className="record-toolbar" role="group" aria-label="Report filters">{filters}</div>}
      <section aria-label={title}>{children}</section>
    </>
  );
}
