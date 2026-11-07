import { useEffect, useRef, useState } from "react";
import { Modal } from "../ui";

/**
 * The computer's camera, for a photo to send: the live picture, a Take
 * photo button, and the photo comes back as a file. (On a phone the camera
 * is the file input with `capture` instead.) The picture is not recorded or
 * kept anywhere but in the message that is sent.
 */
export function CameraSheet({
  onTake,
  onClose,
}: {
  onTake: (f: File) => void;
  onClose: () => void;
}) {
  const video = useRef<HTMLVideoElement>(null);
  const [error, setError] = useState<string>();
  const [ready, setReady] = useState(false);
  useEffect(() => {
    let stream: MediaStream | undefined;
    let live = true;
    navigator.mediaDevices
      .getUserMedia({ video: { facingMode: "user" }, audio: false })
      .then((s) => {
        if (!live) return s.getTracks().forEach((t) => t.stop());
        stream = s;
        if (video.current) video.current.srcObject = s;
      })
      .catch((e: unknown) => {
        const name = (e as { name?: string })?.name;
        setError(
          name === "NotAllowedError" || name === "SecurityError"
            ? "The browser did not let Taskiem use the camera. Allow it for this site in the browser's settings, or choose a photo instead."
            : name === "NotFoundError"
              ? "No camera was found. Choose a photo instead."
              : "The camera could not be started. Choose a photo instead.",
        );
      });
    return () => {
      live = false;
      stream?.getTracks().forEach((t) => t.stop());
    };
  }, []);
  const take = () => {
    const v = video.current;
    if (!v || !v.videoWidth) return;
    const c = document.createElement("canvas");
    c.width = v.videoWidth;
    c.height = v.videoHeight;
    c.getContext("2d")?.drawImage(v, 0, 0);
    c.toBlob(
      (b) =>
        b &&
        onTake(
          new File([b], `photo-${Date.now()}.jpg`, { type: "image/jpeg" }),
        ),
      "image/jpeg",
      0.9,
    );
  };
  return (
    <Modal title="Take a photo" onClose={onClose}>
      {error ? (
        <p className="hint" role="alert">
          {error}
        </p>
      ) : (
        <video
          ref={video}
          className="camera-video"
          autoPlay
          playsInline
          muted
          aria-label="What the camera sees"
          onLoadedMetadata={() => setReady(true)}
        />
      )}
      <div className="row-actions">
        {!error && (
          <button
            type="button"
            className="primary"
            disabled={!ready}
            onClick={take}
          >
            Take photo
          </button>
        )}
        <button type="button" onClick={onClose}>
          {error ? "Close" : "Cancel"}
        </button>
      </div>
    </Modal>
  );
}
