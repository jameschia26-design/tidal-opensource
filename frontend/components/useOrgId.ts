"use client";
import { useOrg } from "./OrgProvider";

// The selected company id, or null when none is chosen. Passed as org_id on every company request.
export function useOrgId(): number | null {
  const { selected } = useOrg();
  return selected?.id ?? null;
}
