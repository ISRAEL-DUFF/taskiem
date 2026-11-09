// Tasks for staff (docs/spec/chat-five-more-flows.md, flow 2; phase 3
// plan, step 4). Everyone sees their own tasks (and their role's not yet
// taken) with Done and Can't; those who run the business also give tasks,
// see everyone's with Remind, Give to someone else and Call off, and the
// tasks that repeat. The page, and the same panel in the chat's Tasks sheet.

import { useState } from "react";
import { Link } from "react-router-dom";
import { Sheet } from "../kit";
import { del, get, post, upload } from "../api";
import { useAuth } from "../auth";
import { EmptyState, ErrorBox, Field, Items, Modal, PageHeader, Row, Skeleton, useAction, useLoad } from "../ui";
import { roleWords, WEEKDAYS, type Weekday } from "../lib/team";
import {
  emptyTask,
  lagosToday,
  linkedWhat,
  repeatWords,
  taskBadge,
  taskProblem,
  taskRequest,
  taskSub,
  taskWho,
  type StaffTask,
  type TaskForm,
  type TaskLink,
  type TasksView,
} from "../lib/tasks";

interface Person {
  id: string;
  name: string;
  roles: string[];
}
type View = TasksView & { people: Person[] };

export function Tasks() {
  const [giving, setGiving] = useState(false);
  const [tick, setTick] = useState(0);
  const mine = useLoad(() => get<View>("/v1/staff/tasks"), [tick]);
  return (
    <>
      <PageHeader
        title="Tasks"
        description="Things to do, given to a person or to anyone in a role. They hear in their chat and reply DONE when it is done."
        actions={
          mine.data?.give && (
            <button type="button" className="primary" onClick={() => setGiving(true)}>
              Give a task
            </button>
          )
        }
      />
      <TasksPanel tick={tick} view={mine} />
      {giving && mine.data && (
        <GiveTask
          people={mine.data.people}
          onClose={() => setGiving(false)}
          onDone={() => (setGiving(false), setTick((n) => n + 1))}
        />
      )}
    </>
  );
}

/** The person's tasks and, for those who give them, everyone's. */
export function TasksPanel({ view, tick = 0, onGive }: { view: ReturnType<typeof useLoad<View>>; tick?: number; onGive?: () => void }) {
  const [all, setAll] = useState(false);
  // A task given reloads both lists; Mine or Everyone's stays as chosen.
  const everyone = useLoad(() => (all ? get<View>("/v1/staff/tasks?all=true") : Promise.resolve(undefined)), [all, tick]);
  const act = useAction();
  const [cant, setCant] = useState<StaffTask>();
  const [pass, setPass] = useState<StaffTask>();
  const v = all ? everyone.data : view.data;
  const reload = () => (all ? everyone.reload() : view.reload());
  const run = (fn: () => Promise<unknown>, done: string) => void act.run(async () => (await fn(), reload()), done);
  const error = (all ? everyone.error : view.error) ?? act.error;
  return (
    <>
      {view.data?.give && (
        <div className="segmented" role="group" aria-label="Whose tasks">
          <button type="button" aria-pressed={!all} onClick={() => setAll(false)}>
            Mine
          </button>
          <button type="button" aria-pressed={all} onClick={() => setAll(true)}>
            Everyone's
          </button>
        </div>
      )}
      <ErrorBox error={error} />
      {!v && !error && <Skeleton rows={3} label="Loading the tasks…" />}
      {v && v.tasks.length === 0 && (
        <EmptyState
          icon="check"
          action={
            onGive && view.data?.give ? (
              <button type="button" className="primary" onClick={onGive}>
                Give a task
              </button>
            ) : undefined
          }
        >
          {all ? "No tasks this week." : "You have no tasks. When someone gives you one, it shows here and in your chat."}
        </EmptyState>
      )}
      {v && v.tasks.length > 0 && (
        <Items label={all ? "Everyone's tasks" : "Your tasks"}>
          {v.tasks.map((t) => {
            const badge = taskBadge(t);
            const open = t.state === "open";
            const free = open && !!t.to_role && !t.taken_by;
            return (
              <Row
                key={t.id}
                icon={t.state === "done" ? "check" : "clock"}
                tone={badge?.tone === "ok" ? "ok" : badge?.tone === "warn" ? "warn" : badge?.tone === "danger" ? "danger" : undefined}
                title={t.what}
                side={badge && <span className={`badge ${badge.tone === "muted" ? "" : badge.tone}`}>{badge.text}</span>}
                sub={<span className="sub">{taskSub(t, !all)}</span>}
                testid="task"
              >
                <div className="toolbar">
                  {t.photo_url && (
                    <a href={t.photo_url} target="_blank" rel="noopener noreferrer">
                      See the photo<span className="sr-only"> for {t.what} (opens in a new tab)</span>
                    </a>
                  )}
                  {!all && free && (
                    <button type="button" onClick={() => run(() => post(`/v1/staff/tasks/${t.id}/take`, {}), "It is yours")}>
                      I'll do it<span className="sr-only">: {t.what}</span>
                    </button>
                  )}
                  {!all && open && !t.photo && (
                    <button type="button" className="primary" onClick={() => run(() => post(`/v1/staff/tasks/${t.id}/done`, {}), "Done. Thank you.")}>
                      Done<span className="sr-only">: {t.what}</span>
                    </button>
                  )}
                  {!all && open && t.photo && (
                    <label className="button primary">
                      Done, with a photo<span className="sr-only">: {t.what}</span>
                      <input
                        type="file"
                        accept="image/*"
                        capture="environment"
                        className="sr-only"
                        onChange={(e) => {
                          const file = e.currentTarget.files?.[0];
                          e.currentTarget.value = "";
                          if (!file) return;
                          const form = new FormData();
                          form.append("photo", file);
                          run(() => upload(`/v1/staff/tasks/${t.id}/done`, form), "Done, with the photo. Thank you.");
                        }}
                      />
                    </label>
                  )}
                  {!all && open && (
                    <button type="button" onClick={() => setCant(t)}>
                      Can't<span className="sr-only">: {t.what}</span>
                    </button>
                  )}
                  {all && open && (
                    <button type="button" onClick={() => run(() => post(`/v1/staff/tasks/${t.id}/remind`, {}), `Reminded ${taskWho(t)}`)}>
                      Remind<span className="sr-only"> {taskWho(t)}: {t.what}</span>
                    </button>
                  )}
                  {all && (open || t.state === "cant") && (
                    <button type="button" onClick={() => setPass(t)}>
                      Give to someone else<span className="sr-only">: {t.what}</span>
                    </button>
                  )}
                  {all && (open || t.state === "cant") && (
                    <button
                      type="button"
                      className="danger"
                      onClick={() => {
                        if (window.confirm(`Call off "${t.what}"?`)) run(() => del(`/v1/staff/tasks/${t.id}`), "Called off");
                      }}
                    >
                      Call off<span className="sr-only">: {t.what}</span>
                    </button>
                  )}
                </div>
              </Row>
            );
          })}
        </Items>
      )}
      {all && v && v.repeats.length > 0 && (
        <section aria-labelledby="task-repeats">
          <h2 id="task-repeats">Repeating</h2>
          <Items label="Repeating tasks">
            {v.repeats.map((r) => (
              <Row
                key={r.id}
                icon="loop"
                title={r.what}
                sub={<span className="sub">{[taskWho({ ...r, taken_by: undefined }), repeatWords(r), r.photo ? "photo when done" : ""].filter(Boolean).join(" · ")}</span>}
                side={
                  <button
                    type="button"
                    onClick={() => {
                      if (window.confirm(`Stop "${r.what}"? Tasks it made stay.`)) run(() => del(`/v1/staff/task-repeats/${r.id}`), "Stopped");
                    }}
                  >
                    Stop<span className="sr-only">: {r.what}</span>
                  </button>
                }
                testid="task-repeat"
              />
            ))}
          </Items>
        </section>
      )}
      {cant && (
        <CantTask
          task={cant}
          onClose={() => setCant(undefined)}
          onDone={() => (setCant(undefined), reload())}
        />
      )}
      {pass && view.data && (
        <PassTask
          task={pass}
          people={view.data.people}
          onClose={() => setPass(undefined)}
          onDone={() => (setPass(undefined), reload())}
        />
      )}
    </>
  );
}

/** Who a task can be for: each person, then anyone in each role. */
function WhoSelect({ people, value, onChange }: { people: Person[]; value: string; onChange: (v: string) => void }) {
  const roles = [...new Set(people.flatMap((p) => p.roles))].filter((r) => r !== "owner").sort();
  return (
    <select value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">Choose…</option>
      <optgroup label="A person">
        {people.map((p) => (
          <option key={p.id} value={`user:${p.id}`}>
            {p.name}
          </option>
        ))}
      </optgroup>
      {roles.length > 0 && (
        <optgroup label="Anyone in a role">
          {roles.map((r) => (
            <option key={r} value={`role:${r}`}>
              Any {roleWords(r).toLowerCase()}
            </option>
          ))}
        </optgroup>
      )}
    </select>
  );
}

/** Giving a task, maybe about something (a card's "Give this to…"). */
export function GiveTask({ people, link, onClose, onDone }: { people: Person[]; link?: TaskLink; onClose: () => void; onDone: () => void }) {
  const [f, setF] = useState<TaskForm>(() => ({ ...emptyTask(lagosToday()), what: link ? linkedWhat(link) : "" }));
  const [tried, setTried] = useState(false);
  const act = useAction();
  const problem = taskProblem(f);
  const set = (x: Partial<TaskForm>) => setF({ ...f, ...x });
  return (
    <Modal title={link ? "Give this to someone" : "Give a task"} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setTried(true);
          if (problem) return;
          void act.run(async () => (await post("/v1/staff/tasks", taskRequest(f, link)), onDone()), "Task given. They hear in their chat.");
        }}
      >
        <Field label="What is to be done">
          <input value={f.what} onChange={(e) => set({ what: e.target.value })} maxLength={200} autoComplete="off" placeholder="Count the cash float" />
        </Field>
        <Field label="Who it is for" hint="For anyone in a role, the first to take it has it.">
          <WhoSelect people={people} value={f.who} onChange={(who) => set({ who })} />
        </Field>
        <Field label="When">
          <select value={f.when} onChange={(e) => set({ when: e.target.value as TaskForm["when"] })}>
            <option value="none">Any time</option>
            <option value="by">By a time</option>
            <option value="day">Every day</option>
            <option value="week">Every week</option>
          </select>
        </Field>
        {f.when === "by" && (
          <Field label="Day">
            <input type="date" value={f.date} onChange={(e) => set({ date: e.target.value })} />
          </Field>
        )}
        {f.when === "week" && (
          <Field label="On">
            <select value={f.day} onChange={(e) => set({ day: e.target.value as Weekday })}>
              {WEEKDAYS.map((d) => (
                <option key={d.value} value={d.value}>
                  {d.label}
                </option>
              ))}
            </select>
          </Field>
        )}
        {f.when !== "none" && (
          <Field label={f.when === "by" ? "By" : "Due at"}>
            <input type="time" value={f.time} onChange={(e) => set({ time: e.target.value })} />
          </Field>
        )}
        <label className="check">
          <input type="checkbox" checked={f.photo} onChange={(e) => set({ photo: e.target.checked })} /> A photo when it is done
        </label>
        {tried && problem && (
          <p className="error-text" role="alert">
            {problem}
          </p>
        )}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy}>
            Give the task
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

function CantTask({ task, onClose, onDone }: { task: StaffTask; onClose: () => void; onDone: () => void }) {
  const [why, setWhy] = useState("");
  const act = useAction();
  return (
    <Modal title="Can't do it" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => (await post(`/v1/staff/tasks/${task.id}/cant`, { why: why.trim() }), onDone()), `Sent back to ${task.giver}`);
        }}
      >
        <p>
          {task.what}. {task.giver} hears that you can't, and why.
        </p>
        <Field label="Why">
          <input value={why} onChange={(e) => setWhy(e.target.value)} maxLength={300} placeholder="No fuel" autoComplete="off" />
        </Field>
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy}>
            Send it back
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

function PassTask({ task, people, onClose, onDone }: { task: StaffTask; people: Person[]; onClose: () => void; onDone: () => void }) {
  const [who, setWho] = useState("");
  const act = useAction();
  return (
    <Modal title="Give to someone else" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!who) return;
          const [kind, id] = [who.slice(0, who.indexOf(":")), who.slice(who.indexOf(":") + 1)];
          void act.run(
            async () => (await post(`/v1/staff/tasks/${task.id}/give`, kind === "user" ? { to_user: id } : { to_role: id }), onDone()),
            "Given. They hear in their chat.",
          );
        }}
      >
        <p>{task.what}</p>
        <Field label="Who it is for now">
          <WhoSelect people={people} value={who} onChange={setWho} />
        </Field>
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy || !who}>
            Give it
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** The chat's Tasks tile: the person's tasks (and, for those who give
 * them, everyone's and Give a task) in a sheet. */
export function TasksSheet({ onClose }: { onClose: () => void }) {
  const [giving, setGiving] = useState(false);
  const [tick, setTick] = useState(0);
  const view = useLoad(() => get<View>("/v1/staff/tasks"), [tick]);
  return (
    <Sheet
      title="Tasks"
      onClose={onClose}
      testid="tasks-sheet"
      headExtra={<Link to="/tasks">Open the page</Link>}
      footer={
        view.data?.give && (
          <button type="button" className="primary" onClick={() => setGiving(true)}>
            Give a task
          </button>
        )
      }
    >
      <TasksPanel tick={tick} view={view} onGive={() => setGiving(true)} />
      {giving && view.data && (
        <GiveTask people={view.data.people} onClose={() => setGiving(false)} onDone={() => (setGiving(false), setTick((n) => n + 1))} />
      )}
    </Sheet>
  );
}

/** "Give this to…" on a card (an order, an invoice, a bill, an order to a
 * supplier, a low stock item): a task about it, for those who give tasks. */
export function GiveThis({ link, className }: { link: TaskLink; className?: string }) {
  const { can } = useAuth();
  const [people, setPeople] = useState<Person[]>();
  const [err, setErr] = useState<unknown>();
  if (!can("member.manage") && !can("invoice.manage")) return null;
  return (
    <>
      <button
        type="button"
        className={className}
        onClick={() =>
          void get<View>("/v1/staff/tasks").then(
            (v) => setPeople(v.people),
            (e) => setErr(e),
          )
        }
      >
        Give this to…<span className="sr-only"> {link.name ?? ""}</span>
      </button>
      {err !== undefined && <ErrorBox error={err} />}
      {people && <GiveTask people={people} link={link} onClose={() => setPeople(undefined)} onDone={() => setPeople(undefined)} />}
    </>
  );
}
