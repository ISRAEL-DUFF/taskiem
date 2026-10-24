import { describe, expect, it } from "vitest";
import {
  classLabel,
  classProblem,
  classRequest,
  currentTerm,
  familyName,
  guardianLine,
  guardianProblem,
  importRefusal,
  isMasked,
  nextOrder,
  nextSessionLabel,
  nextTermName,
  phoneOK,
  planLine,
  promotionCounts,
  promotionLine,
  promotionRequest,
  savedLine,
  schoolAccess,
  sessionProblem,
  showSchools,
  startedLine,
  studentPath,
  studentProblem,
  studentRequest,
  studentsPath,
  studentStatus,
  termProblem,
  termStateWords,
  emptyStudentForm,
  type SchoolPromotion,
  type SchoolSession,
} from "./schools";

const can = (perms: string[]) => (p: string) => perms.includes(p);

describe("who sees what", () => {
  it("shows the area to people who may read it, only while it is on", () => {
    expect(showSchools(true, can(["school.read"]))).toBe(true);
    expect(showSchools(false, can(["school.read"]))).toBe(false);
    expect(showSchools(undefined, can(["school.read"]))).toBe(false);
    expect(showSchools(true, can(["school.gate"]))).toBe(false);
    expect(schoolAccess(can(["school.read", "school.manage", "pii.reveal"]))).toEqual({ see: true, manage: true, reveal: true });
  });
});

describe("words", () => {
  it("names classes, statuses, terms and families", () => {
    expect(classLabel(" JSS  1 ", "A")).toBe("JSS 1 A");
    expect(classLabel("Primary 4", "")).toBe("Primary 4");
    expect(studentStatus("withdrawn").label).toBe("Withdrawn");
    expect(studentStatus("odd").label).toBe("odd");
    expect(termStateWords("past")).toBe("Past (read-only)");
    expect(isMasked("E•••")).toBe(true);
    expect(isMasked("Ebube Eze")).toBe(false);
    expect(guardianLine({ name: "Ngozi Eze", phone: "+2348031234567", relationship: "Mother" })).toBe("Ngozi Eze (Mother) · +2348031234567");
    expect(familyName({ id: "f", guardians: [{ id: "g", name: "Ngozi Eze", phone: "x" }], students: [], created_at: "" })).toBe("Ngozi Eze's family");
    expect(familyName({ id: "f", guardians: [], students: [], created_at: "" })).toBe("A family with no guardian yet");
  });

  it("says what an import and a start did", () => {
    expect(planLine({ added: 3, updated: 0, families: [{ existing: false }, { existing: false }] as never })).toBe("This file will add 3 students, in 2 new families.");
    expect(planLine({ added: 0, updated: 1, families: [{ existing: true }] as never })).toBe("This file will update 1 student already here, in 1 family already here.");
    expect(savedLine({ added: 1, updated: 2 })).toBe("Saved: 1 student added, 2 updated.");
    expect(startedLine("First term", "2026/2027", 0)).toContain("enrolled already");
    expect(startedLine("First term", "2026/2027", 2)).toBe("First term 2026/2027 is the current term: 2 students enrolled.");
  });
});

describe("the import's refusal", () => {
  it("reads each line and why, and only for an import", () => {
    const r = importRefusal(422, { code: "import_unreadable", error: "2 lines cannot be imported; nothing was saved", problems: [{ row: 3, why: "the class is missing" }, { row: "x" }], total: 2 }, "x");
    expect(r).toEqual({ message: "2 lines cannot be imported; nothing was saved.", missing: [], problems: [{ row: 3, why: "the class is missing" }], total: 2 });
    expect(importRefusal(422, { code: "statement_unreadable" }, "x")).toBeNull();
    expect(importRefusal(400, { code: "import_unreadable" }, "x")).toBeNull();
    expect(importRefusal(422, { code: "import_unreadable", missing: ["phone", 3] }, "no column")?.missing).toEqual(["phone"]);
  });
});

describe("forms", () => {
  it("checks a class", () => {
    expect(classProblem({ name: "", arm: "", order: "1" })).toContain("name");
    expect(classProblem({ name: "JSS 1", arm: "", order: "x" })).toContain("order");
    expect(classProblem({ name: "JSS 1", arm: "A", order: "7" })).toBeNull();
    expect(classRequest({ name: " JSS 1 ", arm: " A", order: "7" })).toEqual({ name: "JSS 1", arm: "A", order: 7 });
    expect(nextOrder([])).toBe(1);
    expect(nextOrder([{ order: 7 }, { order: 3 }])).toBe(8);
  });

  it("checks sessions and terms", () => {
    expect(sessionProblem("2026/2027")).toBeNull();
    expect(sessionProblem("2026/2028")).toContain("follows");
    expect(sessionProblem("2026-27")).toContain("two years");
    expect(nextSessionLabel([{ label: "2026/2027" }])).toBe("2027/2028");
    expect(nextSessionLabel([], new Date("2026-09-01T12:00:00Z"))).toBe("2026/2027");
    expect(nextSessionLabel([], new Date("2026-03-01T12:00:00Z"))).toBe("2025/2026");
    expect(nextTermName([{ name: "First term" }])).toBe("Second term");
    const t = { name: "First term", starts_on: "2026-09-14", ends_on: "2026-12-11", fees_due_on: "2026-09-07" };
    expect(termProblem(t)).toBeNull();
    expect(termProblem({ ...t, ends_on: "2026-09-01" })).toContain("ends after");
    expect(termProblem({ ...t, fees_due_on: "2027-01-01" })).toContain("by the end");
    expect(termProblem({ ...t, starts_on: "" })).toContain("start, end");
  });

  it("checks a student and their guardian", () => {
    const f = { ...emptyStudentForm("c1"), name: "Ebube Eze", admission_number: "GCA/001" };
    expect(studentProblem(f)).toBeNull();
    expect(studentRequest(f)).toEqual({ name: "Ebube Eze", admission_number: "GCA/001", class_id: "c1" });
    expect(studentProblem({ ...f, admission_number: "GCA 001" })).toContain("letters, digits");
    expect(studentProblem({ ...f, class_id: "" })).toContain("class");
    expect(studentProblem({ ...f, guardian: { name: "Ngozi", phone: "" } })).toContain("phone");
    const withGuardian = { ...f, guardian: { name: "Ngozi Eze", phone: "0803 123 4567", email: "", relationship: "Mother" } };
    expect(studentProblem(withGuardian)).toBeNull();
    expect(studentRequest(withGuardian).guardians).toEqual([{ name: "Ngozi Eze", phone: "0803 123 4567", relationship: "Mother" }]);
    expect(guardianProblem({ name: "", phone: "" }, true)).toBeNull();
    expect(guardianProblem({ name: "A", phone: "08031234567", email: "nope" })).toContain("email");
    expect(phoneOK("+447700900123")).toBe(true);
    expect(phoneOK("0803")).toBe(false);
  });
});

describe("addresses", () => {
  it("builds the students list's and a student's", () => {
    expect(studentsPath({ class: "c1", status: "active", q: " musa ", reveal: true })).toBe("/v1/schools/students?class=c1&status=active&q=musa&reveal=true&limit=1000");
    expect(studentsPath({ class: "", status: "", q: "", reveal: false })).toBe("/v1/schools/students?limit=1000");
    expect(studentPath("a/b", true)).toBe("/v1/schools/students/a%2Fb?reveal=true");
  });
});

describe("promotion", () => {
  const p: Pick<SchoolPromotion, "students"> = {
    students: [
      { id: "a", admission_number: "1", name: "A", class: "JSS 1", action: "promote", next_id: "j2", next: "JSS 2" },
      { id: "b", admission_number: "2", name: "B", class: "JSS 1", action: "promote", next_id: "j2", next: "JSS 2" },
      { id: "c", admission_number: "3", name: "C", class: "SS 3", action: "graduate", next_id: null, next: "" },
    ],
  };
  it("counts with the exceptions chosen and sends only those that differ", () => {
    expect(promotionCounts(p, {})).toEqual({ promote: 2, repeat: 0, leave: 0, graduate: 1 });
    expect(promotionCounts(p, { a: "repeat", c: "promote" })).toEqual({ promote: 1, repeat: 1, leave: 0, graduate: 1 });
    expect(promotionRequest(p, { a: "repeat", b: "promote" })).toEqual({ exceptions: { a: "repeat" } });
    expect(promotionLine({ promote: 2, repeat: 1, leave: 0, graduate: 1 })).toBe("2 students move up, 1 repeats, 1 graduates.");
    expect(promotionLine({})).toBe("No active students to promote.");
  });
  it("finds the current term", () => {
    const sessions = [{ id: "s", label: "2026/2027", promoted_at: null, created_at: "", terms: [{ id: "t", state: "current", name: "First term" }] }] as unknown as SchoolSession[];
    expect(currentTerm(sessions)?.id).toBe("t");
    expect(currentTerm([])).toBeUndefined();
  });
});
