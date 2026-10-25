// Git glyphs, which Heroicons doesn't include.

export function BranchIcon(props) {
  return <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" aria-hidden="true" {...props}>
    <circle cx="4.5" cy="3.5" r="1.5" /><circle cx="4.5" cy="12.5" r="1.5" /><circle cx="11.5" cy="5.5" r="1.5" /><path d="M4.5 5v6M11.5 7c0 2.5-3 2.5-6.2 4.3" />
  </svg>;
}

export function CommitIcon(props) {
  return <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" aria-hidden="true" {...props}>
    <circle cx="8" cy="8" r="2.5" /><path d="M1 8h4.5M10.5 8H15" />
  </svg>;
}
