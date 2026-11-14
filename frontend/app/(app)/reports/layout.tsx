"use client";
import { usePathname } from "next/navigation";
import ReportTools from "@/components/ReportTools";
import { useOrgId } from "@/components/useOrgId";

// Every screen under /reports gets the same print, CSV and column controls, so a new report picks
// them up without repeating the toolbar. The report keeps ownership of its own filters and tables.
export default function ReportsLayout({ children }: { children: React.ReactNode }) {
  const path = usePathname();
  const orgId = useOrgId();
  const slug = path.split("/").filter(Boolean).pop() ?? "report";
  const title = slug.replace(/-/g, " ");
  return <ReportTools title={title} orgId={orgId}>{children}</ReportTools>;
}
