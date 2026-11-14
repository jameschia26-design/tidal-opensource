import Link from "next/link";
import PageHeader from "@/components/PageHeader";

export default function Account() {
  return <>
    <PageHeader title="Account" description="Sign-in and password recovery." />
    <h2>Password reset</h2>
    <p>For a verified password account, request a one-time reset code by email. Resetting your password signs out all sessions. Google sign-in accounts manage their password with Google.</p>
    <p><Link href="/forgot">Reset password</Link></p>
    <h2>Sign out</h2>
    <p>Sign out of this session. Managing individual sessions is not available.</p>
    <form method="post" action="/api/v1/auth/logout"><button type="submit" className="button-secondary">Sign out</button></form>
    <p><Link href="/settings/profile">View profile</Link></p>
  </>;
}
