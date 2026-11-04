import {
  useEffect,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyEvent,
} from "react";
import { Icon } from "../icons";

/**
 * The clip: a small menu of what to send (WhatsApp Web's): a photo, a
 * document, or the camera. It only says which; the composer does the rest.
 */
export function AttachMenu({
  onChoose,
  documents = true,
}: {
  onChoose: (what: "photo" | "document" | "camera") => void;
  /** A document may be attached (a business's chat, not Taskiem's). */
  documents?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLSpanElement>(null);
  const clip = useRef<HTMLButtonElement>(null);
  const items = () =>
    Array.from(
      box.current?.querySelectorAll<HTMLButtonElement>(".attach-menu button") ??
        [],
    );
  // Opened by the keyboard or a click, focus goes to its first item; Esc
  // closes it and gives focus back to the clip.
  useEffect(() => {
    if (!open) return;
    items()[0]?.focus();
    const esc = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      setOpen(false);
      clip.current?.focus();
    };
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
  const arrows = (e: ReactKeyEvent) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    e.preventDefault();
    const list = items();
    const here = list.findIndex((b) => b === document.activeElement);
    const step = e.key === "ArrowDown" ? 1 : list.length - 1;
    list[(here + step) % list.length]?.focus();
  };
  return (
    <span className="attach-wrap" ref={box}>
      <button
        ref={clip}
        type="button"
        className="icon-button"
        aria-label="Attach"
        title={
          documents
            ? "Attach a photo, a document or take a photo"
            : "Attach a photo: with its name and price as the caption, it is added as a product"
        }
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
          onKeyDown={arrows}
        >
          <button type="button" onClick={() => choose("photo")}>
            <Icon name="image" />
            Photo
          </button>
          {documents && (
            <button type="button" onClick={() => choose("document")}>
              <Icon name="file" />
              Document
            </button>
          )}
          <button type="button" onClick={() => choose("camera")}>
            <Icon name="camera" />
            Camera
          </button>
        </span>
      )}
    </span>
  );
}
