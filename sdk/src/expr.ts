// Expressions in code (spec 10.1): arrow functions are compiled to CEL when a
// workflow is built, and CEL is printed back as arrow functions when code is
// generated from a definition. Only a subset of JavaScript maps onto CEL;
// anything else is rejected with a pointer to a raw CEL string or a code
// step, never guessed at.

/** The variables an expression can read (wd/v1 rule 10). */
export const ROOTS = ["trigger", "steps", "run", "env", "secrets", "item", "index"] as const;
export type Root = (typeof ROOTS)[number];

export class ExpressionError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ExpressionError";
  }
}

// ---------------------------------------------------------------- syntax tree

type Node =
  | { k: "ident"; name: string }
  | { k: "lit"; v: string | number | boolean | null; double?: boolean; raw?: string | undefined }
  | { k: "member"; obj: Node; name: string }
  | { k: "index"; obj: Node; idx: Node }
  | { k: "call"; fn: string; args: Node[] }
  | { k: "mcall"; obj: Node; name: string; args: Node[] }
  | { k: "unary"; op: "!" | "-"; x: Node }
  | { k: "bin"; op: string; l: Node; r: Node }
  | { k: "cond"; c: Node; t: Node; f: Node }
  | { k: "list"; items: Node[] }
  | { k: "map"; entries: { key: Node; value: Node }[] }
  | { k: "arrow"; params: Param; body: Node }
  | { k: "template"; quasis: string[]; exprs: Node[] }
  | { k: "paren"; x: Node };

/** An arrow function's parameters: one name, or destructured roots. */
type Param = { kind: "name"; name: string } | { kind: "destructure"; names: Map<string, string> } | { kind: "none" };

// ------------------------------------------------------------------- lexing

type Tok = { t: "id" | "num" | "str" | "tpl" | "punct" | "eof"; v: string; pos: number; raw?: string };

const PUNCT = ["===", "!==", "...", "=>", "==", "!=", "<=", ">=", "&&", "||", "??", "?.", "(", ")", "[", "]", "{", "}", ".", ",", ":", ";", "?", "!", "<", ">", "+", "-", "*", "/", "%", "="];

function lex(src: string, lang: "js" | "cel"): Tok[] {
  const out: Tok[] = [];
  let i = 0;
  const at = (j: number) => src[j] ?? "";
  const isId = (c: string) => /[A-Za-z0-9_$]/.test(c);
  while (i < src.length) {
    const c = at(i);
    if (/\s/.test(c)) {
      i++;
      continue;
    }
    if (c === "/" && src[i + 1] === "/") {
      while (i < src.length && src[i] !== "\n") i++;
      continue;
    }
    if (c === "/" && src[i + 1] === "*") {
      const end = src.indexOf("*/", i + 2);
      i = end < 0 ? src.length : end + 2;
      continue;
    }
    const start = i;
    if (/[A-Za-z_$]/.test(c)) {
      while (i < src.length && isId(at(i))) i++;
      out.push({ t: "id", v: src.slice(start, i), pos: start });
      continue;
    }
    if (/[0-9]/.test(c) || (c === "." && /[0-9]/.test(src[i + 1] ?? ""))) {
      if (c === "0" && /[xX]/.test(src[i + 1] ?? "")) {
        i += 2;
        while (i < src.length && /[0-9a-fA-F_]/.test(at(i))) i++;
      } else {
        while (i < src.length && /[0-9_]/.test(at(i))) i++;
        if (src[i] === "." && /[0-9]/.test(src[i + 1] ?? "")) {
          i++;
          while (i < src.length && /[0-9_]/.test(at(i))) i++;
        } else if (src[i] === "." && lang === "cel" && !/[A-Za-z_]/.test(src[i + 1] ?? "")) {
          i++; // "1." is a CEL double
        }
        if (/[eE]/.test(src[i] ?? "")) {
          i++;
          if (/[+-]/.test(src[i] ?? "")) i++;
          while (i < src.length && /[0-9]/.test(at(i))) i++;
        }
      }
      if (lang === "cel" && /[uU]/.test(src[i] ?? "")) throw new ExpressionError("unsigned integers are not supported in code");
      if (src[i] === "n") throw new ExpressionError("BigInt literals are not supported in expressions");
      out.push({ t: "num", v: src.slice(start, i).replace(/_/g, ""), pos: start });
      continue;
    }
    if (c === "'" || c === '"') {
      if (lang === "cel" && src.startsWith(c.repeat(3), i)) throw new ExpressionError("triple-quoted CEL strings are not supported in code");
      i++;
      let s = "";
      while (i < src.length && src[i] !== c) {
        if (src[i] === "\\") {
          s += unescapeChar(src, i);
          i += escapeLength(src, i);
        } else {
          if (src[i] === "\n") throw new ExpressionError("unterminated string");
          s += src[i];
          i++;
        }
      }
      if (i >= src.length) throw new ExpressionError("unterminated string");
      i++;
      out.push({ t: "str", v: s, pos: start, raw: src.slice(start, i) });
      continue;
    }
    if (c === "`" && lang === "js") {
      // A template literal: kept raw, split later.
      i++;
      let depth = 0;
      while (i < src.length) {
        if (src[i] === "\\") {
          i += 2;
          continue;
        }
        if (depth === 0 && src[i] === "`") break;
        if (src[i] === "$" && src[i + 1] === "{") {
          depth++;
          i += 2;
          continue;
        }
        if (depth > 0 && src[i] === "}") depth--;
        i++;
      }
      if (i >= src.length) throw new ExpressionError("unterminated template literal");
      i++;
      out.push({ t: "tpl", v: src.slice(start + 1, i - 1), pos: start });
      continue;
    }
    if ((c === "r" || c === "R" || c === "b" || c === "B") && lang === "cel" && (src[i + 1] === "'" || src[i + 1] === '"')) {
      throw new ExpressionError("raw and bytes CEL strings are not supported in code");
    }
    const p = PUNCT.find((q) => src.startsWith(q, i));
    if (!p) throw new ExpressionError(`unexpected character ${JSON.stringify(c)}`);
    out.push({ t: "punct", v: p, pos: start });
    i += p.length;
  }
  out.push({ t: "eof", v: "", pos: src.length });
  return out;
}

function escapeLength(src: string, i: number): number {
  const n = src[i + 1];
  if (n === "u") return src[i + 2] === "{" ? src.indexOf("}", i) - i + 1 : 6;
  if (n === "x") return 4;
  return 2;
}

function unescapeChar(src: string, i: number): string {
  const n = src[i + 1];
  switch (n) {
    case "n":
      return "\n";
    case "r":
      return "\r";
    case "t":
      return "\t";
    case "b":
      return "\b";
    case "f":
      return "\f";
    case "v":
      return "\v";
    case "0":
      return "\0";
    case "u": {
      const hex = src[i + 2] === "{" ? src.slice(i + 3, src.indexOf("}", i)) : src.slice(i + 2, i + 6);
      return String.fromCodePoint(parseInt(hex, 16));
    }
    case "x":
      return String.fromCharCode(parseInt(src.slice(i + 2, i + 4), 16));
    default:
      return n ?? "";
  }
}

// ------------------------------------------------------------------ parsing

/** A Pratt parser shared by the JavaScript subset and the CEL subset. */
class Parser {
  i = 0;
  constructor(
    readonly toks: Tok[],
    readonly lang: "js" | "cel",
  ) {}

  peek(o = 0): Tok {
    return this.toks[Math.min(this.i + o, this.toks.length - 1)] ?? EOF;
  }
  next(): Tok {
    return this.toks[this.i++] ?? EOF;
  }
  is(v: string, o = 0): boolean {
    const t = this.peek(o);
    return (t.t === "punct" || t.t === "id") && t.v === v;
  }
  eat(v: string): Tok {
    const t = this.next();
    if ((t.t !== "punct" && t.t !== "id") || t.v !== v) throw new ExpressionError(`expected "${v}" but found ${describe(t)}`);
    return t;
  }

  expr(): Node {
    if (this.lang === "js" && this.arrowAhead()) return this.arrow();
    return this.cond();
  }

  cond(): Node {
    const c = this.binary(0);
    if (!this.is("?")) return c;
    this.next();
    const t = this.lang === "cel" ? this.binary(0) : this.expr();
    this.eat(":");
    const f = this.lang === "cel" ? this.cond() : this.expr();
    return { k: "cond", c, t, f };
  }

  static readonly LEVELS: string[][] = [["||", "??"], ["&&"], ["===", "!==", "==", "!=", "<", "<=", ">", ">=", "in"], ["+", "-"], ["*", "/", "%"]];

  binary(level: number): Node {
    if (level >= Parser.LEVELS.length) return this.unary();
    let l = this.binary(level + 1);
    for (;;) {
      const t = this.peek();
      const op = t.v;
      if (!((t.t === "punct" || (t.t === "id" && op === "in")) && (Parser.LEVELS[level] ?? []).includes(op))) return l;
      this.next();
      const r = this.binary(level + 1);
      l = { k: "bin", op, l, r };
    }
  }

  unary(): Node {
    if (this.is("!") || this.is("-")) {
      const op = this.next().v as "!" | "-";
      return { k: "unary", op, x: this.unary() };
    }
    if (this.is("+")) throw new ExpressionError("unary + is not supported");
    if (this.lang === "js" && this.is("typeof")) throw new ExpressionError("typeof is not supported in expressions");
    return this.postfix(this.primary());
  }

  postfix(n: Node): Node {
    for (;;) {
      if (this.is("?.")) throw new ExpressionError("optional chaining (?.) is not supported; test with cel.has(...) instead");
      if (this.is(".")) {
        this.next();
        const name = this.next();
        if (name.t !== "id") throw new ExpressionError(`expected a field name after "." but found ${describe(name)}`);
        if (this.is("(")) {
          n = { k: "mcall", obj: n, name: name.v, args: this.args() };
        } else {
          n = { k: "member", obj: n, name: name.v };
        }
        continue;
      }
      if (this.is("[")) {
        this.next();
        const idx = this.expr();
        this.eat("]");
        n = { k: "index", obj: n, idx };
        continue;
      }
      if (this.is("(")) {
        if (n.k !== "ident") throw new ExpressionError("only named functions can be called");
        n = { k: "call", fn: n.name, args: this.args() };
        continue;
      }
      if (this.lang === "js" && this.is("!") && !this.is("=", 1)) {
        this.next(); // a TypeScript non-null assertion left in source
        continue;
      }
      return n;
    }
  }

  args(): Node[] {
    this.eat("(");
    const out: Node[] = [];
    while (!this.is(")")) {
      if (this.is("...")) throw new ExpressionError("spread arguments are not supported");
      out.push(this.expr());
      if (!this.is(")")) this.eat(",");
    }
    this.eat(")");
    return out;
  }

  primary(): Node {
    const t = this.next();
    switch (t.t) {
      case "num": {
        const hex = /^0[xX]/.test(t.v);
        const double = !hex && /[.eE]/.test(t.v);
        return { k: "lit", v: hex ? parseInt(t.v, 16) : Number(t.v), double, raw: t.v };
      }
      case "str":
        return { k: "lit", v: t.v, raw: t.raw };
      case "tpl":
        return this.template(t.v);
      case "id":
        switch (t.v) {
          case "true":
            return { k: "lit", v: true };
          case "false":
            return { k: "lit", v: false };
          case "null":
            return { k: "lit", v: null };
          case "undefined":
            throw new ExpressionError("undefined has no CEL equivalent; use null, or cel.has(...) to test for a field");
          case "new":
          case "function":
          case "this":
          case "await":
            throw new ExpressionError(`"${t.v}" is not supported in expressions; use a code step`);
        }
        return { k: "ident", name: t.v };
      case "punct":
        if (t.v === "(") {
          const e = this.expr();
          this.eat(")");
          return { k: "paren", x: e };
        }
        if (t.v === "[") {
          const items: Node[] = [];
          while (!this.is("]")) {
            if (this.is("...")) throw new ExpressionError("spread is not supported in expressions");
            items.push(this.expr());
            if (!this.is("]")) this.eat(",");
          }
          this.eat("]");
          return { k: "list", items };
        }
        if (t.v === "{") {
          const entries: { key: Node; value: Node }[] = [];
          while (!this.is("}")) {
            if (this.is("...")) throw new ExpressionError("spread is not supported in expressions");
            let key: Node;
            const kt = this.peek();
            if (this.lang === "js" && kt.t === "id") {
              this.next();
              key = { k: "lit", v: kt.v };
              if (!this.is(":")) {
                entries.push({ key, value: { k: "ident", name: kt.v } }); // shorthand { a }
                if (!this.is("}")) this.eat(",");
                continue;
              }
            } else if (this.lang === "js" && this.is("[")) {
              throw new ExpressionError("computed keys are not supported in expressions");
            } else {
              key = this.lang === "js" ? this.primary() : this.expr();
            }
            this.eat(":");
            entries.push({ key, value: this.expr() });
            if (!this.is("}")) this.eat(",");
          }
          this.eat("}");
          return { k: "map", entries };
        }
    }
    throw new ExpressionError(`unexpected ${describe(t)}`);
  }

  template(raw: string): Node {
    const quasis: string[] = [];
    const exprs: Node[] = [];
    let i = 0;
    let cur = "";
    while (i < raw.length) {
      if (raw[i] === "\\") {
        cur += unescapeChar(raw, i);
        i += escapeLength(raw, i);
        continue;
      }
      if (raw[i] === "$" && raw[i + 1] === "{") {
        let depth = 1;
        let j = i + 2;
        while (j < raw.length && depth > 0) {
          if (raw[j] === "{") depth++;
          if (raw[j] === "}") depth--;
          j++;
        }
        quasis.push(cur);
        cur = "";
        exprs.push(parseWith(raw.slice(i + 2, j - 1), "js"));
        i = j;
        continue;
      }
      cur += raw[i];
      i++;
    }
    quasis.push(cur);
    return { k: "template", quasis, exprs };
  }

  // Skips a parameter's type annotation, up to the closing parenthesis.
  skipType() {
    let depth = 0;
    while (this.peek().t !== "eof") {
      const t = this.peek();
      if (t.t === "punct" && (t.v === "(" || t.v === "[" || t.v === "{" || t.v === "<")) depth++;
      if (t.t === "punct" && (t.v === "]" || t.v === "}" || t.v === ">")) depth--;
      if (t.t === "punct" && t.v === ")") {
        if (depth === 0) return;
        depth--;
      }
      this.next();
    }
  }

  // An arrow starts with "x =>", "() =>", "(x) =>", or "({ a, b }) =>".
  arrowAhead(): boolean {
    if (this.peek().t === "id" && this.is("=>", 1)) return true;
    if (!this.is("(")) return false;
    let depth = 0;
    for (let o = 0; ; o++) {
      const t = this.peek(o);
      if (t.t === "eof") return false;
      if (t.t === "punct" && (t.v === "(" || t.v === "{" || t.v === "[")) depth++;
      if (t.t === "punct" && (t.v === ")" || t.v === "}" || t.v === "]")) depth--;
      if (depth === 0) return this.is("=>", o + 1);
    }
  }

  arrow(): Node {
    let params: Param = { kind: "none" };
    if (this.peek().t === "id") {
      params = { kind: "name", name: this.next().v };
    } else {
      this.eat("(");
      if (this.is("{")) {
        this.next();
        const names = new Map<string, string>();
        while (!this.is("}")) {
          const key = this.next();
          if (key.t !== "id") throw new ExpressionError(`expected a name in the parameter list, found ${describe(key)}`);
          let local = key.v;
          if (this.is(":")) {
            this.next();
            const l = this.next();
            if (l.t !== "id") throw new ExpressionError("only simple renames are supported when destructuring the context");
            local = l.v;
          }
          if (this.is("=")) throw new ExpressionError("default values are not supported in the parameter list");
          names.set(local, key.v);
          if (!this.is("}")) this.eat(",");
        }
        this.eat("}");
        params = { kind: "destructure", names };
      } else if (this.peek().t === "id") {
        params = { kind: "name", name: this.next().v };
        if (this.is(":")) this.skipType(); // a type annotation, as in (e: Employee) =>
      }
      if (this.is(",")) throw new ExpressionError("expression functions take one parameter: the context ({ trigger, steps, env, run, item, index })");
      this.eat(")");
    }
    this.eat("=>");
    let body: Node;
    if (this.is("{")) {
      // A block body is allowed only as "{ return <expr>; }".
      const save = this.i;
      this.next();
      if (this.is("return")) {
        this.next();
        body = this.expr();
        if (this.is(";")) this.next();
        this.eat("}");
      } else {
        this.i = save;
        body = this.primary(); // an object literal
      }
    } else {
      body = this.expr();
    }
    return { k: "arrow", params, body };
  }
}

const EOF: Tok = { t: "eof", v: "", pos: 0 };

function describe(t: Tok): string {
  return t.t === "eof" ? "the end of the expression" : JSON.stringify(t.v);
}

function parseWith(src: string, lang: "js" | "cel"): Node {
  const p = new Parser(lex(src, lang), lang);
  const n = p.expr();
  if (p.peek().t !== "eof") {
    if (lang === "js" && p.is(";") && p.peek(1).t === "eof") return n;
    throw new ExpressionError(`unexpected ${describe(p.peek())}`);
  }
  return n;
}

// ------------------------------------------------- JavaScript to CEL (build)

/** CEL functions callable from code as cel.<name>(...). */
export const CEL_FUNCTIONS = ["has", "size", "string", "int", "uint", "double", "bool", "bytes", "timestamp", "duration", "type", "dyn", "matches"] as const;

const MACROS: Record<string, string> = { filter: "filter", map: "map", some: "exists", every: "all", exists: "exists", all: "all" };

/**
 * Compiles an expression function's source (Function.prototype.toString())
 * to a CEL expression string, without the leading "=".
 */
export function compileFunction(source: string): string {
  const fn = parseWith(source.trim(), "js");
  if (fn.k !== "arrow") throw new ExpressionError("an expression must be an arrow function, such as ({ trigger }) => trigger.body.amount");
  const scope = new Scope(fn.params);
  return printCel(lower(fn.body, scope));
}

/**
 * A number in code, typed by its value: integral is a CEL int, anything else
 * a double. Its source text is not used, since bundling rewrites it (100000
 * becomes 1e5, 2.0 becomes 2), and a JavaScript number has no int or double.
 */
function number(v: number): Node {
  if (!Number.isFinite(v)) throw new ExpressionError("non-finite numbers are not supported");
  if (Number.isInteger(v)) {
    if (!Number.isSafeInteger(v)) throw new ExpressionError(`${v} is too large to be exact in code; write the expression as a CEL string`);
    return { k: "lit", v, raw: String(v) };
  }
  return { k: "lit", v, double: true, raw: String(v) };
}

class Scope {
  vars = new Set<string>();
  constructor(readonly param: Param) {}
  with(v: string): Scope {
    const s = new Scope(this.param);
    s.vars = new Set(this.vars).add(v);
    return s;
  }
}

function lower(n: Node, s: Scope): Node {
  switch (n.k) {
    case "lit":
      return typeof n.v === "number" ? number(n.v) : n;
    case "paren":
      return lower(n.x, s);
    case "ident": {
      if (s.vars.has(n.name)) return n;
      if (s.param.kind === "destructure") {
        const root = s.param.names.get(n.name);
        if (root !== undefined) {
          checkRoot(root);
          return { k: "ident", name: root };
        }
      }
      if (s.param.kind === "name" && n.name === s.param.name) {
        throw new ExpressionError(`"${n.name}" on its own is not an expression value; read a field, such as ${n.name}.trigger.body`);
      }
      if (n.name === "cel") throw new ExpressionError("cel is only for calls such as cel.has(...)");
      throw new ExpressionError(`"${n.name}" is not available in an expression (only ${ROOTS.join(", ")}); compute it in a code step`);
    }
    case "member":
      if (s.param.kind === "name" && n.obj.k === "ident" && n.obj.name === s.param.name && !s.vars.has(n.obj.name)) {
        checkRoot(n.name);
        return { k: "ident", name: n.name };
      }
      if (n.name === "length") throw new ExpressionError(".length is ambiguous in CEL; use cel.size(x)");
      return { k: "member", obj: lower(n.obj, s), name: n.name };
    case "index":
      return { k: "index", obj: lower(n.obj, s), idx: lower(n.idx, s) };
    case "call":
      throw new ExpressionError(`${n.fn}(...) is not available in an expression; CEL functions are called as cel.<name>(...)`);
    case "mcall": {
      if (isCelRef(n.obj, s)) {
        if (n.name === "in") {
          if (n.args.length !== 2) throw new ExpressionError("cel.in takes two arguments: cel.in(value, list)");
          const [value, list] = n.args as [Node, Node];
          return { k: "bin", op: "in", l: lower(value, s), r: lower(list, s) };
        }
        if (!(CEL_FUNCTIONS as readonly string[]).includes(n.name)) throw new ExpressionError(`cel.${n.name} is not a CEL function`);
        return { k: "call", fn: n.name, args: n.args.map((a) => lower(a, s)) };
      }
      const macro = MACROS[n.name];
      const a = n.args[0];
      if (macro && n.args.length === 1 && a?.k === "arrow") {
        if (a.params.kind !== "name") throw new ExpressionError(`.${n.name}(...) takes a one-parameter arrow function, such as (e) => e.amount > 0`);
        const v = a.params.name;
        return { k: "mcall", obj: lower(n.obj, s), name: macro, args: [{ k: "ident", name: v }, lower(a.body, s.with(v))] };
      }
      if (["toLowerCase", "toUpperCase", "includes", "find", "reduce", "forEach", "slice", "toString", "toFixed"].includes(n.name)) {
        throw new ExpressionError(`.${n.name}() has no CEL equivalent here; see docs/contracts/wd-v1.md for CEL functions, or use a code step`);
      }
      return { k: "mcall", obj: lower(n.obj, s), name: n.name, args: n.args.map((a) => lower(a, s)) };
    }
    case "unary":
      return { k: "unary", op: n.op, x: lower(n.x, s) };
    case "bin": {
      const op = { "===": "==", "!==": "!=" }[n.op] ?? n.op;
      if (n.op === "==" || n.op === "!=") throw new ExpressionError(`use ${n.op}= instead of ${n.op}: CEL equality is strict`);
      if (n.op === "??") throw new ExpressionError("?? is not supported; use cel.has(x) ? x : fallback");
      if (n.op === "in") throw new ExpressionError(`"in" means something else in JavaScript; use cel.in(value, list)`);
      // JavaScript ranks === below <, CEL ranks them equal: make the
      // grouping explicit rather than pick one reading.
      if (PREC[op] === 4 && [n.l, n.r].some((x) => x.k === "bin" && PREC[x.op] === 4)) {
        throw new ExpressionError("add parentheses around comparisons used inside another comparison");
      }
      return { k: "bin", op, l: lower(n.l, s), r: lower(n.r, s) };
    }
    case "cond":
      return { k: "cond", c: lower(n.c, s), t: lower(n.t, s), f: lower(n.f, s) };
    case "list":
      return { k: "list", items: n.items.map((i) => lower(i, s)) };
    case "map":
      return { k: "map", entries: n.entries.map((e) => ({ key: lower(e.key, s), value: lower(e.value, s) })) };
    case "template": {
      let out: Node | null = null;
      const add = (x: Node) => {
        out = out === null ? x : { k: "bin", op: "+", l: out, r: x };
      };
      n.quasis.forEach((q, i) => {
        if (q !== "") add({ k: "lit", v: q });
        const e = n.exprs[i];
        if (e) add({ k: "call", fn: "string", args: [lower(e, s)] });
      });
      return out ?? { k: "lit", v: "" };
    }
    case "arrow":
      throw new ExpressionError("a function is only allowed as the argument of .filter, .map, .some or .every");
  }
}

// The SDK's cel helpers as they appear in function source: "cel", or as
// bundlers rewrite an imported binding: "cel2" (esbuild renaming),
// "import_sdk.cel" or "__vite_ssr_import_0__.cel" (module namespaces).
function isCelRef(n: Node, s: Scope): boolean {
  const bound = (name: string) =>
    s.vars.has(name) || (s.param.kind === "name" && s.param.name === name) || (s.param.kind === "destructure" && s.param.names.has(name));
  if (n.k === "ident") return /^cel\d*$/.test(n.name) && !bound(n.name);
  return n.k === "member" && n.name === "cel" && n.obj.k === "ident" && !bound(n.obj.name) && !(ROOTS as readonly string[]).includes(n.obj.name);
}

function checkRoot(name: string) {
  if (!(ROOTS as readonly string[]).includes(name)) throw new ExpressionError(`the context has no "${name}" (only ${ROOTS.join(", ")})`);
}

// Precedence for printing, lowest first. CEL and the JavaScript we print
// share the order: ?: || && relations + * unary member.
const PREC: Record<string, number> = { "||": 2, "&&": 3, "==": 4, "!=": 4, "<": 4, "<=": 4, ">": 4, ">=": 4, in: 4, "===": 4, "!==": 4, "+": 5, "-": 5, "*": 6, "/": 6, "%": 6 };

function prec(n: Node): number {
  switch (n.k) {
    case "paren":
      return prec(n.x);
    case "cond":
      return 1;
    case "bin":
      return PREC[n.op] ?? 0;
    case "unary":
      return 7;
    default:
      return 8;
  }
}

/** Prints CEL canonically: single-quoted strings, minimal parentheses. */
function printCel(n: Node): string {
  const p = (x: Node, min: number) => (prec(x) < min ? `(${printCel(x)})` : printCel(x));
  switch (n.k) {
    case "ident":
      return n.name;
    case "lit":
      return printLit(n, "cel");
    case "member":
      return `${p(n.obj, 8)}.${n.name}`;
    case "index":
      return `${p(n.obj, 8)}[${printCel(n.idx)}]`;
    case "call":
      return `${n.fn}(${n.args.map(printCel).join(", ")})`;
    case "mcall":
      return `${p(n.obj, 8)}.${n.name}(${n.args.map(printCel).join(", ")})`;
    case "unary":
      return n.op + p(n.x, 7);
    case "bin": {
      const lp = PREC[n.op] ?? 0;
      return `${p(n.l, lp)} ${n.op} ${p(n.r, lp + 1)}`;
    }
    case "cond":
      return `${p(n.c, 2)} ? ${p(n.t, 2)} : ${p(n.f, 1)}`;
    case "list":
      return `[${n.items.map(printCel).join(", ")}]`;
    case "map":
      return `{${n.entries.map((e) => `${printCel(e.key)}: ${printCel(e.value)}`).join(", ")}}`;
    default:
      throw new ExpressionError(`cannot print ${n.k} as CEL`);
  }
}

function printLit(n: Extract<Node, { k: "lit" }>, lang: "cel" | "js"): string {
  if (typeof n.v === "string") {
    const q = lang === "cel" ? "'" : '"';
    let out = q;
    for (const ch of n.v) {
      switch (ch) {
        case "\\":
          out += "\\\\";
          break;
        case q:
          out += "\\" + q;
          break;
        case "\n":
          out += "\\n";
          break;
        case "\r":
          out += "\\r";
          break;
        case "\t":
          out += "\\t";
          break;
        default: {
          const cp = ch.codePointAt(0) ?? 0;
          out += cp < 0x20 || cp === 0x7f ? `\\u${cp.toString(16).padStart(4, "0")}` : ch;
        }
      }
    }
    return out + q;
  }
  if (typeof n.v === "number") {
    if (!Number.isFinite(n.v)) throw new ExpressionError("non-finite numbers are not supported");
    if (n.double) {
      const s = n.raw ?? String(n.v);
      return /[.eE]/.test(s) ? s : s + ".0";
    }
    return n.raw && !/^0[xX]/.test(n.raw) ? n.raw : String(n.v);
  }
  return String(n.v);
}

// -------------------------------------------- CEL to JavaScript (generation)

/**
 * Prints a CEL expression (without "=") as an arrow function, or returns
 * null when it cannot be printed so that compiling it gives back exactly
 * the same CEL text. Callers then keep the raw "=..." string, so generated
 * code always compiles to the definition it came from.
 */
export function celToFunction(cel: string): string | null {
  let ast: Node;
  try {
    ast = parseWith(cel, "cel");
  } catch {
    return null;
  }
  const roots = new Set<string>();
  let body: string;
  try {
    body = printJs(ast, new Set(), roots);
  } catch {
    return null;
  }
  if (roots.size === 0) return null; // a constant is clearer as a literal
  const names = ROOTS.filter((r) => roots.has(r));
  const fn = `({ ${names.join(", ")} }) => ${body.startsWith("{") ? `(${body})` : body}`;
  try {
    if (compileFunction(fn) !== cel) return null;
  } catch {
    return null;
  }
  return fn;
}

function printJs(n: Node, vars: Set<string>, roots: Set<string>): string {
  const p = (x: Node, min: number) => (prec(x) < min ? `(${printJs(x, vars, roots)})` : printJs(x, vars, roots));
  const all = (xs: Node[]) => xs.map((x) => printJs(x, vars, roots)).join(", ");
  switch (n.k) {
    case "ident":
      if (vars.has(n.name)) return n.name;
      if (!(ROOTS as readonly string[]).includes(n.name)) throw new ExpressionError(`unknown variable ${n.name}`);
      roots.add(n.name);
      return n.name;
    case "paren":
      return printJs(n.x, vars, roots);
    case "lit":
      // esbuild prints 100.0 as 100, which CEL would read as an int.
      if (n.double && !/\.[0-9]*[1-9]|[eE]/.test(n.raw ?? "")) throw new ExpressionError("an integral double would be printed as an int");
      return printLit(n, "js");
    case "member":
      if (n.name === "length") throw new ExpressionError("a field named length cannot be read in code");
      return `${p(n.obj, 8)}.${n.name}`;
    case "index":
      return `${p(n.obj, 8)}[${printJs(n.idx, vars, roots)}]`;
    case "call":
      if (!(CEL_FUNCTIONS as readonly string[]).includes(n.fn)) throw new ExpressionError(`no code form for ${n.fn}()`);
      return `cel.${n.fn}(${all(n.args)})`;
    case "mcall": {
      const js = Object.entries(MACROS).find(([js, cel]) => cel === n.name && js !== "exists" && js !== "all")?.[0];
      const [first, body] = n.args;
      if (js && n.args.length === 2 && first?.k === "ident" && body) {
        const inner = new Set(vars).add(first.name);
        return `${p(n.obj, 8)}.${js}((${first.name}) => ${printJs(body, inner, roots)})`;
      }
      if (MACROS[n.name] !== undefined || n.name === "exists_one") throw new ExpressionError("macro without a code form");
      return `${p(n.obj, 8)}.${n.name}(${all(n.args)})`;
    }
    case "unary":
      return n.op + p(n.x, 7);
    case "bin": {
      if (n.op === "in") return `cel.in(${printJs(n.l, vars, roots)}, ${printJs(n.r, vars, roots)})`;
      const op = { "==": "===", "!=": "!==" }[n.op] ?? n.op;
      const lp = PREC[n.op] ?? 0;
      const lmin = lp === 4 ? 5 : lp; // comparisons inside comparisons get parentheses
      return `${p(n.l, lmin)} ${op} ${p(n.r, lp + 1)}`;
    }
    case "cond":
      return `${p(n.c, 2)} ? ${p(n.t, 2)} : ${p(n.f, 1)}`;
    case "list":
      return `[${all(n.items)}]`;
    case "map":
      return `{ ${n.entries
        .map((e) => {
          if (e.key.k !== "lit" || typeof e.key.v !== "string") throw new ExpressionError("only string keys have a code form");
          const key = /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(e.key.v) ? e.key.v : printLit(e.key, "js");
          return `${key}: ${printJs(e.value, vars, roots)}`;
        })
        .join(", ")} }`;
    default:
      throw new ExpressionError(`cannot print ${n.k}`);
  }
}
