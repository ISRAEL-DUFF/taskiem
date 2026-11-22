import { useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { ApiError, get, post, postOnce, put, type JSONSchema } from "../api";
import { useAuth } from "../auth";
import { SchemaForm } from "../forms/SchemaForm";
import { Icon } from "../icons";
import { naira } from "../lib/invoices";
import {
  answerTone,
  answerWords,
  careWords,
  firstRows,
  nextAsk,
  pressable,
  pressErrorWords,
  startErrorWords,
  stillGoing,
  type PressedButton,
  type ToolAnswer,
  type ToolButton,
  type ToolStart,
  type ToolValue,
} from "../lib/toolanswer";
import { madeCare, madeIcon, type MadeTool } from "../lib/tools";
import { reloadMadeTools, useMadeTools } from "../lib/useMadeTools";
import { passkeysSupported, stepUpAssert } from "../passkeys";
import { KitEmpty, Pill, Sheet } from "../kit";
import { toast } from "../toast";
import { EmptyState, ErrorBox, PageHeader, Skeleton } from "../ui";

// Your tools (made tools M1): a tool's sheet. Its form, drawn from the
// tool's inputs; then its care (made tools §8): a tool that only reads
// starts at once and shows what it read; any other shows what will run and
// asks yes, and one that moves money asks for the passkey too. Then its
// answer (§7), asked for gently until the run ends: a sentence, a card in
// the chat's card frame, or a list whose buttons open the tools they name.

/** A tool to open, with the values its form starts with, and the button
 * pressed to open it (its token, sent with the start). */
export type OpenTool = (
  command: string,
  values?: Record<string, unknown>,
  button?: string,
) => void;

type Phase =
  | { at: "form" }
  | { at: "confirm"; start: ToolStart }
  | { at: "run"; run: string; values: ToolValue[]; echo: boolean };

const newKey = () =>
  typeof crypto !== "undefined" && "randomUUID" in crypto
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random()}`;

const path = (command: string, what: string) =>
  `/v1/tools/${encodeURIComponent(command)}/${what}`;

/** The run's answer, asked for again while the run is on its way. */
export function useAnswer(run: string | undefined): {
  answer?: ToolAnswer;
  error?: unknown;
} {
  const [answer, setAnswer] = useState<ToolAnswer>();
  const [error, setError] = useState<unknown>();
  useEffect(() => {
    if (!run) return;
    let live = true;
    let timer = 0;
    let asked = 0;
    const ask = () =>
      get<ToolAnswer>(`/v1/runs/${encodeURIComponent(run)}/answer`).then(
        (a) => {
          if (!live) return;
          setAnswer(a);
          setError(undefined);
          if (stillGoing(a.status))
            timer = window.setTimeout(ask, nextAsk(asked++));
        },
        (e: unknown) => {
          if (!live) return;
          setError(e);
          // Lost for a moment: asked again, more slowly.
          if (asked < 20) timer = window.setTimeout(ask, nextAsk(asked++ + 4));
        },
      );
    void ask();
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
  }, [run]);
  return { answer, error };
}

/** The values as rows: what will run, or what was read. */
function Values({ values, label }: { values: ToolValue[]; label: string }) {
  if (values.length === 0)
    return <p className="tk-form-now">Nothing to fill in.</p>;
  return (
    <dl className="tk-tool-values" aria-label={label}>
      {values.map((v) => (
        <div key={v.name}>
          <dt>{v.label}</dt>
          <dd>{v.value}</dd>
        </div>
      ))}
    </dl>
  );
}

/** An answer's buttons: each pressed by its token, which opens the tool
 * the server says with the values it gives, for whoever pressed it (made
 * tools M2). A once button someone used says who took it and greys; one
 * that is gone says so. */
function AnswerButtons({
  bs,
  onOpenTool,
  row,
}: {
  bs?: ToolButton[];
  onOpenTool?: OpenTool;
  row?: string;
}) {
  const [busy, setBusy] = useState<number>();
  const [spent, setSpent] = useState<Record<number, true>>({});
  const [note, setNote] = useState<string>();
  const shown = pressable(bs);
  if (shown.length === 0) return null;
  const press = async (b: ToolButton, i: number) => {
    if (!onOpenTool || !b.token) return;
    setBusy(i);
    setNote(undefined);
    try {
      const p = await post<PressedButton>(
        `/v1/tools/buttons/${encodeURIComponent(b.token)}`,
      );
      onOpenTool(p.command, p.inputs ?? {}, b.token);
    } catch (e) {
      const why = pressErrorWords(e);
      if (why.spent) {
        setSpent((s) => ({ ...s, [i]: true }));
        setNote(why.words);
      } else toast(why.words, "error");
    } finally {
      setBusy(undefined);
    }
  };
  return (
    <>
      <span className="tk-chatcard-actions">
        {shown.map(({ b, i }) => (
          <button
            key={i}
            type="button"
            className="small"
            onClick={() => void press(b, i)}
            disabled={!onOpenTool || busy !== undefined || !!spent[i]}
            data-spent={spent[i] ? "" : undefined}
          >
            {b.label}
            {row && <span className="sr-only">: {row}</span>}
          </button>
        ))}
      </span>
      {note && (
        <span className="tk-answer-taken" role="status">
          {note}
        </span>
      )}
    </>
  );
}

/** A tool's answer as the chat draws it: a sentence as a message, a card
 * in the card frame, a list as a card with its first rows and See all. */
export function AnswerView({
  answer: a,
  run,
  name,
  onOpenTool,
}: {
  answer: ToolAnswer;
  run: string;
  name: string;
  onOpenTool?: OpenTool;
}) {
  const [all, setAll] = useState(false);
  const words = answerWords(a);
  if (words !== undefined) {
    const bad = a.status === "failed" || a.status === "needs_reconciliation";
    return (
      <div
        className={`tk-answer-note${bad ? " bad" : ""}`}
        role="status"
        data-testid="tool-answer"
        data-status={a.status}
      >
        <p>{words}</p>
        {(bad || a.status === "cancelled") && (
          <Link to={`/runs/${encodeURIComponent(run)}`}>
            Open the run to see why
          </Link>
        )}
      </div>
    );
  }
  if (a.type === "text")
    return (
      <div
        className="tk-answer-text"
        role="status"
        data-testid="tool-answer"
        data-status={a.status}
      >
        <span className="sr-only">{name} says: </span>
        {a.text}
      </div>
    );
  if (a.type === "card")
    return (
      <article
        className="tk-chatcard tk-answer-card"
        aria-label={a.title || name}
        data-testid="tool-answer"
        data-status={a.status}
      >
        {(a.ref || a.state?.text) && (
          <span className="tk-chatcard-top">
            <span className="tk-kicker">{a.ref}</span>
            {a.state?.text && (
              <Pill tone={answerTone(a.state.tone)}>{a.state.text}</Pill>
            )}
          </span>
        )}
        <span className="card-title">{a.title || name}</span>
        {a.amount_kobo !== undefined && (
          <span className="tk-answer-amount">{naira(a.amount_kobo)}</span>
        )}
        {(a.lines ?? []).length > 0 && (
          <dl className="tk-tool-values">
            {(a.lines ?? []).map((l, i) => (
              <div key={i}>
                <dt>{l.label}</dt>
                <dd>{l.value}</dd>
              </div>
            ))}
          </dl>
        )}
        <AnswerButtons bs={a.buttons} onOpenTool={onOpenTool} />
      </article>
    );
  const rows = a.rows ?? [];
  const { shown, more } = firstRows(rows);
  const list = all ? rows : shown;
  return (
    <article
      className="tk-chatcard tk-answer-card"
      aria-label={a.title || name}
      data-testid="tool-answer"
      data-status={a.status}
    >
      <span className="card-title">{a.title || name}</span>
      {rows.length === 0 ? (
        <span className="tk-chatcard-sub">{a.empty || "Nothing to show."}</span>
      ) : (
        <ul className="tk-answer-rows" aria-label={a.title || name}>
          {list.map((r, i) => (
            <li key={i}>
              <span className="tk-answer-row">
                <span className="tk-row-words">
                  <span className="tk-row-name">{r.title}</span>
                  {r.sub && <span className="tk-row-sub">{r.sub}</span>}
                </span>
                {r.state?.text && (
                  <Pill tone={answerTone(r.state.tone)}>{r.state.text}</Pill>
                )}
              </span>
              {all && (
                <AnswerButtons
                  bs={r.actions}
                  onOpenTool={onOpenTool}
                  row={r.title}
                />
              )}
            </li>
          ))}
        </ul>
      )}
      {!all &&
        (more > 0 || rows.some((r) => pressable(r.actions).length > 0)) && (
          <span className="tk-chatcard-actions">
            <button type="button" className="small" onClick={() => setAll(true)}>
              {more > 0 ? `See all (${more} more)` : "See all"}
            </button>
          </span>
        )}
    </article>
  );
}

/** What confirming with a passkey needs said, when it cannot be asked. */
type NeedsKey = { methods: string[] };

/** A tool's form, its care and its answer: the sheet's body and foot. */
export function ToolRunner({
  tool,
  values,
  button,
  onOpenTool,
  onStarted,
}: {
  tool: MadeTool;
  /** The form's first values (a button's). */
  values?: Record<string, unknown>;
  /** The button pressed to open it: sent with the start, which spends a
   * once button (the confirm keeps it). */
  button?: string;
  onOpenTool?: OpenTool;
  /** A run started (the conversation may show what is newer). */
  onStarted?: (run: string) => void;
}) {
  const [input, setInput] = useState<Record<string, unknown> | undefined>(
    values,
  );
  const [phase, setPhase] = useState<Phase>({ at: "form" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [needsKey, setNeedsKey] = useState<NeedsKey>();
  // One start at once per filled-in form: sent again, the same run.
  const key = useRef(newKey());
  const care = madeCare(tool);
  const schema = tool.inputs as JSONSchema | undefined;
  const fields = Object.keys(schema?.properties ?? {}).length > 0;
  const run = phase.at === "run" ? phase.run : undefined;
  const { answer, error: answerError } = useAnswer(run);
  const act = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError(undefined);
    try {
      await fn();
    } catch (e) {
      setError(startErrorWords(e));
    } finally {
      setBusy(false);
    }
  };
  const started = (id: string, shown: ToolValue[], echo: boolean) => {
    setPhase({ at: "run", run: id, values: shown, echo });
    onStarted?.(id);
  };
  const start = () =>
    act(async () => {
      const r = await postOnce<ToolStart>(path(tool.command, "start"), key.current, {
        input: input ?? {},
        // The web's Taskiem conversation also keeps the answer's words.
        answer_in_chat: true,
        ...(button && { button }),
      });
      if (r.run_id) started(r.run_id, r.values, true);
      else setPhase({ at: "confirm", start: r });
    });
  const confirm = (s: ToolStart) =>
    act(async () => {
      const send = (proof: Record<string, unknown>) =>
        post<{ run_id: string }>(path(tool.command, "confirm"), {
          confirm: s.confirm,
          input: input ?? {},
          ...proof,
        });
      let r: { run_id: string };
      try {
        r = await send({});
      } catch (e) {
        if (!(e instanceof ApiError) || e.status !== 403 || !e.body.step_up)
          throw e;
        const methods = Array.isArray(e.body.methods)
          ? (e.body.methods as string[])
          : [];
        if (!methods.includes("passkey") || !passkeysSupported()) {
          setNeedsKey({ methods });
          return;
        }
        const op =
          typeof e.body.operation === "string" ? e.body.operation : "tool.start";
        const target =
          typeof e.body.target === "string"
            ? e.body.target
            : `tool:${tool.command}`;
        r = await send({ passkey: await stepUpAssert(op, target) });
      }
      started(r.run_id, s.values, false);
    });

  if (phase.at === "run")
    return (
      <>
        <div className="tk-sheet-list tk-form-body">
          {phase.echo && phase.values.length > 0 && (
            <div className="tk-tool-echo">
              <p className="tk-form-now">Read as:</p>
              <Values values={phase.values} label="What was read" />
            </div>
          )}
          {answer ? (
            <AnswerView
              answer={answer}
              run={phase.run}
              name={tool.name}
              onOpenTool={onOpenTool}
            />
          ) : answerError ? (
            <ErrorBox error={answerError} />
          ) : (
            <div className="tk-answer-note" role="status" data-testid="tool-answer" data-status="running">
              <p>Working on it…</p>
            </div>
          )}
        </div>
        <div className="tk-sheet-foot">
          <button
            type="button"
            className="tk-cta"
            onClick={() => {
              key.current = newKey();
              setPhase({ at: "form" });
            }}
          >
            Use it again
          </button>
        </div>
      </>
    );

  if (phase.at === "confirm")
    return (
      <>
        <div className="tk-sheet-list tk-form-body">
          <p className="tk-form-now">
            {phase.start.care === "step_up"
              ? "This moves money. Check what will run, then confirm with your passkey."
              : "Check what will run, then say yes."}
          </p>
          <Values values={phase.start.values} label="What will run" />
          {needsKey && (
            <div className="warning" role="alert" data-testid="tool-needs-passkey">
              {needsKey.methods.length === 0
                ? "This tool needs a passkey: add one under Account first."
                : "This browser cannot use your passkey here."}{" "}
              <Link to="/account">Go to Account</Link>
            </div>
          )}
          {error && (
            <p className="tk-field-error" role="alert">
              {error}
            </p>
          )}
        </div>
        <div className="tk-sheet-foot tk-foot-two">
          <button
            type="button"
            className="tk-btn ghost"
            disabled={busy}
            onClick={() => {
              setNeedsKey(undefined);
              setPhase({ at: "form" });
            }}
          >
            Change
          </button>
          <button
            type="button"
            className="tk-cta"
            disabled={busy}
            onClick={() => void confirm(phase.start)}
          >
            {phase.start.care === "step_up" ? "Yes, with my passkey" : "Yes"}
          </button>
        </div>
      </>
    );

  return (
    <>
      <div className="tk-sheet-list tk-form-body">
        {fields ? (
          <SchemaForm
            schema={schema}
            value={input}
            expressions={false}
            onChange={(v) => {
              key.current = newKey();
              setInput(v);
            }}
          />
        ) : (
          <p className="tk-form-now">Nothing to fill in.</p>
        )}
        <p className="tk-form-now tk-tool-care">
          <Icon name={care === "answers" ? "bolt" : "lock"} />
          {care === "answers"
            ? "It answers at once, and shows what it read."
            : care === "yes"
              ? "It shows what will run and asks yes first."
              : "It moves money: it asks yes and your passkey first."}
        </p>
        {error && (
          <p className="tk-field-error" role="alert">
            {error}
          </p>
        )}
      </div>
      <div className="tk-sheet-foot">
        <button
          type="button"
          className="tk-cta"
          disabled={busy}
          onClick={() => void start()}
        >
          {care === "answers" ? "Go" : "Next"}
        </button>
      </div>
    </>
  );
}

/** A tool's sheet, over the chat or a page; a button on its answer opens
 * the tool it names in its place. */
export function MadeToolSheet({
  command,
  values,
  from,
  onClose,
  onStarted,
  onTools,
}: {
  command: string;
  values?: Record<string, unknown>;
  from?: DOMRect | null;
  onClose: () => void;
  onStarted?: (run: string) => void;
  /** Back out to the tools panel. */
  onTools?: () => void;
}) {
  const { can } = useAuth();
  const tools = useMadeTools(can("run.start"));
  const [open, setOpen] = useState<{
    command: string;
    values?: Record<string, unknown>;
    button?: string;
    n: number;
  }>({ command, values, n: 0 });
  const tool = tools.find((t) => t.command === open.command);
  const openTool: OpenTool = (c, v, button) => {
    if (!tools.some((t) => t.command === c)) {
      toast("This is not available: that tool is not one of yours.", "error");
      return;
    }
    setOpen((o) => ({ command: c, values: v, button, n: o.n + 1 }));
  };
  return (
    <Sheet
      title={tool?.name ?? `/${open.command}`}
      sub={tool ? `/${tool.command} · ${tool.about}` : undefined}
      from={from}
      onClose={onClose}
      onOut={onTools}
      testid="made-tool-sheet"
    >
      {tool ? (
        <ToolRunner
          key={`${tool.command}:${open.n}`}
          tool={tool}
          values={open.values}
          button={open.button}
          onOpenTool={openTool}
          onStarted={onStarted}
        />
      ) : (
        <div className="tk-sheet-list">
          <KitEmpty icon="info" title="This tool is not here">
            It may have been turned off, or it is not one of yours.
          </KitEmpty>
        </div>
      )}
    </Sheet>
  );
}

/** /tools/{command}: a tool's own page, which opens its sheet (the chat
 * apps link a tool that moves money here, for the passkey), with the
 * owner's "Always ask before it runs". */
export function ToolPage() {
  const { command = "" } = useParams();
  const { can, me } = useAuth();
  const [loaded, setLoaded] = useState(false);
  const tools = useMadeTools(can("run.start"));
  const [open, setOpen] = useState(true);
  const [saving, setSaving] = useState(false);
  // The switch shows the change at once, while it is saved.
  const [want, setWant] = useState<boolean>();
  const [error, setError] = useState<unknown>();
  useEffect(() => {
    void reloadMadeTools().then(() => setLoaded(true));
  }, []);
  const tool = tools.find((t) => t.command === command);
  const owner = !!me?.roles.includes("owner");
  if (!loaded && !tool) return <Skeleton rows={2} label="Loading the tool…" />;
  if (!tool)
    return (
      <>
        <PageHeader title={`/${command}`} />
        <EmptyState
          icon="bolt"
          action={
            <Link className="button" to="/chat?with=taskiem">
              Open the chat
            </Link>
          }
        >
          There is no tool called /{command} that you can use here. It may
          have been turned off, or it is for other people in the business.
        </EmptyState>
      </>
    );
  const always = (on: boolean) => {
    setSaving(true);
    setWant(on);
    setError(undefined);
    put(path(tool.command, "settings"), { always_ask: on })
      .then(() => reloadMadeTools())
      .then(
        () => toast(on ? "It asks yes before it runs" : "Saved"),
        (e: unknown) => setError(e),
      )
      .finally(() => {
        setSaving(false);
        setWant(undefined);
      });
  };
  return (
    <>
      <p className="crumbs">
        <Link to="/chat?with=taskiem" aria-label="Back to Chat">
          Chat
        </Link>
      </p>
      <PageHeader title={tool.name} description={tool.about} />
      <section className="card tk-tool-page" aria-label="The tool" data-testid="tool-page">
        <div className="tk-tool-head">
          <span className="tk-tile-icon" aria-hidden="true">
            <Icon name={madeIcon(tool.icon)} />
          </span>
          <span>
            <strong>/{tool.command}</strong>
            <span className="sub">{careWords(madeCare(tool))}</span>
          </span>
        </div>
        <p>
          <button type="button" className="primary" onClick={() => setOpen(true)}>
            Use {tool.name}
          </button>
        </p>
      </section>
      {owner && (
        <section className="card" aria-labelledby="tool-care-h">
          <h2 id="tool-care-h">Before it runs</h2>
          <label className="switch-line">
            <input
              type="checkbox"
              role="switch"
              checked={want ?? (tool.always_ask || tool.care.level !== "answers")}
              disabled={saving || tool.care.level !== "answers"}
              onChange={(e) => always(e.target.checked)}
              data-testid="tool-always-ask"
            />
            <span>Always ask before it runs</span>
          </label>
          <p className="hint">
            {tool.care.level === "answers"
              ? "It only reads, so it answers at once and shows what it read. Turn this on to have it show what will run and ask yes first."
              : tool.care.level === "step_up"
                ? "It moves money, so it always asks yes and a passkey."
                : "Its steps change things, so it always asks yes first."}
          </p>
          <ErrorBox error={error} />
        </section>
      )}
      {open && (
        <MadeToolSheet command={tool.command} onClose={() => setOpen(false)} />
      )}
    </>
  );
}
