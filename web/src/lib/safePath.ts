// Where the app may send someone after they sign in: ?next= comes from the
// address, so anyone can write it. The rule is api/sso.go's safeReturn: a
// path that starts with a single slash, with no scheme or host, and no
// backslash or control character, encoded or not (browsers read
// "/\evil.example" and "/\t/evil.example" as another host). react-router's
// <Navigate> throws on any other address, and that throw blanks the app.

const bad = (s: string) => [...s].some((c) => c === "\\" || c < " " || c === "\u007f");

/** next when it is one of the app's own paths, else fallback. */
export function safeNext(next: string | null | undefined, fallback: string): string {
  if (!next || !next.startsWith("/") || next.startsWith("//") || bad(next)) return fallback;
  let path: string;
  try {
    path = decodeURIComponent(next.split(/[?#]/, 1)[0] ?? "");
  } catch {
    return fallback;
  }
  if (path.startsWith("//") || bad(path)) return fallback;
  return next;
}
