import { useEffect, useRef, useState } from "react";
import { Icon } from "../icons";

/**
 * The clip: a small menu of what to send (WhatsApp Web's): a photo, a
 * document, or the camera. It only says which; the composer does the rest.
 */
export function AttachMenu({
  onChoose,
}: {
  onChoose: (what: "photo" | "document" | "camera") => void;
}) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    if (!open) return;
    const esc = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    const away = (e: MouseEvent) =>
      !box.current?.contains(e.target as Node) && setOpen(false);
    document.addEventListener("keydown", esc);
    document.addEventListener("mousedown", away);
    return () => {
      document.removeEventListener("keydown", esc);
      document.removeEventListener("mousedown", away);
    };
  }, [open]);
  const choose = (what: "photo" | "document" | "camera") => {
    setOpen(false);
    onChoose(what);
  };
  return (
    <span className="attach-wrap" ref={box}>
      <button
        type="button"
        className="icon-button"
        aria-label="Attach"
        title="Attach a photo, a document or take a photo"
        aria-expanded={open}
        aria-haspopup="true"
        data-testid="attach"
        onClick={() => setOpen(!open)}
      >
        <Icon name="attach" />
      </button>
      {open && (
        <span
          className="attach-menu"
          data-testid="attach-menu"
          role="group"
          aria-label="Attach"
        >
          <button type="button" onClick={() => choose("photo")}>
            <Icon name="image" />
            Photo
          </button>
          <button type="button" onClick={() => choose("document")}>
            <Icon name="file" />
            Document
          </button>
          <button type="button" onClick={() => choose("camera")}>
            <Icon name="camera" />
            Camera
          </button>
        </span>
      )}
    </span>
  );
}
