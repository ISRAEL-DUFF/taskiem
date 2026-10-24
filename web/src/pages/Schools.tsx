import { useState, type ReactNode } from "react";
import { Link, NavLink, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { ApiError, get, patch, post, put } from "../api";
import { useAuth } from "../auth";
import { EmptyState, ErrorBox, Field, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { StudentFeesPanel } from "./SchoolFees";
import { IssuingSettings, StudentAccountPanel } from "./SchoolAccountParts";
import { StudentReceiptsPanel } from "./SchoolReceipts";
import {
  PROMOTION_ACTIONS,
  STATUS_FILTERS,
  classProblem,
  classRequest,
  currentTerm,
  dateLabel,
  emptyStudentForm,
  familyName,
  guardianLine,
  importRefusal,
  nextOrder,
  nextSessionLabel,
  nextTermName,
  planLine,
  plainMessage,
  promotionCounts,
  promotionLine,
  promotionRequest,
  savedLine,
  schoolAccess,
  sessionProblem,
  startedLine,
  studentPath,
  studentProblem,
  studentRequest,
  studentStatus,
  studentsPath,
  termProblem,
  termStateWords,
  type ClassForm,
  type ImportPlan,
  type ImportRefusal,
  type PromotionAction,
  type SchoolClass,
  type SchoolFamily,
  type SchoolPromotion,
  type SchoolSession,
  type SchoolSettings,
  type SchoolStudent,
  type SchoolStudentDetail,
  type SchoolTerm,
  type StudentForm,
  type TermForm,
} from "../lib/schools";

// The Schools area (decision 0046, B2; docs/schools.md): students, their
// families, classes and terms, importing a school's students, promotion,
// and the switch that turns the area on.

/** Runs a request and words the server's refusal as a sentence. */
async function plainly<T>(p: Promise<T>): Promise<T> {
  try {
    return await p;
  } catch (e) {
    if (e instanceof ApiError && (e.status === 400 || e.status === 409)) e.message = plainMessage(e.message);
    throw e;
  }
}

function StatusPill({ status }: { status: string }) {
  const s = studentStatus(status);
  return <span className={`badge sf-status ${s.tone}`}>{s.label}</span>;
}

/** Students, Families, Classes and terms, Settings. */
export function SchoolTabs() {
  const { me } = useAuth();
  return (
    <nav className="tabs" aria-label="Schools">
      <NavLink to="/schools" end>
        Students
      </NavLink>
      <NavLink to="/schools/families">Families</NavLink>
      <NavLink to="/schools/classes">Classes and terms</NavLink>
      <NavLink to="/schools/accounts">Account numbers</NavLink>
      <NavLink to="/schools/payments">Payments</NavLink>
      <NavLink to="/schools/receipts">Receipts</NavLink>
      {me?.roles.includes("owner") && <NavLink to="/settings/schools">Settings</NavLink>}
    </nav>
  );
}

/** What a Schools page shows while the area is off. */
function AreaOff({ title }: { title: string }) {
  const { me } = useAuth();
  return (
    <>
      <PageHeader title={title} />
      <EmptyState icon="schools" action={me?.roles.includes("owner") ? <Link to="/settings/schools">Turn Schools on</Link> : undefined}>
        The Schools area is off for this business. An owner turns it on in Settings &gt; Schools.
      </EmptyState>
    </>
  );
}

function useClasses() {
  return useLoad(() => get<{ classes: SchoolClass[] }>("/v1/schools/classes"), []);
}

// --- students ---

/** Schools > Students (GET /v1/schools/students): every student, by admission number, with filters. */
export function Schools() {
  const { me, can } = useAuth();
  const may = schoolAccess(can);
  const [params, setParams] = useSearchParams();
  const shown = params.get("status") ?? "active";
  const filter = { class: params.get("class") ?? "", status: shown === "all" ? "" : shown, q: params.get("q") ?? "", reveal: params.get("reveal") === "true" };
  const [q, setQ] = useState(filter.q);
  const classes = useClasses();
  const list = useLoad(() => get<{ students: SchoolStudent[]; total: number; revealed: boolean }>(studentsPath(filter)), [filter.class, filter.status, filter.q, filter.reveal]);
  if (!me?.schools) return <AreaOff title="Students" />;
  const set = (k: string, v: string) => {
    const next = new URLSearchParams(params);
    if (v) next.set(k, v);
    else next.delete(k);
    setParams(next, { replace: true });
  };
  const actions = may.manage ? (
    <>
      <Link className="button" to="/schools/import">
        Import students
      </Link>
      <Link className="button primary" to="/schools/students/new">
        Add a student
      </Link>
    </>
  ) : undefined;
  const students = list.data?.students;
  return (
    <>
      <PageHeader title="Students" description="Every student of the school by admission number, with their class and status. Names are hidden unless you may see personal data and show them." actions={actions} />
      <SchoolTabs />
      <form
        className="filters"
        role="search"
        aria-label="Filter students"
        onSubmit={(e) => {
          e.preventDefault();
          set("q", q.trim());
        }}
      >
        <label>
          Class{" "}
          <select value={filter.class} onChange={(e) => set("class", e.target.value)}>
            <option value="">Every class</option>
            {(classes.data?.classes ?? []).map((c) => (
              <option key={c.id} value={c.id}>
                {c.label}
              </option>
            ))}
          </select>
        </label>
        <label>
          Status{" "}
          <select value={shown} onChange={(e) => set("status", e.target.value === "active" ? "" : e.target.value)}>
            {STATUS_FILTERS.map((f) => (
              <option key={f.label} value={f.value}>
                {f.label}
              </option>
            ))}
          </select>
        </label>
        <label>
          Search{" "}
          <input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={filter.reveal ? "Admission number or name" : "Admission number"} />
        </label>
        <button type="submit">Search</button>
        {may.reveal && (
          <label className="row">
            <input type="checkbox" checked={filter.reveal} onChange={(e) => set("reveal", e.target.checked ? "true" : "")} /> Show names
          </label>
        )}
      </form>
      <ErrorBox error={list.error} />
      {students === undefined && !list.error && <Skeleton rows={5} label="Loading students…" />}
      {students && students.length === 0 && (
        <EmptyState icon="schools" action={!filter.q && !filter.class && may.manage ? <Link to="/schools/import">Import students</Link> : undefined}>
          {filter.q || filter.class ? "No students match." : "No students yet. Import them from a spreadsheet, or add them one by one."}
        </EmptyState>
      )}
      {students && students.length > 0 && (
        <>
          <p className="hint" data-testid="students-total">
            {list.data?.total === 1 ? "1 student" : `${list.data?.total ?? 0} students`}
          </p>
          <table className="cards">
            <thead>
              <tr>
                <th scope="col">Admission number</th>
                <th scope="col">Name</th>
                <th scope="col">Class</th>
                <th scope="col">Status</th>
              </tr>
            </thead>
            <tbody>
              {students.map((s) => (
                <tr key={s.id} data-testid="student">
                  <td className="primary" data-label="Admission number">
                    <Link to={`/schools/students/${s.id}`}>{s.admission_number}</Link>
                  </td>
                  <td data-label="Name">{s.name}</td>
                  <td data-label="Class">{s.class}</td>
                  <td data-label="Status">
                    <StatusPill status={s.status} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </>
  );
}

function GuardianFields({ value, onChange, optional }: { value: StudentForm["guardian"]; onChange: (g: StudentForm["guardian"]) => void; optional?: boolean }) {
  const opt = optional ? " (optional)" : "";
  return (
    <>
      <Field label={`Guardian's name${opt}`}>
        <input value={value.name} autoComplete="off" onChange={(e) => onChange({ ...value, name: e.target.value })} />
      </Field>
      <Field label={`Guardian's phone${opt}`} hint="Families are known by their guardians' phones: a phone is in one family.">
        <input value={value.phone} type="tel" autoComplete="off" onChange={(e) => onChange({ ...value, phone: e.target.value })} placeholder="08031234567" />
      </Field>
      <Field label="Guardian's email (optional)">
        <input value={value.email} type="email" autoComplete="off" onChange={(e) => onChange({ ...value, email: e.target.value })} />
      </Field>
      <Field label="Relationship (optional)">
        <input value={value.relationship} autoComplete="off" onChange={(e) => onChange({ ...value, relationship: e.target.value })} placeholder="Mother, Father, Uncle" />
      </Field>
    </>
  );
}

/** Schools > Add a student (POST /v1/schools/students), with a new family when a guardian is given. */
export function SchoolStudentNew() {
  const { me, can } = useAuth();
  const nav = useNavigate();
  const classes = useClasses();
  const [form, setForm] = useState<StudentForm>(() => emptyStudentForm());
  const [tried, setTried] = useState(false);
  const act = useAction();
  if (!me?.schools) return <AreaOff title="Add a student" />;
  if (!schoolAccess(can).manage)
    return (
      <>
        <PageHeader title="Add a student" />
        <p className="hint">You can see students, but not add them. Someone who manages the school can.</p>
      </>
    );
  const problem = studentProblem(form);
  const list = classes.data?.classes ?? [];
  return (
    <>
      <PageHeader title="Add a student" description="One student at a time. While a term runs, they are enrolled in it at once." actions={<Link to="/schools">All students</Link>} />
      <ErrorBox error={classes.error} />
      {classes.data && list.length === 0 && (
        <EmptyState icon="schools" action={<Link to="/schools/classes">Add classes</Link>}>
          Add the school's classes first: every student is in one.
        </EmptyState>
      )}
      {list.length > 0 && (
        <form
          className="card"
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            setTried(true);
            if (problem) return;
            void act.run(async () => {
              const d = await plainly(post<SchoolStudentDetail>("/v1/schools/students", studentRequest(form)));
              nav(`/schools/students/${d.id}`);
            }, "Student added");
          }}
        >
          <Field label="Name">
            <input value={form.name} autoComplete="off" onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </Field>
          <Field label="Admission number" hint="Unique in the school, such as GCA/2026/014.">
            <input value={form.admission_number} autoComplete="off" spellCheck={false} onChange={(e) => setForm({ ...form, admission_number: e.target.value })} />
          </Field>
          <Field label="Class">
            <select value={form.class_id} onChange={(e) => setForm({ ...form, class_id: e.target.value })}>
              <option value="">Choose a class</option>
              {list.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.label}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Date of birth (optional)">
            <input type="date" value={form.date_of_birth} onChange={(e) => setForm({ ...form, date_of_birth: e.target.value })} />
          </Field>
          <fieldset className="sf-choices">
            <legend>Their family</legend>
            <p className="hint">A guardian makes a new family. A brother or sister of a student already here: add them from the Families page instead.</p>
            <GuardianFields optional value={form.guardian} onChange={(guardian) => setForm({ ...form, guardian })} />
          </fieldset>
          {tried && problem && (
            <div className="error" role="alert">
              {problem}
            </div>
          )}
          <ErrorBox error={act.error} />
          <div className="toolbar">
            <button type="submit" className="primary" disabled={act.busy}>
              Add student
            </button>
          </div>
        </form>
      )}
    </>
  );
}

/** Schools > Import students (POST /v1/schools/students/import): checked whole, shown, then saved. */
export function SchoolImport() {
  const { me, can } = useAuth();
  const [text, setText] = useState<string | null>(null);
  const [fileKey, setFileKey] = useState(0);
  const [plan, setPlan] = useState<ImportPlan | null>(null);
  const [refusal, setRefusal] = useState<ImportRefusal | null>(null);
  const [said, setSaid] = useState("");
  const act = useAction();
  if (!me?.schools) return <AreaOff title="Import students" />;
  if (!schoolAccess(can).manage)
    return (
      <>
        <PageHeader title="Import students" />
        <p className="hint">You can see students, but not import them. Someone who manages the school can.</p>
      </>
    );
  const send = (confirm?: string) =>
    act.run(async () => {
      setRefusal(null);
      try {
        const r = await plainly(post<ImportPlan>("/v1/schools/students/import", confirm ? { csv: text, confirm } : { csv: text }));
        if (r.saved) {
          setSaid(savedLine(r));
          setPlan(null);
          setText(null);
          setFileKey((k) => k + 1);
        } else {
          setPlan(r);
        }
      } catch (e) {
        const why = e instanceof ApiError ? importRefusal(e.status, e.body, e.message) : null;
        if (!why) throw e;
        setRefusal(why);
        setPlan(null);
      }
    }, false);
  return (
    <>
      <PageHeader
        title="Import students"
        description="A spreadsheet of students and their guardians, saved as a CSV. Every line is checked first: a file with any line that cannot be taken saves nothing. Uploading the same students again updates them by admission number and never adds them twice."
        actions={<Link to="/schools">All students</Link>}
      />
      <SchoolTabs />
      <div role="status" data-testid="import-status">
        {said && <div className="notice">{said}</div>}
      </div>
      <form
        className="card"
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          if (text) void send();
        }}
      >
        <Field
          label="Students file (CSV)"
          hint="Its first line names the columns: name, admission number, class, guardian name, phone, and optionally email, relationship and date of birth. A student with two guardians is on two lines. Classes are written as on the Classes page (JSS 1 A)."
        >
          <input
            key={fileKey}
            type="file"
            accept=".csv,text/csv"
            onChange={(e) => {
              const f = e.target.files?.[0];
              setPlan(null);
              setRefusal(null);
              setSaid("");
              if (f) void f.text().then(setText);
              else setText(null);
            }}
          />
        </Field>
        {refusal && (
          <div className="error" role="alert" data-testid="import-refusal">
            {refusal.message}
            {refusal.problems.length > 0 && (
              <ul aria-label="Lines that cannot be imported">
                {refusal.problems.map((p) => (
                  <li key={p.row}>
                    Line {p.row}: {p.why}
                  </li>
                ))}
              </ul>
            )}
            {refusal.total > refusal.problems.length && <div>And {refusal.total - refusal.problems.length} more.</div>}
          </div>
        )}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className={plan ? "" : "primary"} disabled={act.busy || !text}>
            Check the file
          </button>
        </div>
      </form>
      {plan && (
        <section className="card" aria-labelledby="import-plan" data-testid="import-plan">
          <h2 id="import-plan">Check before saving</h2>
          <p>{planLine(plan)}</p>
          <h3>Families, by guardians' phones</h3>
          <ul className="plain-list">
            {plan.families.map((f) => (
              <li key={f.index} data-testid="import-family">
                <strong>{f.existing ? "A family already here" : "A new family"}</strong>: {f.guardians.map(guardianLine).join("; ")}. Students: {f.students.join(", ")}.
              </li>
            ))}
          </ul>
          <h3>Students</h3>
          <table className="cards">
            <thead>
              <tr>
                <th scope="col">Line</th>
                <th scope="col">Admission number</th>
                <th scope="col">Name</th>
                <th scope="col">Class</th>
                <th scope="col">What happens</th>
              </tr>
            </thead>
            <tbody>
              {plan.students.map((s) => (
                <tr key={s.admission_number} data-testid="import-student">
                  <td data-label="Line">{s.row}</td>
                  <td className="primary" data-label="Admission number">
                    {s.admission_number}
                  </td>
                  <td data-label="Name">{s.name}</td>
                  <td data-label="Class">{s.class}</td>
                  <td data-label="What happens">{s.action === "add" ? "Added" : "Updated"}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <div className="toolbar">
            <button type="button" className="primary" disabled={act.busy} onClick={() => void send(plan.hash)}>
              Save {plan.students.length === 1 ? "1 student" : `${plan.students.length} students`}
            </button>
            <button type="button" disabled={act.busy} onClick={() => setPlan(null)}>
              Not now
            </button>
          </div>
        </section>
      )}
    </>
  );
}

/** A student's page (GET /v1/schools/students/{id}): details, family, class history; class and status changes. */
export function SchoolStudentPage() {
  const { id = "" } = useParams();
  const { me, can } = useAuth();
  const may = schoolAccess(can);
  const [reveal, setReveal] = useState(false);
  const one = useLoad(() => get<{ student: SchoolStudentDetail; revealed: boolean }>(studentPath(id, reveal)), [id, reveal]);
  const classes = useClasses();
  const act = useAction();
  const [said, setSaid] = useState("");
  if (!me?.schools) return <AreaOff title="Student" />;
  const s = one.data?.student;
  const change = (body: Record<string, unknown>, line: string) =>
    act.run(async () => {
      await plainly(patch(`/v1/schools/students/${encodeURIComponent(s?.id ?? id)}`, body));
      setSaid(line);
      one.reload();
    }, false);
  return (
    <>
      <PageHeader title={s ? `${s.admission_number}${s.name ? `: ${s.name}` : ""}` : "Student"} actions={<Link to="/schools">All students</Link>} />
      <ErrorBox error={one.error} />
      {!s && !one.error && <Skeleton rows={4} label="Loading the student…" />}
      {s && (
        <>
          <div role="status">{said && <div className="notice">{said}</div>}</div>
          <section className="card" aria-labelledby="student-details">
            <h2 id="student-details">Details</h2>
            <dl className="kv">
              <dt>Name</dt>
              <dd data-testid="student-name">{s.name}</dd>
              <dt>Admission number</dt>
              <dd>{s.admission_number}</dd>
              <dt>Class</dt>
              <dd>{s.class}</dd>
              <dt>Status</dt>
              <dd>
                <StatusPill status={s.status} />
              </dd>
              {s.date_of_birth && (
                <>
                  <dt>Date of birth</dt>
                  <dd>{dateLabel(s.date_of_birth)}</dd>
                </>
              )}
            </dl>
            {!one.data?.revealed && may.reveal && (
              <button type="button" onClick={() => setReveal(true)}>
                Show personal details
              </button>
            )}
            {!one.data?.revealed && !may.reveal && <p className="hint">Names and dates of birth are hidden. Someone who may see personal data can show them.</p>}
            {may.manage && (
              <div className="toolbar" style={{ marginTop: 16 }}>
                <label>
                  Move to{" "}
                  <select
                    value={s.class_id}
                    disabled={act.busy}
                    onChange={(e) => {
                      const c = classes.data?.classes.find((x) => x.id === e.target.value);
                      if (c) void change({ class_id: c.id }, `Moved to ${c.label}.`);
                    }}
                  >
                    {(classes.data?.classes ?? []).map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.label}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  Status{" "}
                  <select value={s.status} disabled={act.busy} onChange={(e) => void change({ status: e.target.value }, `Now ${studentStatus(e.target.value).label.toLowerCase()}.`)}>
                    <option value="active">Active</option>
                    <option value="withdrawn">Withdrawn</option>
                    <option value="graduated">Graduated</option>
                  </select>
                </label>
              </div>
            )}
            <ErrorBox error={act.error} />
          </section>
          <section className="card" aria-labelledby="student-family">
            <h2 id="student-family">Family</h2>
            {s.family ? <FamilyCard family={s.family} /> : <p className="hint">No family yet: add one on the Families page.</p>}
          </section>
          <StudentAccountPanel studentID={s.id} />
          <StudentFeesPanel studentID={s.id} />
          <StudentReceiptsPanel studentID={s.id} />
          <section className="card" aria-labelledby="student-history">
            <h2 id="student-history">Class history</h2>
            {s.enrollments.length === 0 ? (
              <p className="hint">Not enrolled in any term yet: starting a term enrols every active student.</p>
            ) : (
              <table className="cards">
                <thead>
                  <tr>
                    <th scope="col">Term</th>
                    <th scope="col">Class</th>
                    <th scope="col">Since</th>
                  </tr>
                </thead>
                <tbody>
                  {s.enrollments.map((e) => (
                    <tr key={e.id}>
                      <td className="primary" data-label="Term">
                        {e.term} {e.session}
                        {e.term_state === "current" && " (current)"}
                      </td>
                      <td data-label="Class">{e.class}</td>
                      <td data-label="Since">{fmtTime(e.created_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>
        </>
      )}
    </>
  );
}

function FamilyCard({ family, top }: { family: SchoolFamily; top?: boolean }): ReactNode {
  const Heading = top ? "h2" : "h3";
  return (
    <div data-testid="family">
      <Heading style={{ marginTop: 0 }}>{familyName(family)}</Heading>
      <p className="hint" style={{ marginTop: 0 }}>
        Guardians
      </p>
      <ul className="plain-list">
        {family.guardians.map((g) => (
          <li key={g.id}>{guardianLine(g)}</li>
        ))}
        {family.guardians.length === 0 && <li className="hint">No guardian yet.</li>}
      </ul>
      <p className="hint">Students</p>
      <ul className="plain-list">
        {family.students.map((st) => (
          <li key={st.id}>
            <Link to={`/schools/students/${st.id}`}>{st.admission_number}</Link> {st.name}, {st.class}
            {st.status !== "active" && ` (${studentStatus(st.status).label.toLowerCase()})`}
          </li>
        ))}
        {family.students.length === 0 && <li className="hint">No student yet.</li>}
      </ul>
    </div>
  );
}

// --- families ---

/** Schools > Families (GET /v1/schools/families): the guardians of each family and their students. */
export function SchoolFamilies() {
  const { me, can } = useAuth();
  const may = schoolAccess(can);
  const [reveal, setReveal] = useState(false);
  const list = useLoad(() => get<{ families: SchoolFamily[]; revealed: boolean }>(`/v1/schools/families${reveal ? "?reveal=true" : ""}`), [reveal]);
  if (!me?.schools) return <AreaOff title="Families" />;
  const families = list.data?.families;
  return (
    <>
      <PageHeader title="Families" description="The guardians of one or more students, who pay together. A guardian, known by their phone, is in one family." />
      <SchoolTabs />
      {may.reveal && (
        <div className="filters">
          <label className="row">
            <input type="checkbox" checked={reveal} onChange={(e) => setReveal(e.target.checked)} /> Show names and phones
          </label>
        </div>
      )}
      <ErrorBox error={list.error} />
      {families === undefined && !list.error && <Skeleton rows={4} label="Loading families…" />}
      {families && families.length === 0 && <EmptyState icon="members">No families yet. Importing students groups them into families by their guardians' phones.</EmptyState>}
      {families?.map((f) => (
        <section className="card" key={f.id}>
          <FamilyCard family={f} top />
        </section>
      ))}
    </>
  );
}

// --- classes, sessions and terms ---

/** Schools > Classes and terms: the classes in their order; sessions and their terms; starting a term; promotion. */
export function SchoolClasses() {
  const { me, can } = useAuth();
  const may = schoolAccess(can);
  const classes = useClasses();
  const sessions = useLoad(() => get<{ sessions: SchoolSession[] }>("/v1/schools/sessions"), []);
  if (!me?.schools) return <AreaOff title="Classes and terms" />;
  const list = classes.data?.classes;
  const current = currentTerm(sessions.data?.sessions);
  return (
    <>
      <PageHeader title="Classes and terms" description="The school's classes in the order students go up through them, and its sessions and terms. Starting a term enrols every active student in their class." />
      <SchoolTabs />
      <section className="card" aria-labelledby="classes-heading">
        <h2 id="classes-heading">Classes</h2>
        <ErrorBox error={classes.error} />
        {list === undefined && !classes.error && <Skeleton rows={3} label="Loading classes…" />}
        {list && list.length === 0 && <p className="hint">No classes yet. Add them in order: JSS 1 first, then JSS 2 and up.</p>}
        {list && list.length > 0 && (
          <table className="cards">
            <thead>
              <tr>
                <th scope="col">Class</th>
                <th scope="col">Order</th>
                <th scope="col">Active students</th>
              </tr>
            </thead>
            <tbody>
              {list.map((c) => (
                <tr key={c.id} data-testid="class">
                  <td className="primary" data-label="Class">
                    <Link to={`/schools?class=${c.id}`}>{c.label}</Link>
                  </td>
                  <td data-label="Order">{c.order}</td>
                  <td data-label="Active students">{c.students}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {may.manage && list && <AddClass next={nextOrder(list)} onAdded={classes.reload} />}
      </section>
      <section className="card" aria-labelledby="sessions-heading">
        <h2 id="sessions-heading">Sessions and terms</h2>
        {current ? (
          <p data-testid="current-term">
            Current term: <strong>{current.name}</strong> {current.session}, {dateLabel(current.starts_on)} to {dateLabel(current.ends_on)}; fees due {dateLabel(current.fees_due_on)}.
          </p>
        ) : (
          <p className="hint" data-testid="current-term">
            No term is running. Add a session and its terms, then start the first.
          </p>
        )}
        <ErrorBox error={sessions.error} />
        {sessions.data?.sessions.map((s) => (
          <SessionBlock key={s.id} session={s} manage={may.manage} onChange={sessions.reload} onStarted={classes.reload} />
        ))}
        {may.manage && sessions.data && <AddSession suggested={nextSessionLabel(sessions.data.sessions)} onAdded={sessions.reload} />}
      </section>
    </>
  );
}

function AddClass({ next, onAdded }: { next: number; onAdded: () => void }) {
  const [form, setForm] = useState<ClassForm>({ name: "", arm: "", order: String(next) });
  const [tried, setTried] = useState(false);
  const act = useAction();
  const problem = classProblem(form);
  return (
    <form
      noValidate
      aria-label="Add a class"
      onSubmit={(e) => {
        e.preventDefault();
        setTried(true);
        if (problem) return;
        void act.run(async () => {
          await plainly(post("/v1/schools/classes", classRequest(form)));
          setForm({ name: "", arm: form.arm, order: String(Number(form.order) + (form.arm ? 0 : 1)) });
          setTried(false);
          onAdded();
        }, "Class added");
      }}
    >
      <h3>Add a class</h3>
      <div className="row" style={{ flexWrap: "wrap" }}>
        <Field label="Name">
          <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="JSS 1" />
        </Field>
        <Field label="Arm (optional)">
          <input value={form.arm} onChange={(e) => setForm({ ...form, arm: e.target.value })} placeholder="A" />
        </Field>
        <Field label="Order" hint="Arms of one class share its order.">
          <input value={form.order} inputMode="numeric" onChange={(e) => setForm({ ...form, order: e.target.value })} />
        </Field>
      </div>
      {tried && problem && (
        <div className="error" role="alert">
          {problem}
        </div>
      )}
      <ErrorBox error={act.error} />
      <button type="submit" disabled={act.busy}>
        Add class
      </button>
    </form>
  );
}

function AddSession({ suggested, onAdded }: { suggested: string; onAdded: () => void }) {
  const [label, setLabel] = useState(suggested);
  const [tried, setTried] = useState(false);
  const act = useAction();
  const problem = sessionProblem(label);
  return (
    <form
      noValidate
      aria-label="Add a session"
      onSubmit={(e) => {
        e.preventDefault();
        setTried(true);
        if (problem) return;
        void act.run(async () => {
          await plainly(post("/v1/schools/sessions", { label: label.trim() }));
          setTried(false);
          onAdded();
        }, "Session added");
      }}
    >
      <h3>Add a session</h3>
      <Field label="Session" hint="The school year, such as 2026/2027.">
        <input value={label} onChange={(e) => setLabel(e.target.value)} />
      </Field>
      {tried && problem && (
        <div className="error" role="alert">
          {problem}
        </div>
      )}
      <ErrorBox error={act.error} />
      <button type="submit" disabled={act.busy}>
        Add session
      </button>
    </form>
  );
}

function SessionBlock({ session, manage, onChange, onStarted }: { session: SchoolSession; manage: boolean; onChange: () => void; onStarted: () => void }) {
  const act = useAction();
  const [said, setSaid] = useState("");
  const [confirm, setConfirm] = useState<string | null>(null);
  return (
    <div className="card" data-testid="session">
      <h3 style={{ marginTop: 0 }}>Session {session.label}</h3>
      <div role="status">{said && <div className="notice">{said}</div>}</div>
      {session.terms.length === 0 && <p className="hint">No terms yet.</p>}
      {session.terms.length > 0 && (
        <table className="cards">
          <thead>
            <tr>
              <th scope="col">Term</th>
              <th scope="col">Dates</th>
              <th scope="col">Fees due</th>
              <th scope="col">State</th>
              <th scope="col">Enrolled</th>
              <th scope="col">Fees</th>
              {manage && <th scope="col">Start</th>}
            </tr>
          </thead>
          <tbody>
            {session.terms.map((t: SchoolTerm) => (
              <tr key={t.id} data-testid="term">
                <td className="primary" data-label="Term">
                  {t.name}
                </td>
                <td data-label="Dates">
                  {dateLabel(t.starts_on)} to {dateLabel(t.ends_on)}
                </td>
                <td data-label="Fees due">{dateLabel(t.fees_due_on)}</td>
                <td data-label="State">{termStateWords(t.state)}</td>
                <td data-label="Enrolled">{t.enrolled}</td>
                <td data-label="Fees">
                  <span className="row" style={{ flexWrap: "wrap" }}>
                    <Link to={`/schools/terms/${t.id}/fees`} aria-label={`Fees for ${t.name} ${session.label}`}>
                      Fees
                    </Link>
                    {t.state !== "planned" && (
                      <Link to={`/schools/terms/${t.id}/invoices`} aria-label={`Invoices for ${t.name} ${session.label}`}>
                        Invoices
                      </Link>
                    )}
                  </span>
                </td>
                {manage && (
                  <td data-label="Start">
                    {t.state === "planned" &&
                      (confirm === t.id ? (
                        <span className="row" style={{ flexWrap: "wrap" }}>
                          <button
                            type="button"
                            className="primary"
                            disabled={act.busy}
                            onClick={() =>
                              void act.run(async () => {
                                const r = await plainly(post<{ term: SchoolTerm; enrolled: number }>(`/v1/schools/terms/${t.id}/start`));
                                setSaid(startedLine(r.term.name, r.term.session, r.enrolled));
                                setConfirm(null);
                                onChange();
                                onStarted();
                              }, false)
                            }
                          >
                            Start {t.name} now
                          </button>
                          <button type="button" onClick={() => setConfirm(null)}>
                            Cancel
                          </button>
                        </span>
                      ) : (
                        <button type="button" onClick={() => setConfirm(t.id)} aria-label={`Start ${t.name} ${session.label}`}>
                          Start…
                        </button>
                      ))}
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {confirm && <p className="hint">Starting a term makes it the current one and enrols every active student in their class. The term before becomes past and read-only.</p>}
      <ErrorBox error={act.error} />
      {manage && <AddTerm session={session} onAdded={onChange} />}
      <p>
        {session.promoted_at ? (
          <span className="hint">Promoted {fmtTime(session.promoted_at)}.</span>
        ) : (
          <Link className="button" to={`/schools/sessions/${session.id}/promotion`}>
            Promotion at the end of {session.label}
          </Link>
        )}
      </p>
    </div>
  );
}

function AddTerm({ session, onAdded }: { session: SchoolSession; onAdded: () => void }) {
  const [form, setForm] = useState<TermForm>({ name: nextTermName(session.terms), starts_on: "", ends_on: "", fees_due_on: "" });
  const [tried, setTried] = useState(false);
  const act = useAction();
  const problem = termProblem(form);
  if (session.terms.length >= 3 && !form.name && !tried) return null;
  return (
    <form
      noValidate
      aria-label={`Add a term to ${session.label}`}
      onSubmit={(e) => {
        e.preventDefault();
        setTried(true);
        if (problem) return;
        void act.run(async () => {
          await plainly(post(`/v1/schools/sessions/${session.id}/terms`, form));
          setTried(false);
          setForm({ name: "", starts_on: "", ends_on: "", fees_due_on: "" });
          onAdded();
        }, "Term added");
      }}
    >
      <h4>Add a term</h4>
      <div className="row" style={{ flexWrap: "wrap" }}>
        <Field label="Term">
          <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="First term" />
        </Field>
        <Field label="Starts">
          <input type="date" value={form.starts_on} onChange={(e) => setForm({ ...form, starts_on: e.target.value })} />
        </Field>
        <Field label="Ends">
          <input type="date" value={form.ends_on} onChange={(e) => setForm({ ...form, ends_on: e.target.value })} />
        </Field>
        <Field label="Fees due">
          <input type="date" value={form.fees_due_on} onChange={(e) => setForm({ ...form, fees_due_on: e.target.value })} />
        </Field>
      </div>
      {tried && problem && (
        <div className="error" role="alert">
          {problem}
        </div>
      )}
      <ErrorBox error={act.error} />
      <button type="submit" disabled={act.busy}>
        Add term
      </button>
    </form>
  );
}

/** Promotion at the end of a session (GET, then POST /v1/schools/sessions/{id}/promotion): everyone up a class, with exceptions, once. */
export function SchoolPromotionPage() {
  const { id = "" } = useParams();
  const { me, can } = useAuth();
  const may = schoolAccess(can);
  const plan = useLoad(() => get<SchoolPromotion>(`/v1/schools/sessions/${encodeURIComponent(id)}/promotion`), [id]);
  const [exceptions, setExceptions] = useState<Record<string, PromotionAction>>({});
  const [confirm, setConfirm] = useState(false);
  const [said, setSaid] = useState("");
  const act = useAction();
  if (!me?.schools) return <AreaOff title="Promotion" />;
  const p = plan.data;
  return (
    <>
      <PageHeader
        title={p ? `Promotion at the end of ${p.session}` : "Promotion"}
        description="Every active student moves up one class in one step; a student in the last class graduates. Choose the exceptions first: a student who repeats, leaves, or graduates early. A session is promoted once."
        actions={<Link to="/schools/classes">Classes and terms</Link>}
      />
      <ErrorBox error={plan.error} />
      {!p && !plan.error && <Skeleton rows={4} label="Loading the promotion…" />}
      <div role="status" data-testid="promotion-status">
        {said && <div className="notice">{said}</div>}
      </div>
      {p?.done && !said && <div className="notice">{p.session} was promoted {fmtTime(p.promoted_at)}: {promotionLine(p.counts)}</div>}
      {p && !p.done && (
        <>
          <section className="card" aria-labelledby="promotion-classes">
            <h2 id="promotion-classes">Classes</h2>
            <ul className="plain-list">
              {p.classes.map((c) => (
                <li key={c.class_id}>
                  {c.class} → {c.next || "graduate"} ({c.students === 1 ? "1 student" : `${c.students} students`})
                </li>
              ))}
            </ul>
          </section>
          <section className="card" aria-labelledby="promotion-students">
            <h2 id="promotion-students">Students</h2>
            <p data-testid="promotion-counts">{promotionLine(promotionCounts(p, exceptions))}</p>
            <table className="cards">
              <thead>
                <tr>
                  <th scope="col">Admission number</th>
                  <th scope="col">Name</th>
                  <th scope="col">Class now</th>
                  <th scope="col">At the end of the session</th>
                </tr>
              </thead>
              <tbody>
                {p.students.map((s) => (
                  <tr key={s.id}>
                    <td className="primary" data-label="Admission number">
                      {s.admission_number}
                    </td>
                    <td data-label="Name">{s.name}</td>
                    <td data-label="Class now">{s.class}</td>
                    <td data-label="At the end of the session">
                      {may.manage ? (
                        <select
                          aria-label={`What happens to ${s.admission_number}`}
                          value={exceptions[s.id] ?? s.action}
                          onChange={(e) => setExceptions({ ...exceptions, [s.id]: e.target.value as PromotionAction })}
                        >
                          {PROMOTION_ACTIONS.filter((a) => a.value !== "promote" || s.next_id).map((a) => (
                            <option key={a.value} value={a.value}>
                              {a.value === "promote" ? `Move up to ${s.next}` : a.label}
                            </option>
                          ))}
                        </select>
                      ) : (
                        PROMOTION_ACTIONS.find((a) => a.value === s.action)?.label
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <ErrorBox error={act.error} />
            {may.manage && (
              <div className="toolbar">
                {confirm ? (
                  <>
                    <button
                      type="button"
                      className="primary"
                      disabled={act.busy}
                      onClick={() =>
                        void act.run(async () => {
                          const r = await plainly(post<SchoolPromotion>(`/v1/schools/sessions/${encodeURIComponent(id)}/promotion`, promotionRequest(p, exceptions)));
                          setSaid(`${r.session} promoted: ${promotionLine(r.counts)}`);
                          setConfirm(false);
                          plan.reload();
                        }, false)
                      }
                    >
                      Promote {p.session} now
                    </button>
                    <button type="button" onClick={() => setConfirm(false)}>
                      Cancel
                    </button>
                    <span className="hint">This happens once and cannot be undone here.</span>
                  </>
                ) : (
                  <button type="button" className="primary" disabled={p.students.length === 0} onClick={() => setConfirm(true)}>
                    Promote…
                  </button>
                )}
              </div>
            )}
          </section>
        </>
      )}
    </>
  );
}

// --- the switch ---

/** Settings > Schools (PUT /v1/settings/schools): only an owner turns the area on or off. */
export function SchoolsSwitch() {
  const { me, refresh } = useAuth();
  const settings = useLoad(() => get<SchoolSettings>("/v1/settings/schools"), []);
  const [bursar, setBursar] = useState(true);
  const act = useAction();
  const owner = !!me?.roles.includes("owner");
  const on = settings.data?.enabled ?? !!me?.schools;
  return (
    <section className="card" aria-labelledby="schools-switch">
      <h2 id="schools-switch" style={{ marginTop: 0 }}>
        Schools
      </h2>
      <p className="hint">
        For a school: its students, families, classes and terms, beside Invoices and Money. People who may see the school's record find it in the menu. Turning it off hides it; nothing is deleted.
      </p>
      <ErrorBox error={settings.error} />
      {owner ? (
        <>
          <label className="row">
            <input
              type="checkbox"
              checked={on}
              disabled={act.busy || !settings.data}
              onChange={(e) => {
                const enabled = e.target.checked;
                void act.run(async () => {
                  await put("/v1/settings/schools", { enabled, bursar_role: enabled && bursar && !settings.data?.bursar_role });
                  settings.reload();
                  await refresh();
                }, enabled ? "Schools turned on" : "Schools turned off");
              }}
            />{" "}
            Turn on Schools
          </label>
          {!on && !settings.data?.bursar_role && (
            <label className="row">
              <input type="checkbox" checked={bursar} onChange={(e) => setBursar(e.target.checked)} /> Also add a Bursar role (students, families, classes and invoices), to give whoever runs the fees office
            </label>
          )}
          {settings.data?.bursar_role && <p className="hint">The Bursar role is ready: give it to people under Members &amp; keys.</p>}
        </>
      ) : (
        <p data-testid="schools-state">{on ? "Schools is on." : "Schools is off."} Only an owner turns it on or off.</p>
      )}
      <ErrorBox error={act.error} />
    </section>
  );
}

export function SchoolSettingsPage() {
  const { me } = useAuth();
  return (
    <>
      <PageHeader title="Schools settings" description="Turn the Schools area on or off for the business, choose who issues students' account numbers, and set the discount threshold." />
      {me?.schools && <SchoolTabs />}
      <SchoolsSwitch />
      {me?.schools && me.roles.includes("owner") && <IssuingSettings />}
      {me?.schools && me.roles.includes("owner") && <DiscountThreshold />}
      {me?.schools && me.roles.includes("owner") && <CashThreshold />}
    </>
  );
}

/** The owner's cash threshold, in naira: postings at or above it wait for a second person; 0 makes every posting wait; none leaves postings to the bursar. */
function CashThreshold() {
  const settings = useLoad(() => get<SchoolSettings>("/v1/settings/schools"), []);
  const [mode, setMode] = useState<"none" | "amount">();
  const [value, setValue] = useState<string>();
  const act = useAction();
  const current = settings.data?.cash_threshold_naira;
  const shownMode = mode ?? (current === null || current === undefined ? "none" : "amount");
  const shown = value ?? String(current ?? "");
  const n = Number(shown.replace(/[,₦\s]/g, "") || "0");
  const bad = shownMode === "amount" && (!Number.isInteger(n) || n < 0 || shown.trim() === "");
  return (
    <section className="card" aria-labelledby="cash-threshold">
      <h2 id="cash-threshold" style={{ marginTop: 0 }}>
        Cash threshold
      </h2>
      <p className="hint">
        A cash or transfer posting at or above this amount waits for a second person (someone who may approve for the school, never the person who posted it) before it reaches a child's
        fees. 0 makes every posting wait.
      </p>
      <ErrorBox error={settings.error} />
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (bad || !settings.data) return;
          void act.run(async () => {
            await put("/v1/settings/schools", { enabled: settings.data?.enabled ?? true, cash_threshold_naira: shownMode === "none" ? null : n });
            setMode(undefined);
            setValue(undefined);
            settings.reload();
          }, "Cash threshold saved");
        }}
      >
        <fieldset style={{ border: 0, padding: 0, margin: 0 }}>
          <legend className="sr-only">When a posting needs a second person</legend>
          <label className="row">
            <input type="radio" name="cash-threshold-mode" checked={shownMode === "none"} onChange={() => setMode("none")} /> No second person needed for cash postings
          </label>
          <label className="row">
            <input type="radio" name="cash-threshold-mode" checked={shownMode === "amount"} onChange={() => setMode("amount")} /> A second person at or above an amount
          </label>
        </fieldset>
        {shownMode === "amount" && (
          <Field label="Cash threshold (₦)" hint="0: every posting waits">
            <input inputMode="numeric" value={shown} onChange={(e) => setValue(e.target.value)} placeholder="50,000" />
          </Field>
        )}
        {bad && (
          <div className="error" role="alert">
            The threshold is a whole number of naira.
          </div>
        )}
        <button type="submit" disabled={act.busy || bad || !settings.data}>
          Save the cash threshold
        </button>
      </form>
      <ErrorBox error={act.error} />
    </section>
  );
}

/** The owner's discount threshold, in naira: discounts at or above it need a second person (refused until approvals arrive). */
function DiscountThreshold() {
  const settings = useLoad(() => get<SchoolSettings>("/v1/settings/schools"), []);
  const [value, setValue] = useState<string>();
  const act = useAction();
  const shown = value ?? String(settings.data?.discount_threshold_naira ?? "");
  const n = Number(shown.replace(/[,₦\s]/g, "") || "0");
  const bad = !Number.isInteger(n) || n < 0;
  return (
    <section className="card" aria-labelledby="discount-threshold">
      <h2 id="discount-threshold" style={{ marginTop: 0 }}>
        Discount threshold
      </h2>
      <p className="hint">
        A discount at or above this amount waits for a second person (someone who may approve for the school, never the person who asked) before it reaches a child's fees. 0 or empty: no
        threshold.
      </p>
      <ErrorBox error={settings.error} />
      <form
        className="row"
        onSubmit={(e) => {
          e.preventDefault();
          if (bad || !settings.data) return;
          void act.run(async () => {
            await put("/v1/settings/schools", { enabled: settings.data?.enabled ?? true, discount_threshold_naira: n });
            setValue(undefined);
            settings.reload();
          }, "Discount threshold saved");
        }}
      >
        <Field label="Threshold (₦)">
          <input inputMode="numeric" value={shown} onChange={(e) => setValue(e.target.value)} placeholder="50,000" />
        </Field>
        <button type="submit" disabled={act.busy || bad || !settings.data}>
          Save the threshold
        </button>
      </form>
      {bad && (
        <div className="error" role="alert">
          The threshold is a whole number of naira.
        </div>
      )}
      <ErrorBox error={act.error} />
    </section>
  );
}

/** A gate session's home until the gate screen (B8): who they are, and signing out. */
export function GateHome() {
  const { me, logout } = useAuth();
  return (
    <div className="login card" style={{ maxWidth: 640 }}>
      <h1>The gate</h1>
      <p>
        You are signed in{me?.user?.name ? ` as ${me.user.name}` : ""} for the school gate. The gate screen, which answers Cleared or See the bursar for a student, is coming soon. Nothing else is open to the gate.
      </p>
      <p>
        <button onClick={() => void logout()}>Sign out</button>
      </p>
    </div>
  );
}
