// Line icons for the navigation (24-unit grid, drawn with currentColor).
// Decorative only: the link's text is its accessible name.
const paths: Record<string, string> = {
  start: "M5 12l4 4L19 6",
  dashboard: "M4 13h6V4H4zm10 7h6V11h-6zM4 20h6v-4H4zm10-11h6V4h-6z",
  workflows: "M6 3v6m0 6v6M6 9a3 3 0 100 6 3 3 0 000-6zm12-6a3 3 0 100 6 3 3 0 000-6zm0 6v3a3 3 0 01-3 3H9",
  templates: "M4 5a1 1 0 011-1h5v16H5a1 1 0 01-1-1zm10-1h5a1 1 0 011 1v5h-6zm0 10h6v5a1 1 0 01-1 1h-5z",
  runs: "M8 5v14l11-7z",
  approvals: "M9 12l2 2 4-4M12 3l7 3v5c0 5-3.5 8.5-7 10-3.5-1.5-7-5-7-10V6z",
  policies: "M4 6h16M4 12h10M4 18h7M17 15l2 2 3-3",
  connections: "M9 15l6-6M10 6l1-1a4 4 0 016 6l-1 1M14 18l-1 1a4 4 0 01-6-6l1-1",
  catalogue: "M3 7l9-4 9 4-9 4zm0 5l9 4 9-4M3 17l9 4 9-4",
  settings: "M12 9a3 3 0 100 6 3 3 0 000-6zm7.4 3a7.4 7.4 0 00-.1-1.2l2-1.6-2-3.4-2.4 1a7 7 0 00-2-1.2L14.5 3h-5l-.4 2.6a7 7 0 00-2 1.2l-2.4-1-2 3.4 2 1.6a7.4 7.4 0 000 2.4l-2 1.6 2 3.4 2.4-1a7 7 0 002 1.2l.4 2.6h5l.4-2.6a7 7 0 002-1.2l2.4 1 2-3.4-2-1.6c.1-.4.1-.8.1-1.2z",
  alerts: "M6 16V11a6 6 0 1112 0v5l2 2H4zm4 4a2 2 0 004 0",
  audit: "M9 4h6l1 2h3v15H5V6h3zm0 8h6m-6 4h4",
  reports: "M5 20V10m7 10V4m7 16v-7",
  members: "M9 11a4 4 0 100-8 4 4 0 000 8zm-6 10a6 6 0 0112 0M17 11a3 3 0 100-6m4 16a5 5 0 00-4-5",
  billing: "M3 7a2 2 0 012-2h14a2 2 0 012 2v10a2 2 0 01-2 2H5a2 2 0 01-2-2zm0 3h18M7 15h4",
  keys: "M15 7a4 4 0 11-3.9 5H3v3h3v3h3v-3h2.1A4 4 0 0115 7zm1 3h.01",
};

export function Icon({ name }: { name: string }) {
  const d = paths[name];
  if (!d) return null;
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.7} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
      <path d={d} />
    </svg>
  );
}
