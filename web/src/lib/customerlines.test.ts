import { describe, expect, it } from "vitest";
import { appThreadLine } from "./customerlinks";
import {
  bothLines,
  chosenLine,
  lineClosed,
  lineNote,
  lineStates,
  lineWords,
  listMark,
  replyLineBody,
  stoppedPreview,
  threadLines,
  type CustomerThreadLine,
} from "./customerlines";

const wa = (x: Partial<CustomerThreadLine> = {}): CustomerThreadLine => ({
  line: "whatsapp",
  id: "w",
  phone: "+2348031112222",
  window_open: true,
  window_until: null,
  stopped: false,
  blocked: false,
  last_in_at: null,
  ...x,
});
const app = (x: Partial<CustomerThreadLine> = {}): CustomerThreadLine => wa({ line: "app", id: "a", ...x });
const thread = (lines: CustomerThreadLine[], reply_line: "whatsapp" | "app" = "app") => ({
  id: "a",
  line: "app" as const,
  phone: "+2348031112222",
  window_open: true,
  window_until: null,
  stopped: false,
  blocked: false,
  lines,
  reply_line,
});

describe("one customer on both lines", () => {
  it("reads a thread's lines, or the one a thread had before there were two", () => {
    expect(threadLines(thread([app(), wa()]))).toHaveLength(2);
    const old = { line: "whatsapp" as const, phone: "+2348031112222", window_open: false, window_until: null, stopped: true };
    expect(threadLines(old)).toEqual([expect.objectContaining({ line: "whatsapp", stopped: true, window_open: false, blocked: false })]);
    expect(bothLines(old)).toBe(false);
    expect(bothLines(thread([app(), wa()]))).toBe(true);
  });

  it("names where a message went", () => {
    expect(lineWords("whatsapp")).toBe("on WhatsApp");
    expect(lineWords("app")).toBe("in the app");
  });

  it("says why a line cannot take a reply, never for WhatsApp's 24 hours", () => {
    expect(lineClosed(wa())).toBeNull();
    expect(lineClosed(wa({ window_open: false }))).toBeNull();
    expect(lineClosed(wa({ stopped: true }))).toBe("They sent STOP on WhatsApp");
    expect(lineClosed(wa({ phone: "" }))).toBe("Their details were erased");
    expect(lineClosed(app())).toBeNull();
    expect(lineClosed(app({ blocked: true, window_open: false }))).toBe("You blocked them in the app");
    expect(lineClosed(app({ stopped: true, notice: "This customer left Taskiem" }))).toBe("This customer left Taskiem");
    expect(lineClosed(app({ stopped: true }))).toBe("They stopped messages in the app");
  });

  it("keeps the line chosen while it is open, else where the server says", () => {
    const t = thread([app(), wa()], "app");
    expect(chosenLine(t, undefined)).toBe("app");
    expect(chosenLine(t, "whatsapp")).toBe("whatsapp");
    const stopped = thread([app(), wa({ stopped: true })], "app");
    expect(chosenLine(stopped, "whatsapp")).toBe("app");
    expect(chosenLine({ line: "whatsapp", phone: "", window_open: false, window_until: null, stopped: false }, undefined)).toBe("whatsapp");
  });

  it("says how a reply goes on the line chosen", () => {
    expect(lineNote(wa())).toBeNull();
    expect(lineNote(wa({ window_open: false }))?.text).toBe("More than a day since they last wrote: your reply goes as a WhatsApp message they answer to carry on.");
    expect(lineNote(wa({ stopped: true }))?.tone).toBe("stop");
    expect(lineNote(wa({ notice: "This customer changed their number" }))).toEqual({ tone: "warn", text: "This customer changed their number." });
    expect(lineNote(app())).toBeNull();
    expect(lineNote(app({ blocked: true }))?.text).toBe("You blocked this customer. Unblock them to write to them.");
    expect(lineNote(app({ phone: "" }))?.text).toBe("This customer's details were erased.");
  });

  it("tells each line's state for a thread with both", () => {
    expect(lineStates(thread([app(), wa()]))).toEqual([]);
    expect(lineStates(thread([app({ blocked: true, notice: "You blocked this customer in the app" }), wa({ stopped: true, notice: "This customer sent STOP on WhatsApp" })]))).toEqual([
      { line: "app", text: "You blocked this customer in the app." },
      { line: "whatsapp", text: "This customer sent STOP on WhatsApp." },
    ]);
    // One line: the box's note says it.
    expect(lineStates(thread([app({ blocked: true, notice: "x" })]))).toEqual([]);
  });

  it("marks the list's row and its preview", () => {
    expect(listMark(thread([app(), wa()]))).toBe("WhatsApp · App");
    expect(listMark({ line: "app", phone: "", window_open: true, window_until: null, stopped: false })).toBe("in the app");
    expect(listMark({ line: "whatsapp", phone: "", window_open: true, window_until: null, stopped: false })).toBe("");
    expect(stoppedPreview(thread([app(), wa()], "whatsapp"))).toBe("Sent STOP");
    expect(stoppedPreview(thread([app(), wa()], "app"))).toBe("Stopped messages");
  });

  it("sends the line only for a thread with both", () => {
    expect(replyLineBody(thread([app(), wa()]), "whatsapp")).toEqual({ line: "whatsapp" });
    expect(replyLineBody(thread([app()]), "app")).toEqual({});
  });

  it("says a customer is on both under their name", () => {
    expect(appThreadLine({ line: "app", lines: [1, 2] })).toBe("On WhatsApp and in the app");
    expect(appThreadLine({ line: "app", lines: [1, 2], typing: true })).toBe("typing…");
    expect(appThreadLine({ line: "app", lines: [1] })).toBe("Customer in the app");
  });
});
