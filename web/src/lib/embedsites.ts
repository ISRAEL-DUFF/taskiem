/** A website allowed to show the business's page (docs/embed-sites.md). */
export interface EmbedSite {
  id: string;
  origin: string;
  method: "dns" | "file";
  token: string;
  verified_at: string | null;
  checked_at?: string;
  last_check_error?: string;
  unverified_at?: string;
  prove: { record_type?: string; record_name?: string; record_short?: string; record_value?: string; file_url?: string; file_contents?: string };
}

/** The code that shows the page on a website: as the designer's email has it (api EmbedSnippet). */
export function pageEmbedSnippet(pageURL: string, business: string): string {
  const title = business.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return `<iframe src="${pageURL.replace(/\/+$/, "")}/embed" title="${title}" style="width:100%;min-height:720px;border:0" loading="lazy"></iframe>`;
}

/** A site's state in words. */
export function siteState(s: EmbedSite): string {
  if (s.verified_at) return "Shows your page.";
  if (s.unverified_at) return "Stopped: the proof was not found for over a day. Put it back, then check again.";
  return "Waiting for the proof below.";
}
