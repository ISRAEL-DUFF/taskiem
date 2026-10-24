// Line icons for the navigation, empty states and icon buttons (24-unit
// grid, drawn with currentColor). Decorative only: the link's or button's
// text, or its aria-label, is its accessible name.
const paths: Record<string, string> = {
  start: "M5 12l4 4L19 6",
  dashboard: "M4 13h6V4H4zm10 7h6V11h-6zM4 20h6v-4H4zm10-11h6V4h-6z",
  workflows: "M6 3v6m0 6v6M6 9a3 3 0 100 6 3 3 0 000-6zm12-6a3 3 0 100 6 3 3 0 000-6zm0 6v3a3 3 0 01-3 3H9",
  templates: "M4 5a1 1 0 011-1h5v16H5a1 1 0 01-1-1zm10-1h5a1 1 0 011 1v5h-6zm0 10h6v5a1 1 0 01-1 1h-5z",
  tables: "M4 5h16v14H4zM4 10h16M4 15h16M10 5v14",
  runs: "M8 5v14l11-7z",
  approvals: "M9 12l2 2 4-4M12 3l7 3v5c0 5-3.5 8.5-7 10-3.5-1.5-7-5-7-10V6z",
  policies: "M4 6h16M4 12h10M4 18h7M17 15l2 2 3-3",
  connections: "M9 15l6-6M10 6l1-1a4 4 0 016 6l-1 1M14 18l-1 1a4 4 0 01-6-6l1-1",
  catalogue: "M3 7l9-4 9 4-9 4zm0 5l9 4 9-4M3 17l9 4 9-4",
  settings: "M12 9a3 3 0 100 6 3 3 0 000-6zm7.4 3a7.4 7.4 0 00-.1-1.2l2-1.6-2-3.4-2.4 1a7 7 0 00-2-1.2L14.5 3h-5l-.4 2.6a7 7 0 00-2 1.2l-2.4-1-2 3.4 2 1.6a7.4 7.4 0 000 2.4l-2 1.6 2 3.4 2.4-1a7 7 0 002 1.2l.4 2.6h5l.4-2.6a7 7 0 002-1.2l2.4 1 2-3.4-2-1.6c.1-.4.1-.8.1-1.2z",
  alerts: "M6 16V11a6 6 0 1112 0v5l2 2H4zm4 4a2 2 0 004 0",
  bell: "M6 16V11a6 6 0 1112 0v5l2 2H4zm4 4a2 2 0 004 0",
  audit: "M9 4h6l1 2h3v15H5V6h3zm0 8h6m-6 4h4",
  reports: "M5 20V10m7 10V4m7 16v-7",
  members: "M9 11a4 4 0 100-8 4 4 0 000 8zm-6 10a6 6 0 0112 0M17 11a3 3 0 100-6m4 16a5 5 0 00-4-5",
  billing: "M3 7a2 2 0 012-2h14a2 2 0 012 2v10a2 2 0 01-2 2H5a2 2 0 01-2-2zm0 3h18M7 15h4",
  keys: "M15 7a4 4 0 11-3.9 5H3v3h3v3h3v-3h2.1A4 4 0 0115 7zm1 3h.01",
  menu: "M4 6h16M4 12h16M4 18h16",
  close: "M6 6l12 12M18 6L6 18",
  search: "M11 18a7 7 0 100-14 7 7 0 000 14zm9 2l-4.3-4.3",
  plus: "M12 5v14M5 12h14",
  forms: "M6 3h9l4 4v14H6zM14 3v5h5M9 12h7M9 16h5",
  inbox: "M4 13l2-8h12l2 8M4 13v6h16v-6M4 13h5l1 2h4l1-2h5",
  storefront: "M4 9l1.5-5h13L20 9M4 9v11h16V9M4 9a2.7 2.7 0 005.3 0 2.7 2.7 0 005.4 0A2.7 2.7 0 0020 9M10 20v-5h4v5",
  home: "M3 10.5L12 3l9 7.5V20a1 1 0 01-1 1h-5v-6H9v6H4a1 1 0 01-1-1z",
  schools: "M2 9l10-5 10 5-10 5zm4 2v5c0 1.7 2.7 3 6 3s6-1.3 6-3v-5M22 9v6",
  activity: "M3 12h4l3-8 4 16 3-8h4",
  orders: "M6 7h12l-1 13H7zM9 7a3 3 0 016 0",
  chevron: "M9 6l6 6-6 6",
  more: "M5 12h.01M12 12h.01M19 12h.01",
  sparkle: "M12 3v4M12 17v4M3 12h4M17 12h4M6 6l2.5 2.5M15.5 15.5L18 18M6 18l2.5-2.5M15.5 8.5L18 6",
  refund: "M9 14L4 9l5-5M4 9h10.5a5.5 5.5 0 010 11H11",
  lock: "M6 11h12v9H6zM8 11V8a4 4 0 018 0v3",
  clock: "M12 7v5l3 2M12 21a9 9 0 100-18 9 9 0 000 18z",
  bolt: "M13 3L5 14h6l-1 7 8-11h-6z",
  invoices: "M6 3h12v18l-3-2-3 2-3-2-3 2zM9 8h6M9 12h6M9 16h3",
  money: "M3 6h18v12H3zM12 9a3 3 0 100 6 3 3 0 000-6zM6.5 9.5v.01M17.5 14.5v.01",
};

export function Icon({ name, className }: { name: string; className?: string }) {
  const d = paths[name];
  if (!d) return null;
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.7} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false" className={className}>
      <path d={d} />
    </svg>
  );
}
