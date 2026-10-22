import { useState } from "react";
import { post } from "../api";
import { ErrorBox, useAction } from "../ui";

/** A spreadsheet as Taskiem read it (POST /v1/sheets/resolve). */
export interface Sheet {
  spreadsheet_id: string;
  title: string;
  tabs: string[];
}

/** The id in a pasted Google Sheets link, or the value when it is one already. */
export function sheetIdOf(link: string): string {
  const m = /(?:\/spreadsheets\/d\/|^)([A-Za-z0-9_-]{20,})/.exec(link.trim());
  return m?.[1] ?? "";
}

/**
 * Chooses a spreadsheet by pasting its link from the browser
 * (no-technical-steps N2): Taskiem opens it with the business's Google
 * Sheets connection and shows its name, so nobody copies an id. onSheet
 * gets its tabs too, for a tab picker.
 */
export function SheetChooser({ value, onChange, connection = "", onSheet }: { value: string; onChange: (id: string) => void; connection?: string; onSheet?: (s: Sheet) => void }) {
  const [link, setLink] = useState("");
  const [sheet, setSheet] = useState<Sheet>();
  const act = useAction();
  const open = (text: string) =>
    act.run(async () => {
      const s = await post<Sheet>("/v1/sheets/resolve", { link: text, connection });
      setSheet(s);
      onChange(s.spreadsheet_id);
      onSheet?.(s);
    }, false);
  return (
    <div>
      {sheet ? (
        <p role="status">
          <strong>{sheet.title || "Your spreadsheet"}</strong> ({sheet.tabs.length} tab{sheet.tabs.length === 1 ? "" : "s"}){" "}
          <button type="button" className="link" onClick={() => (setSheet(undefined), setLink(""))}>
            Choose another
          </button>
        </p>
      ) : (
        <span className="toolbar">
          <input
            aria-label="The spreadsheet's link"
            placeholder="Paste the sheet's link from your browser"
            value={link || value}
            onChange={(e) => {
              setLink(e.target.value);
              const id = sheetIdOf(e.target.value);
              if (id) onChange(id);
            }}
          />
          <button type="button" className="small" disabled={act.busy || !sheetIdOf(link || value)} onClick={() => void open(link || value)}>
            {act.busy ? "Opening…" : "Check it"}
          </button>
        </span>
      )}
      <ErrorBox error={act.error} />
    </div>
  );
}
