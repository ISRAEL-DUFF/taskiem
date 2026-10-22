import { describe, expect, it } from "vitest";
import { capableConnections } from "./capabilities";

describe("capableConnections", () => {
  const connectors = [
    { id: "paystack", ref: "paystack@1", name: "Paystack" },
    { id: "iswallet", ref: "iswallet@1", name: "iswallet" },
    { id: "googlesheets", ref: "googlesheets@1", name: "Google Sheets" },
  ];
  it("offers the connections whose service can, once per name, with the service", () => {
    const got = capableConnections(
      [
        { connector: "paystack", name: "main", environment: "dev", status: "active" },
        { connector: "paystack", name: "main", environment: "prod", status: "expired" },
        { connector: "iswallet", name: "wallet", environment: "prod", status: "active" },
        { connector: "googlesheets", name: "sheet", environment: "prod", status: "active" },
      ],
      ["iswallet@1", "paystack@1"],
      connectors,
    );
    expect(got).toEqual([
      { name: "main", label: "main (Paystack, expired)" },
      { name: "wallet", label: "wallet (iswallet)" },
    ]);
  });
});
