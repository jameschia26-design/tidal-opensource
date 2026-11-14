"use client";
import PageHeader from "@/components/PageHeader";
import PreviewNotice from "@/components/PreviewNotice";
import InvoiceForm from "@/components/InvoiceForm";
import { useOrg } from "@/components/OrgProvider";

export default function NewInvoice() {
  const { selected } = useOrg();
  return <>
    <PageHeader title="New invoice" description="Choose a customer, add dates and items, then save a draft. Posting happens separately." />
    <PreviewNotice>GST invoices: choose SR on a line for 9% GST. Quotes, credit notes, sales receipts, purchase orders, recurring invoices and billable-expense invoices offer optional per-line GST too.</PreviewNotice>
    {!selected ? <p className="notice">Choose a company first.</p> : selected.role === "viewer" ? <p className="notice">Your role allows viewing only.</p> : <InvoiceForm key={selected.id} orgId={selected.id} />}
  </>;
}
