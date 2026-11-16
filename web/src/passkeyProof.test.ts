import { afterEach, describe, expect, it, vi } from "vitest";
import { passkeyProof } from "./passkeys";

// A passkey as a second proof proves the device and leaves the session as
// it is (C2): it never signs in again with the passkey (/v1/auth/passkey).
describe("a passkey as a second proof", () => {
  afterEach(() => vi.unstubAllGlobals());
  it("asks the second proof's own options and sends the assertion there", async () => {
    const calls: { path: string; body: unknown }[] = [];
    vi.stubGlobal("window", { dispatchEvent: () => true, location: { pathname: "/you" } });
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string, init: RequestInit) => {
        calls.push({ path, body: JSON.parse(String(init.body ?? "{}")) });
        const body = path.endsWith("/options") ? { publicKey: { challenge: "AAAA", rpId: "localhost" } } : { device_proved: true };
        return new Response(JSON.stringify(body), { status: 200 });
      }),
    );
    const bytes = new Uint8Array([1, 2, 3]).buffer;
    vi.stubGlobal("navigator", {
      credentials: {
        get: vi.fn(async () => ({
          id: "cred",
          rawId: bytes,
          type: "public-key",
          response: { clientDataJSON: bytes, authenticatorData: bytes, signature: bytes, userHandle: null },
        })),
      },
    });
    await passkeyProof();
    expect(calls.map((c) => c.path)).toEqual(["/v1/auth/second-proof/passkey/options", "/v1/auth/second-proof/passkey"]);
    expect(calls[1]?.body).toMatchObject({ credential: { id: "cred", type: "public-key" } });
    expect(calls.some((c) => c.path === "/v1/auth/passkey")).toBe(false);
  });
});
