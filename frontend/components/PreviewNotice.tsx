// Honest label for screens backed by the core release. Not a claim of accuracy or completeness.
export default function PreviewNotice({ children }: { children: React.ReactNode }) {
  return <div className="notice preview" role="note"><strong>Preview.</strong> {children}</div>;
}
