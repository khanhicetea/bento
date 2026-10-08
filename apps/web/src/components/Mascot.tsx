export type Mood = "ok" | "busy" | "alert" | "idle";

const moodLabel: Record<Mood, string> = {
  ok: "Ben, happy",
  busy: "Ben, working",
  alert: "Ben, worried",
  idle: "Ben, asleep",
};

/** Ben, the Bento mascot. His mood always mirrors the state he sits next to (see DESIGN.md §2). */
export function Mascot({ mood, size = 96, decorative = true }: { mood: Mood; size?: number; decorative?: boolean }) {
  return (
    <svg
      className="mascot"
      width={size}
      height={size}
      viewBox="0 0 160 160"
      role={decorative ? undefined : "img"}
      aria-hidden={decorative ? true : undefined}
      aria-label={decorative ? undefined : moodLabel[mood]}
    >
      <ellipse cx="80" cy="148" rx="50" ry="6" fill="#241E1A" opacity="0.12" />
      <line x1="108" y1="44" x2="134" y2="8" stroke="#B78B5C" strokeWidth="5" strokeLinecap="round" />
      <line x1="116" y1="46" x2="148" y2="16" stroke="#B78B5C" strokeWidth="5" strokeLinecap="round" />
      <rect x="38" y="126" width="22" height="16" rx="7" fill="#8E2B1F" />
      <rect x="100" y="126" width="22" height="16" rx="7" fill="#8E2B1F" />
      <rect x="12" y="34" width="136" height="102" rx="30" fill="#D0402A" />
      <rect x="28" y="38" width="80" height="5" rx="2.5" fill="#E8735A" />
      <rect x="22" y="46" width="74" height="80" rx="20" fill="#FFF8EA" />
      <rect x="102" y="46" width="36" height="38" rx="13" fill="#7FA35B" />
      <path d="M112 70q8-14 18-12q-2 12-18 12z" fill="#A9C784" />
      <rect x="102" y="88" width="36" height="38" rx="13" fill="#F2C14E" />
      <path d="M110 107h20M110 115h14" stroke="#E0A92E" strokeWidth="3" strokeLinecap="round" />
      <ellipse cx="36" cy="98" rx="7" ry="4" fill="#F4A7A0" />
      <ellipse cx="82" cy="98" rx="7" ry="4" fill="#F4A7A0" />
      {mood === "ok" && (
        <g>
          <circle cx="46" cy="84" r="6" fill="#241E1A" />
          <circle cx="72" cy="84" r="6" fill="#241E1A" />
          <circle cx="48" cy="82" r="2" fill="#FFFFFF" />
          <circle cx="74" cy="82" r="2" fill="#FFFFFF" />
          <path d="M51 98q8 8 16 0" stroke="#241E1A" strokeWidth="3.5" fill="none" strokeLinecap="round" />
        </g>
      )}
      {mood === "busy" && (
        <g>
          <circle cx="49" cy="82" r="6" fill="#241E1A" />
          <circle cx="75" cy="82" r="6" fill="#241E1A" />
          <circle cx="51" cy="80" r="2" fill="#FFFFFF" />
          <circle cx="77" cy="80" r="2" fill="#FFFFFF" />
          <circle cx="61" cy="101" r="4" fill="none" stroke="#241E1A" strokeWidth="3" />
          <path d="M88 58q5 7 0 10q-5-3 0-10z" fill="#8DB6DF" />
          <path d="M140 28l6-4M146 36h7" stroke="#B78B5C" strokeWidth="3" strokeLinecap="round" />
        </g>
      )}
      {mood === "alert" && (
        <g>
          <path d="M38 72l12 4M80 72l-12 4" stroke="#241E1A" strokeWidth="3.5" strokeLinecap="round" />
          <circle cx="46" cy="86" r="5.5" fill="#241E1A" />
          <circle cx="72" cy="86" r="5.5" fill="#241E1A" />
          <path d="M51 104q8-7 16 0" stroke="#241E1A" strokeWidth="3.5" fill="none" strokeLinecap="round" />
          <circle cx="22" cy="24" r="15" fill="#F2C14E" stroke="#241E1A" strokeWidth="3" />
          <path d="M22 15v11" stroke="#241E1A" strokeWidth="4" strokeLinecap="round" />
          <circle cx="22" cy="32" r="2.4" fill="#241E1A" />
        </g>
      )}
      {mood === "idle" && (
        <g>
          <path
            d="M39 85q7 5 14 0M65 85q7 5 14 0"
            stroke="#241E1A"
            strokeWidth="3.5"
            fill="none"
            strokeLinecap="round"
          />
          <path d="M55 101h12" stroke="#241E1A" strokeWidth="3.5" strokeLinecap="round" />
          <path
            d="M14 14h12l-12 13h12M32 4h8l-8 9h8"
            stroke="#7A7166"
            strokeWidth="3"
            fill="none"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </g>
      )}
    </svg>
  );
}

/** The Bento mark: `public/bento-logo.svg` inline so it needs no request. */
export function BentoMark({ size = 36 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 48 48" aria-hidden="true">
      <rect x="2" y="6" width="44" height="36" rx="11" fill="#D0402A" />
      <rect x="6" y="10" width="23" height="28" rx="7" fill="#FFF8EA" />
      <rect x="32" y="10" width="10" height="13" rx="4" fill="#7FA35B" />
      <rect x="32" y="26" width="10" height="12" rx="4" fill="#F2C14E" />
      <circle cx="13.5" cy="22.5" r="2.3" fill="#241E1A" />
      <circle cx="21.5" cy="22.5" r="2.3" fill="#241E1A" />
      <path d="M14.8 28.5q2.7 2.8 5.4 0" stroke="#241E1A" strokeWidth="1.9" fill="none" strokeLinecap="round" />
    </svg>
  );
}
