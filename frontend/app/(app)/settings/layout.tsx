// Every /settings screen shares one column, heading rhythm and section spacing.
export default function SettingsLayout({ children }: { children: React.ReactNode }) {
  return <div className="settings-page">{children}</div>;
}
