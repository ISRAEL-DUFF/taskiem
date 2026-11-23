import { useEffect, useState } from "react";
import { NavLink } from "react-router-dom";
import { get } from "../api";
import { placedPages, type PageList, type PageSummary } from "../lib/pages";

// A page as a tab under an area (docs/pages.md; pages §9): a published page
// whose place is the area shows as a tab after Taskiem's own on that area's
// screens. The area's screens show only while it is on, so its tabs do too.

let read: Promise<PageSummary[]> | undefined;
let readAt = 0;

/** The pages this person may open, read at most once a minute. */
function pagesOnce(): Promise<PageSummary[]> {
  if (!read || Date.now() - readAt > 60_000) {
    readAt = Date.now();
    read = get<PageList>("/v1/pages")
      .then((l) => l.pages)
      .catch(() => []);
  }
  return read;
}

/** The business's pages placed under these areas, as tabs. */
export function AreaPageTabs({ areas }: { areas: string[] }) {
  const [pages, setPages] = useState<PageSummary[]>([]);
  const key = areas.join(",");
  useEffect(() => {
    let live = true;
    void pagesOnce().then((all) => live && setPages(placedPages(all, areas)));
    return () => {
      live = false;
    };
  }, [key]);
  return (
    <>
      {pages.map((p) => (
        <NavLink key={p.name} to={`/pages/${p.name}`} data-testid="area-page-tab">
          {p.title}
        </NavLink>
      ))}
    </>
  );
}
