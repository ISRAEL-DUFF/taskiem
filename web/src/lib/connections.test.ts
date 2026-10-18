import { describe, expect, it } from "vitest";
import type { ConnectorInfo } from "../api";
import { connectorOf, fieldsToComplete, linkResult } from "./connections";

const xenibase: ConnectorInfo = {
  ref: "pgdock@1",
  id: "pgdock",
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
    expect(connectorOf([plain, xenibase], "pgdock")?.name).toBe("Xenibase");
    expect(connectorOf([plain], "pgdock")).toBeUndefined();
    expect(connectorOf(undefined, "pgdock")).toBeUndefined();
  });
});
