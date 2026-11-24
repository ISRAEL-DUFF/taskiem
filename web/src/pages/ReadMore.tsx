import { useState } from "react";
import { reveal } from "../lib/readmore";

/** A long message folded: what is shown now, and whether more is to read. */
export function useFold(text: string) {
  const [taps, setTaps] = useState(0);
  const r = reveal(text, taps);
  return { ...r, more: () => setTaps((n) => n + 1) };
}

/** "… Read more", at the end of what is shown. */
export function ReadMore({ onClick }: { onClick: () => void }) {
  return (
    <>
      <span aria-hidden="true">… </span>
      <button type="button" className="read-more" onClick={onClick}>
        Read more
      </button>
    </>
  );
}
