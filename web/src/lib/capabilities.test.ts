import { describe, expect, it } from "vitest";
import { capableConnections, railsForAll, templateCapabilities } from "./capabilities";

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

describe("railsForAll", () => {
  it("keeps the rails that can do everything, best known first", () => {
    const caps = [
      { name: "pay_bank", rails: ["anchor@1", "iswallet@1", "paystack@1", "lenco@1"] },
      { name: "balance", rails: ["anchor@1", "iswallet@1", "paystack@1"] },
    ];
    expect(railsForAll(caps, ["pay_bank", "balance"])).toEqual(["paystack", "iswallet", "anchor"]);
    expect(railsForAll(caps, ["refund"])).toEqual([]);
    expect(templateCapabilities(["xenibase@1", "capability:pay_bank", "capability:balance"])).toEqual(["pay_bank", "balance"]);
  });
  it("counts a rail that sends any one of a trigger's payment events", () => {
    const caps = [
      { name: "payment.received", rails: ["paystack@1", "moniepoint@1", "flutterwave@1"], kind: "event" },
      { name: "virtual_account.credited", rails: ["moniepoint@1"], kind: "event" },
    ];
    expect(railsForAll(caps, ["payment.received", "virtual_account.credited"])).toEqual(["paystack", "moniepoint", "flutterwave"]);
  });
});
