// Menus in the Taskiem chat (docs/spec/chat-menus.md §6): a message of
// kind "menu" drawn as a card from the person's own catalogue
// (GET /v1/chat/actions). A topic's menu shows its first five actions as
// rows, the rest behind "All N" in a sheet; the topics are tiles. A row
// shows its answer (sent back as "act:<id>"), opens its form or its page
// here, or starts its questions in the chat. Once a row is pressed, its ✕
// closes it, the person says something else, or a newer menu comes, the
// card folds to one line saying what happened (no "Show again": 21
// November 2026).

import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { useNavigate } from "react-router-dom";
import { get, post } from "../api";
import { KitField, Sheet } from "../kit";
import { FormFrame } from "./FormFrame";
import { Icon } from "../icons";
import type { ToolKey } from "../lib/tools";

export interface ChatField {
  key: string;
  label: string;
  hint?: string;
  kind: string;
  choices?: string[];
  optional?: boolean;
}

export interface ChatAction {
  id: string;
  title: string;
  line: string;
  icon: string;
  does: "show" | "asks" | "changes";
  opens: "show" | "form" | "questions" | "page";
  shortcut?: string;
  tool?: string;
  add?: boolean;
  page?: string;
  fields?: ChatField[];
}

export interface ChatTopic {
  id: string;
  name: string;
  icon: string;
  color: string;
  actions: ChatAction[];
}

export interface MenuMessage {
  topic?: string;
  line?: string;
  pick?: string;
  values?: Record<string, string>;
  tip?: string;
  /** How it ended, as the server kept it. */
  done?: "chose" | "closed" | "passed";
  /** The action chosen, when it ended so. */
  chose?: string;
}

/** A menu message's JSON, or undefined when it is not one. */
export function parseMenu(text: string): MenuMessage | undefined {
  try {
    const m = JSON.parse(text) as unknown;
    return m && typeof m === "object" ? (m as MenuMessage) : undefined;
  } catch {
    return undefined;
  }
}

/** At most this many of a topic's rows show in the chat (§6.1). */
export const MENU_ROWS = 5;

// The catalogue's icons, as this app draws them.
const ICON: Record<string, string> = {
  sell: "money", stock: "store", products: "box", services: "clock", prices: "tag", orders: "invoices", tables: "tables",
  purchases: "orders", bills: "money", invoices: "invoices", money: "money", rider: "send", storefront: "storefront",
  bookings: "calendar", customers: "card", tasks: "check", approvals: "approvals", workflows: "workflows", saved: "star",
  school: "schools", you: "settings", handover: "clock",
};
export const menuIcon = (name: string) => ICON[name] ?? "more";

let catalogue: { at: number; topics: Promise<ChatTopic[]> } | undefined;

/** How long a read catalogue serves new menus: a part of the business
 * turned off, a role changed or the most used reordered shows within it. */
const FRESH_MS = 60_000;

/** The person's catalogue, read again once a minute old; undefined while
 * it comes, null when it could not be read (with a way to try again). */
export function useCatalogue(): { topics: ChatTopic[] | undefined | null; retry: () => void } {
  const [topics, setTopics] = useState<ChatTopic[] | null>();
  const [tries, setTries] = useState(0);
  useEffect(() => {
    if (!catalogue || Date.now() - catalogue.at > FRESH_MS)
      catalogue = { at: Date.now(), topics: get<{ topics: ChatTopic[] }>("/v1/chat/actions").then((r) => r.topics ?? []) };
    let live = true;
    catalogue.topics.then(
      (t) => live && setTopics(t),
      () => {
        catalogue = undefined;
        if (live) setTopics(null);
      },
    );
    return () => {
      live = false;
    };
  }, [tries]);
  return { topics, retry: () => (setTopics(undefined), setTries((n) => n + 1)) };
}

/** A topic's actions, the picked one first. */
export function ordered(t: ChatTopic, pick?: string): ChatAction[] {
  const i = t.actions.findIndex((a) => a.id === pick);
  const first = t.actions[i];
  if (i <= 0 || !first) return t.actions;
  return [first, ...t.actions.slice(0, i), ...t.actions.slice(i + 1)];
}

export function MenuCard({
  id,
  menu,
  open,
  busy,
  onReply,
  openTool,
  onFold,
  head = true,
}: {
  /** The menu message; none for the menu in the tools panel
   * (menus-everywhere.md §4), which has no ✕. */
  id?: string;
  /** The topics' own heading: not in the tools panel, whose chip says it. */
  head?: boolean;
  menu: MenuMessage;
  /** The newest menu, nothing said since: open; any other is folded. */
  open: boolean;
  busy: boolean;
  /** A row sent back to Taskiem, as a button's press. */
  onReply: (reply: string, text: string) => void;
  openTool: (k: ToolKey, from?: DOMRect, start?: { add?: boolean }) => void;
  onFold?: () => void;
}) {
  const { topics, retry } = useCatalogue();
  const navigate = useNavigate();
  const [topic, setTopic] = useState(menu.topic);
  const [chosen, setChosen] = useState<string>();
  const [ended, setEnded] = useState(menu.done);
  const [shown, setShown] = useState(open && !menu.done);
  const [all, setAll] = useState(false);
  const [form, setForm] = useState<{ action: ChatAction; values: Record<string, string> }>();
  useEffect(() => {
    if (open) return;
    setShown(false);
    setEnded((e) => e ?? "passed");
  }, [open]);
  const choose = (a: ChatAction, tell = false) => {
    setChosen(a.title);
    setEnded("chose");
    setShown(false);
    onFold?.();
    // Nothing goes to Taskiem for a form or a page: the server is told, so
    // the menu stays folded to what was chosen.
    if (tell && id) void post(`/v1/taskiem-chat/menus/${encodeURIComponent(id)}/close`, { chose: a.id }).catch(() => undefined);
  };
  const close = () => {
    setEnded("closed");
    setShown(false);
    if (id) void post(`/v1/taskiem-chat/menus/${encodeURIComponent(id)}/close`, {}).catch(() => undefined);
  };
  const closeButton = id && (
    <button type="button" className="tk-menu-close" aria-label="Close this menu" onClick={close}>
      <Icon name="close" />
    </button>
  );

  if (topics === null)
    return (
      <div className="tk-menu tk-menu-folded" data-msg={id} data-testid="taskiem-menu">
        <span className="tk-menu-folded-words">The menu could not be loaded.</span>
        <button type="button" className="link" onClick={retry}>
          Try again
        </button>
      </div>
    );
  if (!topics) return <div className="tk-menu tk-menu-loading" data-msg={id} aria-busy="true" />;
  // The action chosen (here, or kept by the server), and its topic when the
  // card was the topics.
  const chosenAction = menu.chose ? topics.flatMap((x) => x.actions).find((a) => a.id === menu.chose) : undefined;
  const shownTopic = topic ?? (chosenAction ? topics.find((x) => x.actions.includes(chosenAction))?.id : undefined);
  const t = shownTopic ? topics.find((x) => x.id === shownTopic) : undefined;

  const press = (a: ChatAction, from?: DOMRect) => {
    setAll(false);
    // The row Taskiem picked with values it took from the message: the form
    // with them in it, even for an action with a tool of its own, whose
    // form would start empty.
    const picked = a.id === menu.pick && topic === menu.topic;
    if (picked && a.fields?.length && Object.keys(menu.values ?? {}).length > 0)
      return setForm({ action: a, values: { ...menu.values } });
    if (a.opens === "form" && a.tool) {
      choose(a, true);
      return openTool(a.tool as ToolKey, from, { add: a.add });
    }
    if (a.opens === "page" && a.page) {
      choose(a, true);
      return navigate(a.page);
    }
    // An action that takes values: its form here (M4), filled with what
    // Taskiem took from the message.
    if (a.fields?.length) {
      return setForm({ action: a, values: a.id === menu.pick && topic === menu.topic ? { ...menu.values } : {} });
    }
    choose(a);
    onReply(`act:${a.id}`, a.title);
  };
  const sent = (a: ChatAction, values: Record<string, string>) => {
    setForm(undefined);
    choose(a);
    const shown = (a.fields ?? []).map((f) => values[f.key]?.trim()).filter(Boolean);
    onReply(`form:${a.id}:${JSON.stringify(values)}`, shown.length ? `${a.title}: ${shown.join(", ")}` : a.title);
  };
  // Its form's submit and keys stay its own: drawn inside a composer (the
  // tools panel's menu), they would reach the composer's form too.
  // Drawn outside the page's other forms (a portal), so its button
  // submits its own form, not a composer's; its events stop at it too.
  const formSheet =
    form &&
    createPortal(
      <div style={{ display: "contents" }} onSubmit={(e) => e.stopPropagation()} onKeyDown={(e) => e.stopPropagation()}>
        <ActionForm action={form.action} initial={form.values} onClose={() => setForm(undefined)} onSend={(v) => sent(form.action, v)} />
      </div>,
      document.body,
    );

  if (!shown)
    return (
      <>
      {formSheet}
      <div className="tk-menu tk-menu-folded" data-msg={id} data-testid="taskiem-menu" data-folded="1">
        <span className="tk-tile-icon tk-menu-icon-sm">
          <Icon name={t ? menuIcon(t.icon) : "grid"} />
        </span>
        <span className="tk-menu-folded-words">
          <b>{t ? t.name : "Topics"}</b>
          <span>
            {" · "}
            {ended === "chose"
              ? `You chose ${chosen ?? chosenAction?.title ?? "an action"}`
              : ended === "closed"
                ? "Closed, nothing done"
                : "Not used"}
          </span>
        </span>
      </div>
      </>
    );

  if (!t)
    return (
      <div className="tk-menu" data-msg={id} data-testid="taskiem-menu" data-topic="">
        {head && (
        <div className="tk-menu-head">
          <span className="tk-tile-icon">
            <Icon name="grid" />
          </span>
          <span>
            <span className="tk-menu-title">What would you like to do?</span>
            {menu.line && <span className="tk-menu-line">{menu.line}</span>}
          </span>
          {closeButton}
        </div>
        )}
        <div className="tk-menu-tiles" role="group" aria-label="Topics">
          {topics.map((x) => (
            <button key={x.id} type="button" className="tk-menu-tile" onClick={() => setTopic(x.id)}>
              <span className="tk-tile-icon">
                <Icon name={menuIcon(x.icon)} />
              </span>
              <span>{x.name}</span>
            </button>
          ))}
        </div>
      </div>
    );

  const acts = ordered(t, topic === menu.topic ? menu.pick : undefined);
  const row = (a: ChatAction) => (
    <button
      key={a.id}
      type="button"
      className={`tk-menu-row${a.id === menu.pick && topic === menu.topic ? " picked" : ""}`}
      disabled={busy && a.opens !== "form" && a.opens !== "page"}
      onClick={(e) => press(a, e.currentTarget.getBoundingClientRect())}
    >
      <span className="tk-tile-icon">
        <Icon name={menuIcon(a.icon)} />
      </span>
      <span className="tk-menu-row-words">
        <span className="tk-menu-row-title">
          {a.title}
          {a.id === menu.pick && menu.values?.name && <span className="tk-menu-val">: {menu.values.name}</span>}
        </span>
        <span className="tk-menu-row-line">{a.line}</span>
      </span>
      <Icon name="chevron" className="tk-menu-chev" />
    </button>
  );
  return (
    <>
      <div className="tk-menu" data-msg={id} data-testid="taskiem-menu" data-topic={t.id}>
        <div className="tk-menu-head">
          <span className="tk-tile-icon">
            <Icon name={menuIcon(t.icon)} />
          </span>
          <span>
            <span className="tk-menu-title">{t.name}</span>
            <span className="tk-menu-line">{(topic === menu.topic && menu.line) || "What would you like to do?"}</span>
          </span>
          {closeButton}
        </div>
        <div className="tk-menu-rows">{acts.slice(0, MENU_ROWS).map(row)}</div>
        <div className="tk-menu-foot">
          {acts.length > MENU_ROWS && (
            <button type="button" onClick={() => setAll(true)}>
              All {acts.length} for {t.name}
            </button>
          )}
          <button type="button" onClick={() => setTopic(undefined)}>
            All topics
          </button>
        </div>
      </div>
      {menu.tip && topic === menu.topic && (
        <p className="tk-menu-tip">
          Next time you can type: <b>{menu.tip}</b>
        </p>
      )}
      {formSheet}
      {all && (
        <Sheet title={t.name} count={acts.length} onClose={() => setAll(false)} testid="taskiem-menu-all">
          <div className="tk-menu-rows">{acts.map(row)}</div>
        </Sheet>
      )}
    </>
  );
}

/** The kinds of value typed as a number. */
const NUMERIC = new Set(["money", "number", "signed", "phone"]);

/**
 * An action's form (chat-menus.md M4): a field for each of its values, with
 * the choices the business's own records offer as chips; Save sends it to
 * Taskiem as "form:<id>:<values>", read as its command typed, the Save
 * being the person's yes.
 */
export function ActionForm({
  action,
  initial,
  onClose,
  onSend,
}: {
  action: ChatAction;
  initial: Record<string, string>;
  onClose: () => void;
  onSend: (values: Record<string, string>) => void;
}) {
  const fields = action.fields ?? [];
  const [values, setValues] = useState<Record<string, string>>(initial);
  const [choices, setChoices] = useState<Record<string, string[]>>({});
  useEffect(() => {
    if (!fields.some((f) => f.kind !== "choice" && f.kind !== "text" && !NUMERIC.has(f.kind))) return;
    let live = true;
    get<{ fields: Record<string, string[]> }>(`/v1/chat/actions/${encodeURIComponent(action.id)}/choices`)
      .then((r) => live && setChoices(r.fields ?? {}))
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [action.id]);
  const set = (k: string, v: string) => setValues((cur) => ({ ...cur, [k]: v }));
  const ready = fields.every((f) => f.optional || (values[f.key] ?? "").trim() !== "");
  return (
    <Sheet title={action.title} onClose={onClose} testid="taskiem-action-form">
      <FormFrame
        save={async () => {
          onSend(Object.fromEntries(Object.entries(values).map(([k, v]) => [k, v.trim()])));
          return null;
        }}
        onSaved={() => undefined}
        ready={ready}
        saveWords={action.title}
      >
        {fields.map((f) => {
          const offered = f.choices ?? choices[f.key] ?? [];
          return (
            <KitField key={f.key} label={f.optional ? `${f.label} (optional)` : f.label} hint={f.hint}>
              {offered.length > 0 && (
                <span className="tk-chips tk-form-chips" role="group" aria-label={f.label}>
                  {offered.map((c) => (
                    <button key={c} type="button" aria-pressed={values[f.key] === c} onClick={() => set(f.key, c)}>
                      {c}
                    </button>
                  ))}
                </span>
              )}
              {f.kind !== "choice" && (
                <input
                  value={values[f.key] ?? ""}
                  onChange={(e) => set(f.key, e.target.value)}
                  inputMode={f.kind === "phone" ? "tel" : NUMERIC.has(f.kind) ? "decimal" : undefined}
                  autoComplete="off"
                />
              )}
            </KitField>
          );
        })}
      </FormFrame>
    </Sheet>
  );
}

/** What Taskiem said to a row pressed outside the Taskiem chat
 * (menus-everywhere.md §3), in a sheet: its words, a question's buttons
 * (answered here, the answer said in turn), and the way to the Taskiem
 * chat, where all of it is kept. */
export function ActionAnswer({
  title,
  said,
  onClose,
}: {
  title: string;
  said: { inbound: boolean; kind: string; text: string; buttons?: { id?: string; title: string }[] }[];
  onClose: () => void;
}) {
  const navigate = useNavigate();
  const [now, setNow] = useState(said);
  const [busy, setBusy] = useState(false);
  const words: string[] = [];
  const buttons: { id: string; title: string }[] = [];
  for (const m of now) {
    if (m.inbound) continue;
    if (m.kind === "menu") {
      const line = parseMenu(m.text)?.line;
      if (line) words.push(line);
    } else if (m.kind !== "chart" && m.text.trim()) words.push(m.text.replaceAll("*", "").trim());
    for (const b of m.buttons ?? []) if (b.id) buttons.push({ id: b.id, title: b.title });
  }
  const press = async (b: { id: string; title: string }) => {
    setBusy(true);
    try {
      const r = await post<{ messages: typeof said }>("/v1/taskiem-chat", { text: b.title, reply: b.id });
      setNow(r.messages);
    } catch {
      words.push("That did not go. Try it in your Taskiem chat.");
    } finally {
      setBusy(false);
    }
  };
  return (
    <Sheet
      title={title}
      onClose={onClose}
      testid="action-answer"
      footer={
        <>
          <button type="button" className="secondary" onClick={() => (onClose(), navigate("/chat?with=taskiem"))}>
            Taskiem chat
          </button>
          <button type="button" onClick={onClose}>
            Done
          </button>
        </>
      }
    >
      <div className="tk-action-answer">
        <p>{words.length ? words.join("\n\n") : "Done. It is in your Taskiem chat."}</p>
        {buttons.length > 0 && (
          <div className="tk-action-answer-buttons">
            {buttons.map((b, i) => (
              <button key={b.id} type="button" className={i === 0 ? "" : "secondary"} disabled={busy} onClick={() => void press(b)}>
                {b.title}
              </button>
            ))}
          </div>
        )}
      </div>
    </Sheet>
  );
}
