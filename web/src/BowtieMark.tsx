// The Bowtie logo: a classic UHF "bowtie" antenna on a set-top stand.
// Geometry mirrors docs/brand/bowtie-mark.svg (cropped to the mark's bounds).
export function BowtieMark({ size = 24, className }: { size?: number; className?: string }) {
  return (
    <svg
      className={className}
      width={size * (732 / 674)}
      height={size}
      viewBox="146 159 732 674"
      aria-hidden="true"
      focusable="false"
    >
      <g fill="none" stroke="var(--accent)" strokeWidth="36" strokeLinecap="round">
        <path d="M444 290A100 100 0 0 1 580 290" />
        <path d="M398 222A168 168 0 0 1 626 222" strokeOpacity="0.5" />
      </g>
      <g fill="none" stroke="var(--text-dim)" strokeLinecap="round">
        <path d="M512 470V792" strokeWidth="38" />
        <path d="M368 812Q512 774 656 812" strokeWidth="42" />
      </g>
      <g fill="none" stroke="var(--accent)" strokeWidth="48" strokeLinejoin="round">
        <path d="M482 452L170 290V614Z" />
        <path d="M542 452L854 290V614Z" />
      </g>
      <circle cx="512" cy="452" r="34" fill="var(--text)" />
    </svg>
  )
}
