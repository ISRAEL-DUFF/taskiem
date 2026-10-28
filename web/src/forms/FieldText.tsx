// A box of text with fields in it, as chips: drop a field from "What came
// in", or press "Add a field" (or type /) and pick one. Kept as the value
// lib/fieldtext.ts says; a value it cannot read was set up by hand.
import { useEffect, useRef, useState } from "react";
import type { Source } from "../lib/plaincanvas";
import { fieldWords, segmentsOf, valueOf, type Segment } from "../lib/fieldtext";

/** The drag type a field carries from "What came in". */
export const FIELD_DRAG = "application/x-taskiem-field";

export function FieldText({ value, onChange, sources, label, multiline = false }: { value: unknown; onChange: (v: string | undefined) => void; sources: Source[]; label: string; multiline?: boolean }) {
  const box = useRef<HTMLDivElement>(null);
  const emitted = useRef<unknown>(Symbol("never"));
  const [menu, setMenu] = useState(false);
  const words = (expr: string) => sources.find((s) => s.expr === expr)?.label.replace(/^From “[^”]*”: /, "").replace(/^What was filled in: /, "") ?? fieldWords(expr);
  const segs = segmentsOf(value, words);

  // The box is drawn from the value only when it changes from outside, so typing keeps the caret.
  useEffect(() => {
    const el = box.current;
    if (!el || !segs || value === emitted.current) return;
    el.replaceChildren(...segs.map((s) => ("text" in s ? document.createTextNode(s.text) : chip(s.expr, s.label))));
  });

  if (segs === null) return <p className="hint">Set up by hand. Turn on Advanced in Settings to change it.</p>;

  const read = (): Segment[] => {
    const out: Segment[] = [];
    const walk = (n: Node) => {
      if (n.nodeType === Node.TEXT_NODE) out.push({ text: n.textContent ?? "" });
      else if (n instanceof HTMLElement && n.dataset.expr) out.push({ expr: n.dataset.expr, label: n.textContent ?? "" });
      else if (n instanceof HTMLElement && n.tagName === "BR") out.push({ text: "\n" });
      else n.childNodes.forEach(walk);
    };
    box.current?.childNodes.forEach(walk);
    // Neighbouring text pieces become one.
    return out.reduce<Segment[]>((acc, s) => {
      const prev = acc[acc.length - 1];
      if (prev && "text" in prev && "text" in s) prev.text += s.text;
      else acc.push({ ...s });
      return acc;
    }, []);
  };
  const emit = () => {
    const v = valueOf(read());
    emitted.current = v;
    onChange(v);
  };
  const insert = (expr: string, at?: Range | null) => {
    const el = box.current;
    if (!el) return;
    const node = chip(expr, words(expr));
    const sel = window.getSelection();
    const range = at ?? (sel && sel.rangeCount && el.contains(sel.anchorNode) ? sel.getRangeAt(0) : null);
    if (range) {
      range.deleteContents();
      range.insertNode(node);
      range.setStartAfter(node);
      range.collapse(true);
      sel?.removeAllRanges();
      sel?.addRange(range);
    } else el.append(node);
    emit();
  };
  return (
    <div className="ftext-wrap">
      <div
        ref={box}
        className={`ftext${multiline ? " multi" : ""}`}
        contentEditable
        suppressContentEditableWarning
        role="textbox"
        aria-multiline={multiline}
        aria-label={label}
        tabIndex={0}
        onInput={emit}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !multiline) e.preventDefault();
          if (e.key === "/" && sources.length) {
            e.preventDefault();
            setMenu(true);
          }
        }}
        onDragOver={(e) => e.dataTransfer.types.includes(FIELD_DRAG) && e.preventDefault()}
        onDrop={(e) => {
          const expr = e.dataTransfer.getData(FIELD_DRAG);
          if (!expr) return;
          e.preventDefault();
          const doc = document as Document & { caretRangeFromPoint?: (x: number, y: number) => Range | null };
          insert(expr, doc.caretRangeFromPoint?.(e.clientX, e.clientY) ?? null);
        }}
      />
      {sources.length > 0 &&
        (menu ? (
          <select
            autoFocus
            aria-label={`Add a field to ${label}`}
            defaultValue=""
            onBlur={() => setMenu(false)}
            onChange={(e) => {
              setMenu(false);
              if (e.target.value) insert(e.target.value);
            }}
          >
            <option value="">Choose a field…</option>
            {sources.map((s) => (
              <option key={s.expr} value={s.expr}>
                {s.label}
              </option>
            ))}
          </select>
        ) : (
          <button type="button" className="link ftext-add" onClick={() => setMenu(true)}>
            Add a field
          </button>
        ))}
    </div>
  );
}

function chip(expr: string, label: string): HTMLElement {
  const s = document.createElement("span");
  s.className = "fchip";
  s.contentEditable = "false";
  s.dataset.expr = expr;
  s.textContent = label;
  return s;
}
