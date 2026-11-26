import { useEffect, useState } from "react";
import { Icon } from "../icons";
import { fileSize } from "../lib/linkchat";
import { fileLine } from "../lib/chatfiles";
import { FileTypeIcon } from "./ChatFiles";

/**
 * A file waiting for its caption (WhatsApp Web's preview): the picture
 * itself for a photo, the name and size for anything else, and Remove. The
 * words typed in the box below go with it. The picture is read as a data
 * URL, which the app's content policy allows (it allows no blob: URLs).
 */
export function PendingFile({
  file,
  onRemove,
  testid = "chat-attaching",
}: {
  file: File;
  onRemove: () => void;
  testid?: string;
}) {
  const [src, setSrc] = useState<string>();
  useEffect(() => {
    setSrc(undefined);
    if (!file.type.startsWith("image/")) return;
    let live = true;
    const r = new FileReader();
    r.onload = () => live && setSrc(String(r.result));
    r.readAsDataURL(file);
    return () => {
      live = false;
    };
  }, [file]);
  const voice = file.type.startsWith("audio/");
  const photo = file.type.startsWith("image/");
  return (
    <div
      className="pending-file"
      data-testid={testid}
      onKeyDown={(e) => {
        if (e.key === "Escape") onRemove();
      }}
    >
      {photo && src && (
        <img
          src={src}
          alt={`Preview of ${file.name}`}
          className="pending-photo"
        />
      )}
      {voice && (
        <span className="pending-icon" aria-hidden="true">
          <Icon name="mic" />
        </span>
      )}
      {/* A PDF or a document: its type's icon and line, as the chat will
          show it (chat-previews §1.1). */}
      {!voice && !photo && <FileTypeIcon a={{ type: file.type, name: file.name }} />}
      <span className="quote">
        {voice
          ? `Voice note · ${fileSize(file.size)}`
          : photo
            ? `Photo: ${file.name} · ${fileSize(file.size)}`
            : `${file.name} · ${fileLine({ kind: "file", type: file.type, name: file.name, size: file.size })}`}
      </span>
      <button type="button" className="small" onClick={onRemove}>
        Remove<span className="sr-only"> the file</span>
      </button>
    </div>
  );
}
