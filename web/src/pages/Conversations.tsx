import { useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { del, get, post } from "../api";
import { useAuth } from "../auth";
import {
  EmptyState,
  ErrorBox,
  Field,
  Item,
  Items,
  PageHeader,
  Skeleton,
  fmtTime,
  useAction,
  useLoad,
} from "../ui";
import {
  fillTemplate,
  kindWords,
  mergeTurns,
  seconds,
  statusWords,
  templateSlots,
} from "../lib/conversations";

// Conversations (decision 0062 V7, docs/conversations.md): what customers
// said to the business's assistant and what was answered, answering by
// hand, and Talk to it here.

interface AssistantView {
  workflow: string;
  name: string;
  environment: string;
  connector: string;
  where: string;
  channel: string;
}

interface CustomerView {
  channel: string;
  customer: string;
  hint: string;
  number?: string;
  state: "assistant" | "waiting" | "stopped";
  first_at: string;
  last_at: string;
  last_role?: string;
  waiting_until?: string;
  window_open: boolean;
}

interface TurnView {
  id: string;
  at: string;
  role: "customer" | "assistant" | "member" | "held";
  kind: string;
  text: string;
  transcript?: string;
  status?: string;
  reason?: string;
  run_id?: string;
  by?: string;
  erased?: boolean;
}

interface Metrics {
  month: {
    customers: number;
    messages: number;
    answers: number;
    held: number;
    by_hand: number;
    handovers: number;
    failed: number;
    reply_seconds_median: number | null;
    reply_seconds_p90: number | null;
  };
  allowances: { ai_answers: number | null; voice: number | null };
}

/** What a customer's state means. */
export function stateWords(c: Pick<CustomerView, "state" | "waiting_until">): {
  text: string;
  tone: "ok" | "warn" | "muted";
} {
  switch (c.state) {
    case "waiting":
      return {
        text: c.waiting_until
          ? `A person is answering (until ${fmtTime(c.waiting_until)})`
          : "A person is answering",
        tone: "warn",
      };
    case "stopped":
      return { text: "Wrote STOP: not answered", tone: "muted" };
    default:
      return { text: "Your assistant answers", tone: "ok" };
  }
}

export function Conversations() {
  const { can } = useAuth();
  const [params, setParams] = useSearchParams();
  const list = useLoad(
    () =>
      get<{ assistants: AssistantView[]; customers: CustomerView[] }>(
        `/v1/conversations${params.get("channel") ? `?channel=${encodeURIComponent(params.get("channel") ?? "")}` : ""}`,
      ),
    [params.get("channel")],
  );
  const assistants = list.data?.assistants ?? [];
  const channel = params.get("channel") ?? assistants[0]?.channel ?? "";
  // The list is per assistant: with none chosen, the first one's is asked
  // for (one busy assistant cannot push another's customers off).
  const first = assistants[0]?.channel;
  useEffect(() => {
    if (!params.get("channel") && first)
      setParams({ channel: first }, { replace: true });
  }, [first, params, setParams]);
  const current =
    assistants.find((a) => a.channel === channel) ?? assistants[0];
  return (
    <>
      <PageHeader
        title="Conversations"
        description="What customers said to your assistant and what it answered. Answer them yourself when they ask for a person."
      />
      <ErrorBox error={list.error} />
      {!list.data ? (
        list.error ? null : (
          <Skeleton />
        )
      ) : assistants.length === 0 ? (
        <EmptyState icon="chat">
          No assistant yet. Publish a workflow that answers your customers on
          WhatsApp or Telegram, and its conversations show here.
        </EmptyState>
      ) : (
        <>
          {assistants.length > 1 && (
            <Field label="Assistant">
              <select
                value={channel}
                onChange={(e) => setParams({ channel: e.target.value })}
              >
                {assistants.map((a) => (
                  <option key={a.channel} value={a.channel}>
                    {a.name} · {a.where} (
                    {a.environment === "prod" ? "live" : a.environment})
                  </option>
                ))}
              </select>
            </Field>
          )}
          {current && <MetricsCard channel={current.channel} />}
          <section style={{ marginTop: 16 }}>
            <h2>Customers</h2>
            {list.data.customers.filter((c) => c.channel === channel).length ===
            0 ? (
              <p className="hint">No customer has written yet.</p>
            ) : (
              <Items label="Customers">
                {list.data.customers
                  .filter((c) => c.channel === channel)
                  .map((c) => {
                    const st = stateWords(c);
                    return (
                      <Item
                        key={c.customer}
                        to={`/conversations/view?channel=${encodeURIComponent(c.channel)}&customer=${encodeURIComponent(c.customer)}`}
                        icon="chat"
                        tone={st.tone}
                        title={c.hint}
                        sub={`Last message ${fmtTime(c.last_at)}`}
                        sideSub={st.text}
                        testid="conversation-customer"
                      />
                    );
                  })}
              </Items>
            )}
          </section>
          {current && can("run.start") && (
            <TalkToIt key={current.channel} assistant={current} />
          )}
        </>
      )}
    </>
  );
}

function MetricsCard({ channel }: { channel: string }) {
  const m = useLoad(
    () =>
      get<Metrics>(
        `/v1/conversations/metrics?channel=${encodeURIComponent(channel)}`,
      ),
    [channel],
  );
  if (!m.data) return m.error ? <ErrorBox error={m.error} /> : null;
  const x = m.data.month;
  const tiles: [string, string][] = [
    ["Customers", String(x.customers)],
    ["Messages", String(x.messages)],
    ["Answers", String(x.answers)],
    ["Time to answer", seconds(x.reply_seconds_median)],
    ["Held back", String(x.held)],
    ["Asked for a person", String(x.handovers)],
    ["Answered by hand", String(x.by_hand)],
    ["Did not finish", String(x.failed)],
  ];
  return (
    <section className="card" data-testid="conversation-metrics">
      <h2 style={{ marginTop: 0 }}>This month</h2>
      <div
        className="stats"
        style={{
          gridTemplateColumns: "repeat(auto-fit, minmax(140px, 1fr))",
          margin: "12px 0",
        }}
      >
        {tiles.map(([label, value]) => (
          <div key={label} className="stat">
            <span className="stat-label">{label}</span>
            <span className="stat-value">{value}</span>
          </div>
        ))}
      </div>
      <p className="hint">
        Used of this month's allowance (all your assistants): AI answers{" "}
        {m.data.allowances.ai_answers == null
          ? "no limit"
          : `${m.data.allowances.ai_answers}%`}
        , voice notes{" "}
        {m.data.allowances.voice == null
          ? "no limit"
          : `${m.data.allowances.voice}%`}
        .
      </p>
    </section>
  );
}

/** One customer's conversation, with answering by hand. */
export function ConversationPage() {
  const { can } = useAuth();
  const [params] = useSearchParams();
  const channel = params.get("channel") ?? "";
  const customer = params.get("customer") ?? "";
  const [reveal, setReveal] = useState(false);
  const [turns, setTurns] = useState<TurnView[]>([]);
  const [noEarlier, setNoEarlier] = useState(false);
  const q = `channel=${encodeURIComponent(channel)}&customer=${encodeURIComponent(customer)}`;
  const thread = useLoad(
    () =>
      get<{ customer: CustomerView; turns: TurnView[] }>(
        `/v1/conversations/thread?${q}${reveal ? "&reveal=true" : ""}`,
      ),
    [q, reveal],
  );
  useEffect(() => {
    setTurns([]);
    setNoEarlier(false);
  }, [q, reveal]);
  // Each answer is merged with what is shown (earlier messages loaded
  // stay; a message's new status replaces the old).
  const latest = thread.data?.turns;
  useEffect(() => {
    if (latest) setTurns((t) => mergeTurns(t, latest));
  }, [latest]);
  // New messages show as they come: asked again every 15 seconds while
  // the page is in view. Not while numbers are shown: each showing is
  // audited.
  const reload = thread.reload;
  useEffect(() => {
    if (reveal) return;
    const t = setInterval(() => {
      if (document.visibilityState === "visible") reload();
    }, 15000);
    return () => clearInterval(t);
  }, [reload, reveal]);
  const stream = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);
  const newest = turns.at(-1)?.id;
  useEffect(() => {
    const el = stream.current;
    if (el && atBottom.current) el.scrollTop = el.scrollHeight;
  }, [newest]);
  const act = useAction();
  const more = useAction();
  const loadEarlier = () =>
    void more.run(async () => {
      const first = turns[0];
      if (!first) return;
      const r = await get<{ turns: TurnView[] }>(
        `/v1/conversations/thread?${q}${reveal ? "&reveal=true" : ""}&before=${encodeURIComponent(first.at)}&before_id=${encodeURIComponent(first.id)}`,
      );
      if (r.turns.length === 0) setNoEarlier(true);
      setTurns((t) => mergeTurns(t, r.turns));
    }, false);
  if (!thread.data)
    return thread.error ? <ErrorBox error={thread.error} /> : <Skeleton />;
  const c = thread.data.customer;
  const st = stateWords(c);
  const run = (fn: () => Promise<unknown>, done: string) =>
    void act.run(async () => (await fn(), thread.reload()), done);
  return (
    <>
      <PageHeader
        title={c.number ?? c.hint}
        description={
          <>
            {st.text} · first wrote {fmtTime(c.first_at)}
          </>
        }
        actions={
          <Link to={`/conversations?channel=${encodeURIComponent(channel)}`}>
            All customers
          </Link>
        }
      />
      <div className="toolbar">
        {can("pii.reveal") && (
          <button
            onClick={() => {
              setReveal(!reveal);
              setTurns([]);
            }}
            data-testid="conversation-reveal"
          >
            {reveal ? "Hide numbers and emails" : "Show numbers and emails"}
          </button>
        )}
        {can("conversation.reply") && c.state === "assistant" && (
          <button
            disabled={act.busy}
            onClick={() =>
              run(
                () =>
                  post("/v1/conversations/pause", {
                    channel,
                    customer,
                    hours: 24,
                  }),
                "Your assistant is quiet for them",
              )
            }
          >
            Answer them myself
          </button>
        )}
        {can("conversation.reply") && c.state === "waiting" && (
          <button
            disabled={act.busy}
            onClick={() =>
              run(
                () => post("/v1/conversations/resume", { channel, customer }),
                "Your assistant answers them again",
              )
            }
          >
            Let the assistant answer them again
          </button>
        )}
      </div>
      <ErrorBox error={act.error} />
      {thread.error && <ErrorBox error={thread.error} />}
      <div className="chat-pane" style={{ marginTop: 12 }}>
        <div
          className="chat-stream"
          data-testid="conversation-thread"
          role="log"
          aria-live="polite"
          aria-relevant="additions"
          aria-busy={more.busy}
          aria-label="The conversation"
          tabIndex={0}
          ref={stream}
          onScroll={(e) => {
            const el = e.currentTarget;
            atBottom.current =
              el.scrollHeight - el.scrollTop - el.clientHeight < 80;
          }}
        >
          {thread.data.turns.length >= 200 && !noEarlier && (
            <button disabled={more.busy} onClick={loadEarlier}>
              {more.busy ? "Loading…" : "Show earlier messages"}
            </button>
          )}
          <ErrorBox error={more.error} />
          {turns.length === 0 && <p className="hint">Nothing yet.</p>}
          {turns.map((t) => (
            <TurnBubble key={t.id} t={t} runs={can("run.read")} />
          ))}
        </div>
      </div>
      {can("conversation.reply") && c.state !== "stopped" && (
        <ReplyBox
          channel={channel}
          customer={customer}
          windowOpen={c.window_open}
          onSent={thread.reload}
        />
      )}
      {c.state === "stopped" && (
        <p className="hint">
          They wrote STOP: nothing is sent to them until they write START.
        </p>
      )}
    </>
  );
}

function TurnBubble({ t, runs }: { t: TurnView; runs: boolean }) {
  const mine = t.role !== "customer";
  const who =
    t.role === "customer"
      ? "Customer"
      : t.role === "member"
        ? "Answered by your team"
        : t.role === "held"
          ? "Not sent"
          : "Your assistant";
  const note = statusWords(t);
  return (
    <div
      className={mine ? "bubble mine" : "bubble"}
      data-testid={`turn-${t.role}`}
      style={
        t.role === "held"
          ? { borderStyle: "dashed", borderColor: "var(--warn)" }
          : undefined
      }
    >
      <span className="who">{who}</span>
      {t.erased ? (
        <span className="text hint">Erased</span>
      ) : (
        <>
          {t.kind !== "text" && t.kind !== "template" && (
            <span className="hint">{kindWords(t.kind)} </span>
          )}
          {t.kind === "template" && <span className="hint">Template: </span>}
          {t.text && <span className="text">{t.text}</span>}
          {t.transcript && <span className="text">“{t.transcript}”</span>}
        </>
      )}
      <span className="hint" style={{ display: "block", fontSize: 12 }}>
        {fmtTime(t.at)}
        {note ? ` · ${note}` : ""}
        {t.run_id && runs && (
          <>
            {" · "}
            <Link to={`/runs/${t.run_id}`}>See how it was answered</Link>
          </>
        )}
      </span>
    </div>
  );
}

interface Template {
  name: string;
  language: string;
  category?: string;
  body: string;
}

function ReplyBox({
  channel,
  customer,
  windowOpen,
  onSent,
}: {
  channel: string;
  customer: string;
  windowOpen: boolean;
  onSent: () => void;
}) {
  const [text, setText] = useState("");
  const [requestId, setRequestId] = useState(() => crypto.randomUUID());
  const act = useAction();
  const templates = useLoad(
    () =>
      windowOpen
        ? Promise.resolve({ templates: [] as Template[] })
        : get<{ templates: Template[] }>(
            `/v1/conversations/templates?channel=${encodeURIComponent(channel)}`,
          ),
    [channel, windowOpen],
  );
  const [chosen, setChosen] = useState("");
  const [values, setValues] = useState<string[]>([]);
  const tpl = templates.data?.templates.find(
    (t) => `${t.name}|${t.language}` === chosen,
  );
  const named = tpl ? templateSlots(tpl.body) < 0 : false;
  const slots = tpl ? Math.max(0, templateSlots(tpl.body)) : 0;
  const filled = Array.from({ length: slots }, (_, i) => values[i] ?? "");
  const ready = windowOpen
    ? !!text.trim()
    : !!tpl && !named && filled.every((v) => v.trim() !== "");
  const send = () =>
    act.run(async () => {
      await post(
        "/v1/conversations/reply",
        windowOpen
          ? { channel, customer, text, request_id: requestId }
          : {
              channel,
              customer,
              request_id: requestId,
              template: {
                name: tpl?.name,
                language: tpl?.language,
                parameters: filled,
                body: tpl ? fillTemplate(tpl.body, filled) : "",
              },
            },
      );
      setText("");
      setValues([]);
      setRequestId(crypto.randomUUID());
      onSent();
    }, "Sent");
  return (
    <section
      className="card"
      style={{ marginTop: 16 }}
      data-testid="conversation-reply"
    >
      <h2 style={{ marginTop: 0 }}>Answer them</h2>
      {windowOpen ? (
        <Field
          label="Your message"
          hint="Sent from your business's number, as your business. Your assistant stays quiet for them for 24 hours after you answer."
        >
          <textarea
            rows={3}
            maxLength={4000}
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              setRequestId(crypto.randomUUID()); // a changed message is a new one
            }}
          />
        </Field>
      ) : (
        <>
          <p className="hint">
            They last wrote more than 24 hours ago, so WhatsApp only lets you
            send one of your approved templates.
          </p>
          <ErrorBox error={templates.error} />
          {templates.data && templates.data.templates.length === 0 && (
            <p className="hint">
              You have no approved templates. Make one in WhatsApp Manager; once
              WhatsApp approves it, it shows here.
            </p>
          )}
          <Field label="Template">
            <select
              value={chosen}
              onChange={(e) => {
                setChosen(e.target.value);
                setValues([]);
                setRequestId(crypto.randomUUID());
              }}
            >
              <option value="">Choose…</option>
              {(templates.data?.templates ?? []).map((t) => (
                <option
                  key={`${t.name}|${t.language}`}
                  value={`${t.name}|${t.language}`}
                >
                  {t.name} ({t.language})
                </option>
              ))}
            </select>
          </Field>
          {tpl && <p className="hint">{tpl.body}</p>}
          {named && (
            <p className="hint">
              This template has named blanks, which cannot be filled in here
              yet. Choose another one.
            </p>
          )}
          {Array.from({ length: slots }, (_, i) => (
            <Field key={i} label={`Fill in {{${i + 1}}}`}>
              <input
                value={values[i] ?? ""}
                onChange={(e) => {
                  setValues(
                    Object.assign([...values], { [i]: e.target.value }),
                  );
                  setRequestId(crypto.randomUUID());
                }}
              />
            </Field>
          ))}
        </>
      )}
      <ErrorBox error={act.error} />
      <button
        className="primary"
        disabled={act.busy || !ready}
        onClick={() => void send()}
      >
        {act.busy ? "Sending…" : "Send"}
      </button>
    </section>
  );
}

/** Talk to it here: write as a customer; see what it would answer. */
function TalkToIt({ assistant }: { assistant: AssistantView }) {
  const [text, setText] = useState("");
  const [lines, setLines] = useState<
    { who: "you" | "it" | "note"; text: string; run?: string }[]
  >([]);
  const [pending, setPending] = useState<string>();
  const act = useAction();
  const note = (text: string, run?: string) =>
    setLines((l) => [...l, { who: "note", text, run }]);
  useEffect(() => {
    if (!pending) return;
    let stop = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let tries = 0;
    const tick = async () => {
      tries++;
      try {
        const r = await get<{
          ended: boolean;
          status: string;
          turns: TurnView[];
        }>(`/v1/conversations/test/${pending}`);
        if (stop) return;
        if (r.ended) {
          const said = r.turns.map((t) => ({
            who: (t.role === "held" ? "note" : "it") as "it" | "note",
            text:
              t.role === "held"
                ? `${statusWords(t)}${t.text ? `: “${t.text}”` : ""}`
                : t.text || kindWords(t.kind),
            run: t.run_id,
          }));
          setLines((l) => [
            ...l,
            ...(said.length
              ? said
              : [
                  {
                    who: "note" as const,
                    text:
                      r.status === "completed"
                        ? "It sent nothing."
                        : "It did not finish: see how it ran.",
                    run: pending,
                  },
                ]),
          ]);
          setPending(undefined);
          return;
        }
        if (tries >= 40) {
          // About a minute: still working (a slow model, a long voice note).
          note("It is taking long. See how it is running.", pending);
          setPending(undefined);
          return;
        }
        timer = setTimeout(() => void tick(), 1500);
      } catch {
        if (stop) return;
        note("Its answer could not be read. See how it ran.", pending);
        setPending(undefined);
      }
    };
    void tick();
    return () => {
      stop = true;
      if (timer) clearTimeout(timer);
    };
  }, [pending]);
  const send = () =>
    act.run(async () => {
      const said = text.trim();
      const r = await post<{
        run?: string;
        dropped?: string;
        outcome?: string;
        reason?: string;
      }>("/v1/conversations/test", {
        workflow: assistant.workflow,
        environment: assistant.environment,
        text: said,
      });
      setLines((l) => [...l, { who: "you", text: said }]);
      setText("");
      if (r.run) {
        if (r.outcome === "quiet")
          note(
            `${statusWords({ role: "customer", status: "quiet", reason: r.reason })}. Start again to forget it.`,
          );
        setPending(r.run);
      } else
        note(
          statusWords({
            role: "customer",
            status: r.dropped ? "dropped" : r.outcome,
            reason: r.dropped ?? r.reason,
          }) || "It did not answer.",
        );
    }, false);
  const reset = () =>
    act.run(async () => {
      await del("/v1/conversations/test");
      setLines([]);
      setPending(undefined);
    }, false);
  return (
    <section
      className="card"
      style={{ marginTop: 24 }}
      data-testid="talk-to-it"
    >
      <h2 style={{ marginTop: 0 }}>Talk to it here</h2>
      <p className="hint">
        Write as a customer would. It answers from what it really knows, with
        its real model, but sends nothing: no message goes out and nothing is
        changed. It uses some of your monthly allowance. It does not remember
        your earlier test messages.
      </p>
      <div
        className="chat-stream"
        style={{ maxHeight: 360 }}
        role="log"
        aria-live="polite"
        aria-label="Your test chat"
        tabIndex={0}
      >
        {lines.map((l, i) => (
          <div key={i} className={l.who === "you" ? "bubble" : "bubble mine"}>
            <span className="who">
              {l.who === "you"
                ? "You, as a customer"
                : l.who === "it"
                  ? "Your assistant"
                  : "Note"}
            </span>
            <span className="text">{l.text}</span>
            {l.run && (
              <span className="hint" style={{ display: "block", fontSize: 12 }}>
                <Link to={`/runs/${l.run}`}>See how it was answered</Link>
              </span>
            )}
          </div>
        ))}
        {pending && (
          <p className="hint">Answering… this can take a few seconds.</p>
        )}
      </div>
      <ErrorBox error={act.error} />
      <form
        className="toolbar"
        onSubmit={(e) => {
          e.preventDefault();
          void send();
        }}
      >
        <input
          value={text}
          maxLength={4000}
          onChange={(e) => setText(e.target.value)}
          placeholder="Do you deliver to Ajah?"
          aria-label="Your message, as a customer"
        />
        <button type="submit" disabled={act.busy || !!pending || !text.trim()}>
          Send
        </button>
        <button
          type="button"
          disabled={act.busy}
          onClick={() => void reset()}
          title="Forget this test chat, with STOP or a hand-over you tried"
        >
          Start again
        </button>
      </form>
    </section>
  );
}
