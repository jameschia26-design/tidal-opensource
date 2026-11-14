"use client";
import { useRouter } from "next/navigation";
import PageHeader from "@/components/PageHeader";
import CompanyForm, { blankCompany } from "@/components/CompanyForm";
import { useOrg } from "@/components/OrgProvider";

export default function Onboarding() {
  const router = useRouter();
  const { refresh } = useOrg();
  return <>
    <PageHeader title="Set up your company" description="Start with business identity, then contact details, address and accounting settings. Optional details can be added later." />
    <CompanyForm initial={blankCompany()} onSaved={async () => { await refresh(); router.push("/overview"); }} />
  </>;
}
