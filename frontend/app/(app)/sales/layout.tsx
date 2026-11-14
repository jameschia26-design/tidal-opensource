"use client";
import { usePathname } from "next/navigation";
import Tabs from "@/components/Tabs";
import { SALES_TABS } from "@/lib/nav";
function pathMatches(pathname: string, href: string) { return pathname === href || pathname.startsWith(href + "/"); }
export default function SalesLayout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  return <>
    <Tabs items={SALES_TABS} current={SALES_TABS.find((tab) => pathMatches(pathname, tab.href))?.href ?? ""} />
    {children}
  </>;
}
