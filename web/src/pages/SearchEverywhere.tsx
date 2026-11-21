// Search everywhere (docs/spec/menus-everywhere.md §6, E4): one box for
// what Taskiem can do and what the business has. As the words are typed,
// the results in groups, three of each: Actions (the person's catalogue:
// "add rider" finds "Add a rider"), Chats (the businesses linked), Products,
// Invoices and bills, Orders, Customers; a group's "Show more", or its chip,
// shows all of it. Invoices, orders and customers are searched on the
// server, every record (the picker's search, E1); the rest are lists read
// once. An action is done as from Today (its form first, its answer in a
// sheet); anything else opens its page.

import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { get } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { naira } from "../lib/invoices";
import { suggest, type Suggestion } from "../lib/mentions";
import { refSuggestion, type ChatRef } from "../lib/refs";
import { Item, Items, PageHeader } from "../ui";
import { ActionForm, menuIcon, useActionRun, useCatalogue, type ChatAction, type ChatTopic } from "./ChatMenu";

const KINDS = {
  all: "All",
  action: "Actions",
  chat: "Chats",
  product: "Products",
  invoice: "Invoices",
  order: "Orders",
  customer: "Customers",
} as const;
type Kind = keyof typeof KINDS;

/** The server's kinds for a chip (and for All, every one). */
const SERVER: Partial<Record<Kind, string>> = { invoice: "invoice,bill", order: "order,purchase", customer: "customer" };

interface StockItem {
  id: string;
  name: string;
  price_kobo?: number;
  unit?: string;
  category?: string;
  variant_of?: string;
}

/** Where a thing found opens. */
function pathOf(s: Suggestion): string {
  switch (s.kind) {
    case "business":
      return `/chat?with=${encodeURIComponent(s.id)}`;
    case "customer":
      return `/chat?with=c-${encodeURIComponent(s.id)}`;
    case "invoice":
      return `/invoices/${encodeURIComponent(s.id)}`;
    case "bill":
      return `/bills/${encodeURIComponent(s.id)}`;
    case "order":
      return `/storefront/orders/${encodeURIComponent(s.name)}`;
    case "purchase":
      return `/purchases/${encodeURIComponent(s.id)}`;
    default:
      return "/storefront/stock";
  }
}

export function SearchEverywhere() {
  const { can } = useAuth();
  const [q, setQ] = useState("");
  const [kind, setKind] = useState<Kind>("all");
  const [shown, setShown] = useState<Record<string, number>>({});
  const { topics } = useCatalogue();
  const [stock, setStock] = useState<StockItem[]>([]);
  const [chats, setChats] = useState<{ link: string; name: string; state: string }[]>([]);
  const [found, setFound] = useState<{ key: string; refs: ChatRef[]; cursor: string }>();
  const [offline, setOffline] = useState(false);
  const [form, setForm] = useState<ChatAction>();
  const action = useActionRun();
  const words = q.trim();

  useEffect(() => {
    if (can("table.read"))
      get<{ items: StockItem[] }>("/v1/stock").then((r) => setStock(r.items ?? []), () => undefined);
    get<{ conversations: { link: string; name: string; state: string }[] }>("/v1/link-chats").then(
      (r) => setChats(r.conversations ?? []),
      () => undefined,
    );
  }, []);

  // Invoices, orders and customers: asked of the server a moment after the
  // last key.
  const kinds = kind === "all" ? "invoice,bill,order,purchase,customer" : SERVER[kind];
  const key = `${kinds}|${words.toLowerCase()}`;
  useEffect(() => {
    if (!words || !kinds || found?.key === key) return;
    let live = true;
    const t = window.setTimeout(() => {
      const qs = new URLSearchParams({ q: words, kind: kinds, limit: kind === "all" ? "30" : "20" });
      get<{ refs: ChatRef[]; cursor?: string }>(`/v1/chat-refs?${qs}`).then(
        (r) => {
          if (!live) return;
          setFound({ key, refs: r.refs, cursor: r.cursor ?? "" });
          setOffline(false);
        },
        () => live && setOffline(true),
      );
    }, 250);
    return () => {
      live = false;
      window.clearTimeout(t);
    };
  }, [key]);

  const more = (group: Kind) => {
    if (kind === "all") {
      setKind(group);
      setShown({});
      return;
    }
    setShown((s) => ({ ...s, [group]: (s[group] ?? 8) + 8 }));
    const f = found;
    const k = SERVER[group];
    if (!f || !k || !f.cursor) return;
    const qs = new URLSearchParams({ q: words, kind: k, cursor: f.cursor, limit: "20" });
    get<{ refs: ChatRef[]; cursor?: string }>(`/v1/chat-refs?${qs}`).then(
      (r) => setFound((cur) => (cur && cur.key === f.key ? { ...cur, refs: [...cur.refs, ...r.refs], cursor: r.cursor ?? "" } : cur)),
      () => setOffline(true),
    );
  };

  // What each group finds.
  const actions: { s: Suggestion; a: ChatAction; t: ChatTopic }[] = [];
  if (words && topics) {
    const all = topics.flatMap((t) =>
      t.actions.map((a) => ({ s: { kind: "item", id: a.id, name: a.title, sub: a.line, category: t.name, words: a.shortcut } as Suggestion, a, t })),
    );
    for (const s of suggest(all.map((x) => x.s), words, null)) {
      const x = all.find((y) => y.s === s);
      if (x) actions.push(x);
    }
  }
  const chatRows = words
    ? suggest(
        chats.filter((c) => c.state === "active").map((c) => ({ kind: "business", id: c.link, name: c.name, sub: "Linked business" }) as Suggestion),
        words,
        null,
      )
    : [];
  const products = words
    ? suggest(
        stock
          .filter((x) => !x.variant_of)
          .map(
            (x) =>
              ({
                kind: "item",
                id: x.id,
                name: x.name,
                sub: x.price_kobo !== undefined ? `Yours · ${naira(x.price_kobo)}${x.unit ? ` a ${x.unit}` : ""}` : "Yours · no price yet",
                category: x.category,
              }) as Suggestion,
          ),
        words,
        null,
      )
    : [];
  const fresh = found && found.key.endsWith(`|${words.toLowerCase()}`) ? found : undefined;
  const refs = (ks: string[]) => (fresh ? fresh.refs.filter((r) => ks.includes(r.kind)).map((r) => refSuggestion(r)) : []);

  const on = (k: Kind) => kind === "all" || kind === k;
  const n = (k: Kind) => shown[k] ?? (kind === "all" ? 3 : 8);
  const group = (k: Kind, title: string, count: number, rows: React.ReactNode[]) =>
    count > 0 && (
      <section key={k} className="search-group" aria-labelledby={`search-${k}`} data-testid={`search-${k}`}>
        <h2 id={`search-${k}`}>{title}</h2>
        <Items label={title}>{rows.slice(0, n(k))}</Items>
        {(count > n(k) || (kind !== "all" && !!SERVER[k] && !!fresh?.cursor)) && (
          <div className="search-more">
            <button type="button" className="secondary" onClick={() => more(k)} aria-label={`Show more ${title}`}>
              Show more
            </button>
          </div>
        )}
      </section>
    );
  const item = (s: Suggestion, icon: string) => (
    <Item key={`${s.kind}:${s.id}`} to={pathOf(s)} icon={icon} title={s.name} sub={s.sub} />
  );
  const groups = words
    ? [
        on("action") &&
          group(
            "action",
            "Actions",
            actions.length,
            actions.map(({ s, a, t }) => (
              <li key={a.id}>
                <button
                  type="button"
                  className="item"
                  onClick={() => (a.fields?.length ? setForm(a) : action.run(`act:${a.id}`, a.title))}
                >
                  <span className="list-icon">
                    <Icon name={menuIcon(a.icon)} />
                  </span>
                  <span className="grow">
                    <span className="item-top">
                      <span className="title">{s.name}</span>
                    </span>
                    <span className="sub">
                      {t.name} · {s.sub}
                    </span>
                  </span>
                  <Icon name="chevron" className="go" />
                </button>
              </li>
            )),
          ),
        on("chat") && group("chat", "Chats", chatRows.length, chatRows.map((s) => item(s, "chat"))),
        on("product") && group("product", "Products", products.length, products.map((s) => item(s, "box"))),
        on("invoice") && group("invoice", "Invoices and bills", refs(["invoice", "bill"]).length, refs(["invoice", "bill"]).map((s) => item(s, "invoices"))),
        on("order") && group("order", "Orders", refs(["order", "purchase"]).length, refs(["order", "purchase"]).map((s) => item(s, "orders"))),
        on("customer") && group("customer", "Customers", refs(["customer"]).length, refs(["customer"]).map((s) => item(s, "members"))),
      ].filter(Boolean)
    : [];

  return (
    <div className="search-everywhere" data-testid="search-everywhere">
      <PageHeader title="Search" />
      <div className="tk-search search-everywhere-box">
        <Icon name="search" />
        <input
          type="search"
          aria-label="Search everything"
          placeholder="Actions, products, invoices, chats…"
          value={q}
          autoFocus
          onChange={(e) => {
            setQ(e.target.value);
            setShown({});
          }}
        />
      </div>
      <div className="tk-chips" role="group" aria-label="Kinds">
        {(Object.keys(KINDS) as Kind[]).map((k) => (
          <button
            key={k}
            type="button"
            aria-pressed={k === kind}
            onClick={() => {
              setKind(k);
              setShown({});
            }}
          >
            {KINDS[k]}
          </button>
        ))}
      </div>
      {!words && (
        <p className="hint search-hint">
          Search what Taskiem can do (“add rider”, “sell”), your products, invoices, orders, customers and chats.
        </p>
      )}
      {offline && <p className="hint">Invoices, orders and customers could not be searched. Check your connection.</p>}
      {groups}
      {words && groups.length === 0 && fresh && <p className="hint">Nothing found for “{words}”.</p>}
      {action.error && (
        <p className="error" role="alert">
          {action.error}
        </p>
      )}
      {form &&
        createPortal(
          <ActionForm
            action={form}
            initial={{}}
            onClose={() => setForm(undefined)}
            onSend={(values) => {
              const a = form;
              setForm(undefined);
              const words = (a.fields ?? []).map((f) => values[f.key]?.trim()).filter(Boolean);
              action.run(`form:${a.id}:${JSON.stringify(values)}`, words.length ? `${a.title}: ${words.join(", ")}` : a.title);
            }}
          />,
          document.body,
        )}
      {action.sheet}
    </div>
  );
}
