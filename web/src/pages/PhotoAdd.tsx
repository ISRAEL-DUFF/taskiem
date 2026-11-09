import { useRef, useState } from "react";
import { Icon } from "../icons";
import { KitField } from "../kit";
import { isTouch, linkProblem, type PhotoPick } from "../lib/photos";
import { CameraSheet } from "./CameraSheet";

// Product photos from the chat (docs/spec/chat-categories-and-variants.md,
// Part C): three ways to add one, the same wherever a product's photo is
// set in the chat (Add product, the product form, the Tables tool): the
// camera, the device's pictures, or a link Taskiem fetches once.

export function PhotoAdd({
  onPick,
  room,
  busy,
}: {
  onPick: (picks: PhotoPick[]) => void;
  /** How many more the product can hold. */
  room: number;
  busy?: boolean;
}) {
  const [camera, setCamera] = useState(false);
  const [linking, setLinking] = useState(false);
  const [link, setLink] = useState("");
  const [problem, setProblem] = useState<string>();
  const pick = useRef<HTMLInputElement>(null);
  const snap = useRef<HTMLInputElement>(null);
  const touch = isTouch();
  if (room <= 0)
    return (
      <p className="hint tk-photo-full">
        This product has all the photos it can hold. Remove one to add another.
      </p>
    );
  const files = (list: FileList | null) => {
    const got = Array.from(list ?? []).slice(0, room);
    if (got.length) onPick(got.map((file) => ({ file })));
  };
  const addLink = () => {
    const bad = linkProblem(link);
    setProblem(bad ?? undefined);
    if (bad) return;
    onPick([{ url: link.trim() }]);
    setLink("");
    setLinking(false);
  };
  return (
    <div className="tk-photo-ways-wrap">
      <div className="tk-photo-ways" role="group" aria-label="Add a photo">
        <button
          type="button"
          className="tk-btn soft"
          disabled={busy}
          onClick={() =>
            // A phone opens its camera app; a computer its webcam, here.
            !touch && typeof navigator.mediaDevices?.getUserMedia === "function"
              ? setCamera(true)
              : snap.current?.click()
          }
        >
          <Icon name="camera" />
          Take a photo
        </button>
        <button
          type="button"
          className="tk-btn soft"
          disabled={busy}
          onClick={() => pick.current?.click()}
        >
          <Icon name="image" />
          {touch ? "From your phone" : "From your computer"}
        </button>
        <button
          type="button"
          className="tk-btn soft"
          disabled={busy}
          aria-expanded={linking}
          onClick={() => {
            setLinking(!linking);
            setProblem(undefined);
          }}
        >
          <Icon name="link" />
          Paste a link
        </button>
      </div>
      {linking && (
        <div className="tk-photo-link">
          <KitField
            label="Link to a picture"
            hint="Taskiem keeps its own copy, so it stays on your page."
            error={problem}
          >
            <input
              type="url"
              inputMode="url"
              autoComplete="off"
              placeholder="https://"
              value={link}
              onChange={(e) => setLink(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  addLink();
                }
              }}
            />
          </KitField>
          <button
            type="button"
            className="tk-btn primary"
            disabled={busy}
            onClick={addLink}
          >
            Add this photo
          </button>
        </div>
      )}
      <input
        ref={pick}
        type="file"
        accept="image/jpeg,image/png,image/webp"
        multiple
        hidden
        data-testid="photo-files"
        onChange={(e) => {
          files(e.target.files);
          e.target.value = "";
        }}
      />
      <input
        ref={snap}
        type="file"
        accept="image/jpeg,image/png,image/webp"
        capture="environment"
        hidden
        onChange={(e) => {
          files(e.target.files);
          e.target.value = "";
        }}
      />
      {camera && (
        <CameraSheet
          facing="environment"
          onClose={() => setCamera(false)}
          onTake={(file) => {
            setCamera(false);
            onPick([{ file }]);
          }}
        />
      )}
    </div>
  );
}
