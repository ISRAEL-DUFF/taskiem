// Search everywhere (docs/spec/menus-everywhere.md §6, E4): one box for
// what Taskiem can do and what the business has. As the words are typed,
// the results in groups, three of each: Actions (the person's catalogue:
// "add rider" finds "Add a rider"), Chats (the businesses linked), Products,
// Services, Invoices and bills, Orders, Customers, Saved; a group's "Show
// more", or its chip, shows all of it. Invoices, orders, customers,
// services and saved things are searched on the server, every record (the
// picker's search, E1); a saved thing comes by its name and kind only,
// the person's own and their business's, never what it holds. The rest are
// lists read once. An action is done as from Today (its form first, its
// answer in a sheet); anything else opens its page.

import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { get } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { naira } from "../lib/invoices";
import { suggest, type Suggestion } from "../lib/mentions";
import { refSuggestion, type ChatRef } from "../lib/refs";
import { foundSuggestion, type Found } from "../lib/found";
import { savedIcon } from "../lib/saved";
import { Item, Items, PageHeader } from "../ui";
import { ActionForm, menuIcon, useActionRun, useCatalogue, type ChatAction, type ChatTopic } from "./ChatMenu";

const KINDS = {
  all: "All",
  action: "Actions",
  chat: "Chats",
  product: "Products",
  service: "Services",
  invoice: "Invoices",
  order: "Orders",
  customer: "Customers",
  saved: "Saved",
} as const;
type Kind = keyof typeof KINDS;

/** The server's kinds for a chip. */
const SERVER: Partial<Record<Kind, string>> = {
  invoice: "invoice,bill",
  order: "order,purchase",
  customer: "customer",
  service: "service",
  saved: "saved",
};
/** All asks each of these on its own, so one kind's many never crowd out
 * another's few (services and saved things have no date to come first by). */
const ALL = ["invoice,bill,order,purchase,customer", "service", "saved"];

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
    case "service":
      return "/storefront/bookings";
    case "saved":
      return "/settings/saved";
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
  const [found, setFound] = useState<{ key: string; refs: Found[]; cursor: string }>();
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

  // Invoices, orders, customers, services and saved things: asked of the
  // server a moment after the last key.
  const kinds = kind === "all" ? ALL.join(",") : SERVER[kind];
  const key = `${kinds}|${words.toLowerCase()}`;
  useEffect(() => {
    if (!words || !kinds || found?.key === key) return;
    let live = true;
    const t = window.setTimeout(() => {
      const ask = (k: string, limit: string) =>
        get<{ refs: Found[]; cursor?: string }>(`/v1/chat-refs?${new URLSearchParams({ q: words, kind: k, limit })}`);
      const asked = kind === "all" ? ALL.map((k, i) => ask(k, i === 0 ? "30" : "10")) : [ask(kinds, "20")];
      Promise.all(asked).then(
        (rs) => {
          if (!live) return;
          setFound({ key, refs: rs.flatMap((r) => r.refs), cursor: kind === "all" ? "" : (rs[0]?.cursor ?? "") });
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
    get<{ refs: Found[]; cursor?: string }>(`/v1/chat-refs?${qs}`).then(
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
  const refs = (ks: string[]) =>
    fresh ? fresh.refs.filter((r) => ks.includes(r.kind)).map((r) => refSuggestion(r as ChatRef)) : [];
  const others = (k: "service" | "saved") =>
    fresh ? fresh.refs.filter((r) => r.kind === k).flatMap((r) => foundSuggestion(r) ?? []) : [];
  const services = others("service");
  const saved = others("saved");

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
        on("service") && group("service", "Services", services.length, services.map((s) => item(s, "clock"))),
        on("invoice") && group("invoice", "Invoices and bills", refs(["invoice", "bill"]).length, refs(["invoice", "bill"]).map((s) => item(s, "invoices"))),
        on("order") && group("order", "Orders", refs(["order", "purchase"]).length, refs(["order", "purchase"]).map((s) => item(s, "orders"))),
        on("customer") && group("customer", "Customers", refs(["customer"]).length, refs(["customer"]).map((s) => item(s, "members"))),
        on("saved") && group("saved", "Saved", saved.length, saved.map((s) => item(s, savedIcon(s.savedKind)))),
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
          Search what Taskiem can do (“add rider”, “sell”), your products, services, invoices, orders, customers, chats and saved things.
        </p>
      )}
      {offline && <p className="hint">Invoices, orders, customers, services and saved things could not be searched. Check your connection.</p>}
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
