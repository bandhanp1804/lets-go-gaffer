import type { ReactNode } from "react";

/** Pitch markings, drawn in chalk behind the hero. Decorative only. */
function PitchLines() {
  return (
    <svg className="pitchlines" viewBox="0 0 800 420" preserveAspectRatio="xMidYMid slice" aria-hidden="true">
      <g fill="none" stroke="currentColor" strokeWidth="3">
        <rect x="20" y="20" width="760" height="380" rx="4" />
        <line x1="400" y1="20" x2="400" y2="400" />
        <circle cx="400" cy="210" r="62" />
        <circle cx="400" cy="210" r="4" fill="currentColor" />
        <rect x="20" y="110" width="110" height="200" />
        <rect x="20" y="160" width="42" height="100" />
        <path d="M130 170 A62 62 0 0 1 130 250" />
        <rect x="670" y="110" width="110" height="200" />
        <rect x="738" y="160" width="42" height="100" />
        <path d="M670 170 A62 62 0 0 0 670 250" />
        <path d="M20 36 A16 16 0 0 0 36 20" />
        <path d="M764 20 A16 16 0 0 0 780 36" />
        <path d="M20 384 A16 16 0 0 1 36 400" />
        <path d="M764 400 A16 16 0 0 1 780 384" />
      </g>
    </svg>
  );
}

/** A football: white with the classic dark panels. */
export function Ball({ size = 72 }: { size?: number }) {
  return (
    <svg className="ballsvg" width={size} height={size} viewBox="0 0 100 100" aria-hidden="true">
      <circle cx="50" cy="50" r="47" fill="#fff" stroke="#14181f" strokeWidth="3" />
      <polygon points="50,30 68,43 61,64 39,64 32,43" fill="#14181f" />
      <g stroke="#14181f" strokeWidth="3" fill="none" strokeLinecap="round">
        <path d="M50 30 L50 5" />
        <path d="M68 43 L93 36" />
        <path d="M61 64 L76 86" />
        <path d="M39 64 L24 86" />
        <path d="M32 43 L7 36" />
      </g>
      <g fill="#14181f">
        <path d="M50 5 L38 10 L44 20 L56 20 L62 10 Z" opacity=".0" />
        <polygon points="93,36 97,52 86,60 80,48 84,38" />
        <polygon points="7,36 3,52 14,60 20,48 16,38" />
        <polygon points="76,86 66,94 54,92 56,82 68,78" />
        <polygon points="24,86 34,94 46,92 44,82 32,78" />
      </g>
    </svg>
  );
}

export function Hero({ children }: { children: ReactNode }) {
  return (
    <section className="hero">
      <PitchLines />
      <div className="floodlight left" aria-hidden="true" />
      <div className="floodlight right" aria-hidden="true" />
      <div className="herocontent">{children}</div>
      <div className="rollingball" aria-hidden="true">
        <Ball size={56} />
      </div>
    </section>
  );
}
