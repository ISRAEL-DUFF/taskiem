import { describe, expect, it } from "vitest";
import type { AuthField, ConnectorInfo } from "../api";
import { connectorOf, fieldsToComplete, keyProblem, linkResult, modesOf, needsConnection, splitFields } from "./connections";
import { connectionLabel, connectorID, pickable } from "../forms/ConnectionPicker";
import { sheetIdOf } from "../forms/SheetChooser";

const xenibase: ConnectorInfo = {
  ref: "xenibase@1",
  id: "xenibase",
  version: "1.1.0",
  name: "Xenibase",
  auth: {
    type: "api_key",
    fields: [
      { key: "token", label: "Token", secret: true },
      { key: "project_id", label: "Project" },
      { key: "org_id", label: "Organisation", required: false },
      { key: "base_url", label: "Server", required: false },
      { key: "secret_key", label: "Secret key", secret: true, required: false },
      { key: "api_url", label: "Data API URL", required: false },
    ],
    oauth2: { authorize_url: "${server}/oauth/authorize", token_url: "${server}/api/v1/oauth/token", scopes: ["read", "write"], pkce: true, fields_from_token: { token: "access_token", org_id: "org_id", project_id: "project_ids[0]", base_url: "${server}" } },
  },
  oauth_linkable: true,
  actions: {},
  triggers: [],
};
const plain: ConnectorInfo = { ...xenibase, ref: "paystack@1", id: "paystack", name: "Paystack", auth: { type: "api_key", fields: [{ key: "secret_key", label: "Secret key", secret: true }] }, oauth_linkable: undefined };

describe("linkResult", () => {
  it("reads the new connection's id", () => {
    expect(linkResult("?linked=abc")).toEqual({ linked: "abc" });
  });
  it("reads what went wrong, decoded", () => {
    expect(linkResult("?error=You+did+not+approve+the+connection+in+Xenibase")).toEqual({ error: "You did not approve the connection in Xenibase" });
  });
  it("is empty otherwise", () => {
    expect(linkResult("")).toEqual({});
    expect(linkResult("?environment=prod")).toEqual({});
  });
});

describe("fieldsToComplete", () => {
  it("lists the optional fields the token does not fill", () => {
    expect(fieldsToComplete(xenibase)?.map((f) => f.key)).toEqual(["secret_key", "api_url"]);
  });
  it("is empty for a connector without OAuth, or none", () => {
    expect(fieldsToComplete(plain)).toEqual([]);
    expect(fieldsToComplete(undefined)).toEqual([]);
  });
});

describe("connectorOf", () => {
  it("finds a connector by the id a connection names", () => {
    expect(connectorOf([plain, xenibase], "xenibase")?.name).toBe("Xenibase");
    expect(connectorOf([plain], "xenibase")).toBeUndefined();
    expect(connectorOf(undefined, "xenibase")).toBeUndefined();
  });
});

const secretKey: AuthField = {
  key: "secret_key",
  label: "Secret key",
  secret: true,
  prefixes: [
    { prefix: "sk_live_", mode: "live", label: "Live (real money)" },
    { prefix: "sk_test_", mode: "test", label: "Test (practice)" },
  ],
  refuse: [{ prefix: "pk_", message: "That is your public key." }],
};

describe("guided keys", () => {
  it("says what is wrong with a pasted key, as the server does", () => {
    expect(keyProblem(secretKey, "pk_live_1", "live")).toBe("That is your public key.");
    expect(keyProblem(secretKey, "sk_test_1", "live")).toBe("that is a test (practice) secret key, but you chose live (real money); copy the live (real money) secret key instead");
    expect(keyProblem(secretKey, "hello", "test")).toBe("that does not look like your secret key: it starts with sk_test_");
    expect(keyProblem(secretKey, "sk_live_1", "live")).toBe("");
    expect(keyProblem(secretKey, "", "live")).toBe("");
    expect(keyProblem({ key: "k", label: "API key" }, "anything")).toBe("");
  });
  it("offers live and test as choices, and keeps optional fields aside", () => {
    const c = { auth: { type: "api_key", fields: [secretKey, { key: "hash", label: "Hash", required: false }] } } as unknown as ConnectorInfo;
    expect(modesOf(c)).toEqual([
      { mode: "live", label: "Live (real money)" },
      { mode: "test", label: "Test (practice)" },
    ]);
    expect(splitFields(c).main.map((f) => f.key)).toEqual(["secret_key"]);
    expect(splitFields(c).more.map((f) => f.key)).toEqual(["hash"]);
  });
});

describe("connection pickers", () => {
  it("offers the service's connections once each, prod's first", () => {
    const list = [
      { connector: "paystack", name: "main", environment: "dev", status: "active", mode: "test" },
      { connector: "paystack", name: "main", environment: "prod", status: "expired", mode: "live" },
      { connector: "termii", name: "main", environment: "prod", status: "active" },
    ];
    expect(pickable(list, "paystack@1")).toEqual([{ name: "main", status: "expired", mode: "live" }]);
    expect(connectorID("paystack@1")).toBe("paystack");
    expect(connectionLabel({ name: "main", status: "active", mode: "test" })).toBe("main (test key: no real money)");
    expect(connectionLabel({ name: "main", status: "expired" })).toBe("main (expired)");
  });
  it("finds a spreadsheet's id in its link", () => {
    expect(sheetIdOf("https://docs.google.com/spreadsheets/d/1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789/edit#gid=0")).toBe("1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789");
    expect(sheetIdOf("1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789")).toBe("1AbCdEfGhIjKlMnOpQrStUvWxYz0123456789");
    expect(sheetIdOf("my sales sheet")).toBe("");
  });
  it("needs no connection for a connector without auth", () => {
    expect(needsConnection(xenibase)).toBe(true);
    expect(needsConnection({ ...xenibase, auth: { type: "none", fields: null } })).toBe(false);
    expect(needsConnection({ ...xenibase, auth: { type: "", fields: null } })).toBe(false);
  });
});
