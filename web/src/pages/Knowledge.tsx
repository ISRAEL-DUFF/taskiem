import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { del, get, patch, post, upload } from "../api";
import { ErrorBox, EmptyState, Field, Item, Items, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";

// What your assistant knows (decision 0062 D12, docs/knowledge.md): the
// business's knowledge sources, what each gave, and the owner's approval
// before any is used.

export interface KnowledgeSource {
  id: string;
  kind: "about" | "storefront" | "table" | "website" | "file" | "sheet" | "books";
  title: string;
  config: Record<string, unknown>;
  status: "new" | "review" | "active" | "failed" | "paused";
  outside: boolean;
  approved_at: string | null;
  read_at: string | null;
  changed_at: string | null;
  error: string | null;
  passages: number;
  pending: number;
}

interface Passage {
  id: string;
  title: string;
  text: string;
  link: string;
  source?: string;
}

interface SourceView extends KnowledgeSource {
  shown: Passage[];
}

export const kindNames: Record<KnowledgeSource["kind"], string> = {
  about: "About my business",
  storefront: "My storefront",
  table: "A table",
  website: "My website",
  file: "A document",
  sheet: "A Google Sheet",
  books: "My books",
};

/** What a source's state means to the owner. */
export function statusWords(s: Pick<KnowledgeSource, "status" | "pending" | "passages" | "error">): { text: string; tone: "ok" | "warn" | "danger" | "muted" } {
  switch (s.status) {
    case "active":
      return { text: `In use: ${s.passages} ${s.passages === 1 ? "piece" : "pieces"}`, tone: "ok" };
    case "review":
      return { text: "Read: check it, then use it", tone: "warn" };
    case "failed":
      return s.passages > 0
        ? { text: "Could not be read again: the last reading is still used", tone: "warn" }
        : { text: "Could not be read", tone: "danger" };
    case "paused":
      return { text: "Paused: not used", tone: "muted" };
    default:
      return { text: "Not read yet", tone: "muted" };
  }
}

export function Knowledge() {
  const list = useLoad(() => get<{ sources: KnowledgeSource[] }>("/v1/knowledge"), []);
  const [adding, setAdding] = useState(false);
  return (
    <>
      <PageHeader
        title="What your assistant knows"
        description="What your assistant answers customers from: your own words, your storefront, your tables, your website, your documents. You see what each gives before it is used."
        actions={<button onClick={() => setAdding(true)}>Add a source</button>}
      />
      {adding && <AddSource onDone={() => (setAdding(false), list.reload())} onCancel={() => setAdding(false)} />}
      <ErrorBox error={list.error} />
      {!list.data ? (
        <Skeleton />
      ) : list.data.sources.length === 0 ? (
        <EmptyState icon="templates">Nothing yet. Start with About my business: your hours, where you deliver, how returns work.</EmptyState>
      ) : (
        <Items label="Sources">
          {list.data.sources.map((s) => {
            const st = statusWords(s);
            return (
              <Item
                key={s.id}
                to={`/knowledge/${s.id}`}
                icon="templates"
                tone={st.tone}
                title={s.title}
                sub={kindNames[s.kind]}
                sideSub={st.text}
                testid="knowledge-source"
              />
            );
          })}
        </Items>
      )}
      <TryIt />
    </>
  );
}

/** Asks what the assistant would find for a question. */
function TryIt() {
  const [q, setQ] = useState("");
  const act = useAction();
  const [found, setFound] = useState<{ found: boolean; passages: Passage[] }>();
  return (
    <section className="card" style={{ marginTop: 24 }}>
      <h2 style={{ marginTop: 0 }}>Try a question</h2>
      <form
        className="toolbar"
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => setFound(await post("/v1/knowledge/search", { question: q })), false);
        }}
      >
        <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Do you deliver to Ajah?" aria-label="A customer's question" />
        <button type="submit" disabled={act.busy || !q.trim()}>
          Find
        </button>
      </form>
      <ErrorBox error={act.error} />
      {found && !found.found && <p className="hint">Nothing answers it yet: add it to About my business.</p>}
      {found?.passages.map((p) => (
        <blockquote key={p.id} className="card" data-testid="knowledge-found">
          <strong>{p.title || p.source}</strong>
          <p style={{ whiteSpace: "pre-wrap" }}>{p.text}</p>
          <span className="hint">From {p.source}</span>
        </blockquote>
      ))}
    </section>
  );
}

function AddSource({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const navigate = useNavigate();
  const act = useAction();
  const [kind, setKind] = useState<KnowledgeSource["kind"]>("about");
  const [title, setTitle] = useState("");
  const [cfg, setCfg] = useState<Record<string, string>>({});
  const [file, setFile] = useState<File>();
  const sites = useLoad(() => get<{ sites: { id: string; origin: string; verified_at: string | null }[] }>("/v1/embed-sites").catch(() => ({ sites: [] })), []);
  const set = (k: string, v: string) => setCfg({ ...cfg, [k]: v });
  const submit = () =>
    act.run(async () => {
      let made: SourceView;
      if (kind === "file") {
        const form = new FormData();
        if (file) form.append("file", file);
        if (title) form.append("title", title);
        made = await upload<SourceView>("/v1/knowledge/files", form);
      } else {
        const config: Record<string, unknown> = { ...cfg };
        if (kind === "website" && cfg.max_pages) config.max_pages = Number(cfg.max_pages);
        made = await post<SourceView>("/v1/knowledge", { kind, ...(title ? { title } : {}), config });
      }
      onDone();
      navigate(`/knowledge/${made.id}`);
    }, false);
  return (
    <section className="card" data-testid="knowledge-add">
      <h2 style={{ marginTop: 0 }}>Add a source</h2>
      <Field label="What">
        <select value={kind} onChange={(e) => (setKind(e.target.value as KnowledgeSource["kind"]), setCfg({}))}>
          {(Object.keys(kindNames) as KnowledgeSource["kind"][]).map((k) => (
            <option key={k} value={k}>
              {kindNames[k]}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Name it (optional)">
        <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder={kindNames[kind]} />
      </Field>
      {kind === "about" && (
        <Field label="What customers ask about" hint="Your hours, where you deliver and for how much, how to pay, returns. Start a part with a line like “Delivery:”.">
          <textarea rows={10} value={cfg.text ?? ""} onChange={(e) => set("text", e.target.value)} />
        </Field>
      )}
      {kind === "storefront" && <p className="hint">Your storefront's name, address, hours, and what you sell with its prices.</p>}
      {kind === "table" && (
        <Field label="Table" hint="Its personal columns (names, numbers, emails) are never read.">
          <input value={cfg.table ?? ""} onChange={(e) => set("table", e.target.value)} placeholder="products" />
        </Field>
      )}
      {kind === "website" && (
        <>
          <Field label="Website" hint="Only a website you have proven is yours (Settings, Websites).">
            <select value={cfg.site ?? ""} onChange={(e) => set("site", e.target.value)}>
              <option value="">Choose…</option>
              {(sites.data?.sites ?? [])
                .filter((s) => s.verified_at)
                .map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.origin}
                  </option>
                ))}
            </select>
          </Field>
          <Field label="Pages to read" hint="At most 50.">
            <input type="number" min={1} max={50} value={cfg.max_pages ?? "20"} onChange={(e) => set("max_pages", e.target.value)} />
          </Field>
        </>
      )}
      {kind === "file" && (
        <Field label="Document" hint="A PDF or Word document with text in it (a scanned page cannot be read yet). Only its text is kept.">
          <input type="file" accept=".pdf,.docx,application/pdf,application/vnd.openxmlformats-officedocument.wordprocessingml.document" onChange={(e) => setFile(e.target.files?.[0])} />
        </Field>
      )}
      {kind === "sheet" && (
        <>
          <Field label="Spreadsheet" hint="The long code in the sheet's address, after /d/.">
            <input value={cfg.spreadsheet_id ?? ""} onChange={(e) => set("spreadsheet_id", e.target.value)} />
          </Field>
          <Field label="Which part" hint="For example Prices!A1:D200. The first row names the columns.">
            <input value={cfg.range ?? ""} onChange={(e) => set("range", e.target.value)} />
          </Field>
        </>
      )}
      {kind === "books" && (
        <Field label="Your books" hint="The items and prices in QuickBooks or Zoho Books, through its connection.">
          <select value={cfg.connector ?? ""} onChange={(e) => set("connector", e.target.value)}>
            <option value="">Choose…</option>
            <option value="quickbooks">QuickBooks</option>
            <option value="zohobooks">Zoho Books</option>
          </select>
        </Field>
      )}
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button className="primary" disabled={act.busy} onClick={() => void submit()}>
          {act.busy ? "Reading…" : "Add and read it"}
        </button>
        <button onClick={onCancel}>Cancel</button>
      </div>
    </section>
  );
}

export function KnowledgeSourcePage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const src = useLoad(() => get<SourceView>(`/v1/knowledge/${id}`), [id]);
  const act = useAction();
  if (src.error) return <ErrorBox error={src.error} />;
  if (!src.data) return <Skeleton />;
  const s = src.data;
  const st = statusWords(s);
  const run = (fn: () => Promise<unknown>, done: string | false) => void act.run(async () => (await fn(), src.reload()), done);
  return (
    <>
      <PageHeader
        title={s.title}
        description={
          <>
            {kindNames[s.kind]} · {st.text}
            {s.read_at ? ` · read ${fmtTime(s.read_at)}` : ""}
            {s.changed_at ? ` · last changed ${fmtTime(s.changed_at)}` : ""}
          </>
        }
        actions={<Link to="/knowledge">All sources</Link>}
      />
      {s.error && (
        <div className="notice" role="note">
          <p>{s.error}</p>
        </div>
      )}
      {s.status === "review" && (
        <div className="notice" role="note" data-testid="knowledge-review">
          <p>
            This is what your assistant would know from it.{s.outside ? " It comes from outside Taskiem: check it says only what you want customers told." : ""} Use it when it is
            right.
          </p>
          <button className="primary" disabled={act.busy} onClick={() => run(() => post(`/v1/knowledge/${s.id}/approve`), "In use")}>
            Use this
          </button>
        </div>
      )}
      <ErrorBox error={act.error} />
      <div className="toolbar">
        {s.kind !== "file" && s.kind !== "about" && (
          <button disabled={act.busy} onClick={() => run(() => post(`/v1/knowledge/${s.id}/refresh`), "Read again")}>
            Read it again
          </button>
        )}
        <button disabled={act.busy} onClick={() => run(() => post(`/v1/knowledge/${s.id}/pause`, { paused: s.status !== "paused" }), "Saved")}>
          {s.status === "paused" ? "Use it again" : "Pause"}
        </button>
        <button
          className="danger"
          disabled={act.busy}
          onClick={() => void act.run(async () => (await del(`/v1/knowledge/${s.id}`), navigate("/knowledge")), "Deleted")}
        >
          Delete
        </button>
      </div>
      {s.kind === "about" && <AboutEditor source={s} onSaved={src.reload} />}
      <section className="card" style={{ marginTop: 24 }}>
        <h2 style={{ marginTop: 0 }}>{s.status === "review" ? "What it gave" : "What your assistant knows from it"}</h2>
        {s.shown.length === 0 && <p className="hint">Nothing yet.</p>}
        {s.shown.map((p) => (
          <article key={p.id} data-testid="knowledge-passage" style={{ borderTop: "1px solid var(--line)", padding: "8px 0" }}>
            {p.title && <strong>{p.title}</strong>}
            <p style={{ whiteSpace: "pre-wrap", margin: "4px 0" }}>{p.text}</p>
            {p.link && (
              <a href={p.link} target="_blank" rel="noreferrer" className="hint">
                {p.link}
              </a>
            )}
          </article>
        ))}
      </section>
    </>
  );
}

function AboutEditor({ source, onSaved }: { source: SourceView; onSaved: () => void }) {
  const [text, setText] = useState(String(source.config.text ?? ""));
  const act = useAction();
  return (
    <section className="card" style={{ marginTop: 24 }}>
      <h2 style={{ marginTop: 0 }}>Your words</h2>
      <textarea rows={12} value={text} onChange={(e) => setText(e.target.value)} aria-label="About my business" style={{ width: "100%" }} />
      <ErrorBox error={act.error} />
      <button className="primary" disabled={act.busy || !text.trim()} onClick={() => void act.run(async () => (await patch(`/v1/knowledge/${source.id}`, { config: { text } }), onSaved()), "Saved")}>
        Save
      </button>
    </section>
  );
}
