// Passkeys in the browser: turn the server's options (base64url) into what
// navigator.credentials expects, and the credential back into JSON.

import { post, type Me } from "./api";

const fromB64 = (s: string): ArrayBuffer => {
  const bin = atob(s.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(s.length / 4) * 4, "="));
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out.buffer;
};

const toB64 = (b: ArrayBuffer | null | undefined): string | undefined => {
  if (!b) return undefined;
  let s = "";
  for (const c of new Uint8Array(b)) s += String.fromCharCode(c);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
};

interface Descriptor {
  type: "public-key";
  id: string;
}

export interface CreationJSON {
  challenge: string;
  rp: PublicKeyCredentialRpEntity;
  user: { id: string; name: string; displayName: string };
  pubKeyCredParams: PublicKeyCredentialParameters[];
  excludeCredentials?: Descriptor[];
  authenticatorSelection?: AuthenticatorSelectionCriteria;
  attestation?: AttestationConveyancePreference;
  timeout?: number;
}

export interface RequestJSON {
  challenge: string;
  rpId?: string;
  allowCredentials?: Descriptor[];
  userVerification?: UserVerificationRequirement;
  timeout?: number;
}

const descriptors = (ds?: Descriptor[]) => ds?.map((d) => ({ type: d.type, id: fromB64(d.id) }));

export const passkeysSupported = () => typeof window !== "undefined" && "PublicKeyCredential" in window;

function credentialJSON(c: PublicKeyCredential) {
  const r = c.response as AuthenticatorAttestationResponse & AuthenticatorAssertionResponse;
  return {
    id: c.id,
    rawId: toB64(c.rawId),
    type: c.type,
    response: {
      clientDataJSON: toB64(r.clientDataJSON),
      ...("attestationObject" in r && { attestationObject: toB64(r.attestationObject), transports: r.getTransports?.() }),
      ...("authenticatorData" in r && { authenticatorData: toB64(r.authenticatorData), signature: toB64(r.signature), userHandle: toB64(r.userHandle) }),
    },
  };
}

/** Proof, besides the session, for adding or removing a factor. */
export interface Proof {
  password?: string;
  totp?: string;
  passkey?: ReturnType<typeof credentialJSON>;
}

/** Builds the proof the server asks for: an existing passkey, else the
 * authenticator code or password the member typed. A passkey is asked for
 * this change only (action: passkey.add, passkey.remove/<id>, totp.setup,
 * password.change, whatsapp.link, whatsapp.pin). */
export async function proof(factors: Me["factors"], typed: string, action: string): Promise<Proof> {
  if (factors?.passkey) return { passkey: await stepUpAssert("account.reauth", action) };
  if (factors?.totp) return { totp: typed };
  if (factors?.password) return { password: typed };
  return {};
}

/** Creates a passkey from the server's creation options (base64url). */
export async function createFrom(o: CreationJSON) {
  const cred = (await navigator.credentials.create({
    publicKey: {
      ...o,
      challenge: fromB64(o.challenge),
      user: { ...o.user, id: fromB64(o.user.id) },
      excludeCredentials: descriptors(o.excludeCredentials),
    },
  })) as PublicKeyCredential | null;
  if (!cred) throw new Error("No passkey was created");
  return credentialJSON(cred);
}

/** Asks for a passkey for the server's request options (base64url). */
export async function assertFrom(o: RequestJSON) {
  const cred = (await navigator.credentials.get({
    publicKey: { ...o, challenge: fromB64(o.challenge), allowCredentials: descriptors(o.allowCredentials) },
  })) as PublicKeyCredential | null;
  if (!cred) throw new Error("No passkey was used");
  return credentialJSON(cred);
}

/** Creates a passkey for the signed-in member. */
export async function addPasskey(name: string, p: Proof) {
  const { publicKey: o } = await post<{ publicKey: CreationJSON }>("/v1/me/passkeys/options", p);
  const cred = (await navigator.credentials.create({
    publicKey: {
      ...o,
      challenge: fromB64(o.challenge),
      user: { ...o.user, id: fromB64(o.user.id) },
      excludeCredentials: descriptors(o.excludeCredentials),
    },
  })) as PublicKeyCredential | null;
  if (!cred) throw new Error("No passkey was created");
  await post("/v1/me/passkeys", { name, credential: credentialJSON(cred) });
}

/** Asks for a passkey for options from the server; returns its JSON. */
export async function assert(path: string, body?: object) {
  const { publicKey: o } = await post<{ publicKey: RequestJSON }>(path, body);
  const cred = (await navigator.credentials.get({
    publicKey: { ...o, challenge: fromB64(o.challenge), allowCredentials: descriptors(o.allowCredentials) },
  })) as PublicKeyCredential | null;
  if (!cred) throw new Error("No passkey was used");
  return credentialJSON(cred);
}

/** A passkey assertion for step-up, good only for this operation on this
 * target (an approval decision, a key change, a factor change). */
export const stepUpAssert = (operation: string, target: string) => assert("/v1/me/step-up/options", { operation, target });

/** Signs in with a passkey. */
export async function passkeyLogin() {
  await post("/v1/auth/passkey", { credential: await assert("/v1/auth/passkey/options") });
}
