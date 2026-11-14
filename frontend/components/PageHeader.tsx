import type { ReactNode } from "react";

// One page heading for every screen: title, plain-language description, the period a report covers,
// and the actions that apply to the whole page.
export default function PageHeader({ title, description, period, actions }: { title: string; description?: string; period?: ReactNode; actions?: ReactNode }) {
  return (
    <header className="page-header">
      <div>
        <h1>{title}</h1>
        {description && <p className="muted">{description}</p>}
        {period && <p className="page-period">{period}</p>}
      </div>
      {actions && <div className="page-actions">{actions}</div>}
    </header>
  );
}
