"use client";
import ContactsPage from "@/components/ContactsPage";
import { useOrg } from "@/components/OrgProvider";

export default function CustomersPage() {
  return <ContactsPage role="customer" />;
}
