import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Icon } from "../icons";
import { fmtTime, useFocusTrap } from "../ui";
import { fileFamily, fileLine, frameOf, hasPicture, type FileFacts } from "../lib/chatfiles";

// A chat's file in its bubble (docs/spec/chat-previews-and-editing.md §1):
// a PDF with the picture of its first page, opening Taskiem's viewer; a
// document with its type's icon and colour, downloaded; a photo at its own
// proportions before it has loaded.

/** The tile of a file's type: its icon in its family's colour. */
export function FileTypeIcon({ a }: { a: Pick<FileFacts, "type" | "doc" | "name"> }) {
  return (
    <span className={`file-type file-${fileFamily(a)}`} aria-hidden="true">
      <Icon name="file" />
    </span>
  );
}

/** A file that is not a photo or a voice note: a PDF (its picture, then
 * its name and "PDF · 3 pages · 1.2 MB"), or a document ("Excel · 84 KB").
 * src is this side's copy; a file still being sent has none. */
export function FileCard({ a, src, who, at }: { a: FileFacts; src?: string; who?: string; at?: string }) {
  const [open, setOpen] = useState(false);
  const [broken, setBroken] = useState(false);
  const name = a.name || (fileFamily(a) === "pdf" ? "PDF" : "File");
  const pdf = fileFamily(a) === "pdf";
  const picture = pdf && src && hasPicture(a) && !broken;
  const frame = frameOf(a);
  const body = (
    <>
      {picture && (
        <span className="file-picture" style={frame ? { aspectRatio: `${frame.width} / ${frame.height}` } : undefined}>
          <img src={`${src}?preview=1`} alt="" loading="lazy" onError={() => setBroken(true)} />
        </span>
      )}
      <span className="file-row">
        {!picture && <FileTypeIcon a={a} />}
        <span className="file-words">
          <span className="name">{name}</span>
          <span className="hint">{fileLine(a)}</span>
        </span>
      </span>
    </>
  );
  if (!src)
    return (
      <span className="chat-file chat-doc" data-testid="chat-file-card">
        {body}
      </span>
    );
  if (!pdf)
    return (
      <a href={src} download={a.name || true} className="chat-file chat-doc" data-testid="chat-file-card">
        <span className="sr-only">Download </span>
        {body}
      </a>
    );
  return (
    <>
      <button type="button" className="chat-file chat-doc chat-pdf" data-testid="chat-file-card" onClick={() => setOpen(true)}>
        <span className="sr-only">Open </span>
        {body}
      </button>
      {open && <PdfViewer name={name} src={src} who={who} at={at} onClose={() => setOpen(false)} />}
    </>
  );
}

/** Taskiem's PDF viewer, over the chat: the browser's own PDF reader in a
 * sandboxed frame (?view=1), the file's name, who sent it and when in its
 * head, Download and Close; Escape closes it. A browser that will not show
 * a PDF in a frame has "Open in a new tab". */
export function PdfViewer({ name, src, who, at, onClose }: { name: string; src: string; who?: string; at?: string; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const id = useId();
  useFocusTrap(ref, true, onClose);
  // Some phone browsers draw no PDF inside a page: they are offered the
  // tab and the download instead of an empty frame.
  const [framed] = useState(() => typeof navigator === "undefined" || navigator.pdfViewerEnabled !== false);
  useEffect(() => {
    const was = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    // The viewer takes Escape (it closes it): the chat behind does not
    // close too, as it would for a key nothing took.
    const take = (e: KeyboardEvent) => e.key === "Escape" && e.preventDefault();
    document.addEventListener("keydown", take, true);
    return () => {
      document.body.style.overflow = was;
      document.removeEventListener("keydown", take, true);
    };
  }, []);
  const view = `${src}?view=1`;
  // Over the whole page, never inside the bubble it was opened from.
  return createPortal(
    <div className="pdf-viewer-backdrop" data-testid="pdf-viewer">
      <div className="pdf-viewer" role="dialog" aria-modal="true" aria-labelledby={id} tabIndex={-1} ref={ref}>
        <div className="pdf-viewer-head">
          <div className="pdf-viewer-title">
            <h2 id={id}>{name}</h2>
            {(who || at) && <p className="hint">{[who, at ? fmtTime(at) : ""].filter(Boolean).join(" · ")}</p>}
          </div>
          <div className="pdf-viewer-actions">
            <a className="button" href={view} target="_blank" rel="noopener">
              Open in a new tab
            </a>
            <a className="button" href={src} download={name}>
              Download
            </a>
            <button type="button" className="primary" onClick={onClose}>
              Close
            </button>
          </div>
        </div>
        {framed ? (
          <iframe className="pdf-viewer-frame" src={view} title={name} sandbox="" />
        ) : (
          <p className="pdf-viewer-none">This browser cannot show the PDF here. Open it in a new tab, or download it.</p>
        )}
      </div>
    </div>,
    document.body,
  );
}
