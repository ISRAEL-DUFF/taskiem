import { describe, expect, it } from "vitest";
import {
  bankFields,
  bankValueOf,
  checkLine,
  detailsProblem,
  filterBanks,
  kindChoice,
  cleanName,
  nameProblem,
  savedSensitive,
  savedSuggestions,
  numberProblem,
  providerLine,
  savedBase,
  savedSub,
  showConnectStep,
  kindProblem,
  shownValue,
  valuesProblem,
  placeAddress,
  placeAddressFit,
  personFill,
  savedPhone,
  savedDates,
  nextBirthday,
  savedDateSub,
  bankCodeFor,
  revealProblem,
  savedNameFor,
  placeTitleFor,
  placeOffer,
  placeBody,
  customerOffer,
  type BankList,
  type MadeKind,
  type KindField,
  type SavedThing,
} from "./saved";

const bayo: SavedThing = {
  id: "1",
  owner: "business",
  name: "bayo",
  title: "Bayo (rice)",
  kind: "bank_account",
  hint: "GTBank ••6789",
  details: { bank_name: "Guaranty Trust Bank", account_number: "••6789" },
  check: { state: "unchecked", account_name: null, via: null, at: null, problem: null },
  to_change: true,
  created_at: "2026-11-20T10:00:00Z",
  updated_at: "2026-11-20T10:00:00Z",
};

describe("saved things", () => {
  it("cleans and checks names as the server does", () => {
    expect(cleanName(" @Bola Stores ")).toBe("bola-stores");
    expect(nameProblem("@mum")).toBeNull();
    expect(nameProblem("x")).toMatch(/2 to 32/);
    expect(nameProblem("bola--stores")).toMatch(/only letters/);
    expect(nameProblem("-mum")).toMatch(/only letters/);
    expect(nameProblem("")).toMatch(/short name/);
  });

  it("checks account numbers", () => {
    expect(numberProblem("012 345-6789")).toBeNull();
    expect(numberProblem("12345")).toBe("An account number is 10 digits.");
  });

  it("words a row", () => {
    expect(savedSub(bayo)).toBe("@bayo · GTBank ••6789");
    expect(checkLine(bayo)).toEqual({ words: "Name not checked yet", tone: "warn" });
    expect(checkLine({ ...bayo, check: { state: "unchecked", account_name: null, via: null, at: null, problem: "Choose the bank again." } })).toEqual({
      words: "Choose the bank again.",
      tone: "warn",
    });
    expect(checkLine({ ...bayo, check: { state: "checked", account_name: "BAYO ADEWALE", via: "iswallet/taskiem", at: "x", problem: null } })).toEqual({
      words: "Bank's name: BAYO ADEWALE",
      tone: "ok",
    });
  });

  it("picks banks from a list with codes, or by name", () => {
    const wallet: BankList = { from: "iswallet/taskiem", banks: [{ code: "000013", name: "GUARANTY TRUST BANK" }, { code: "000015", name: "Zenith Bank" }] };
    const byName: BankList = { from: "", banks: [{ code: "", name: "Guaranty Trust Bank" }] };
    expect(bankFields(wallet, "c:000013")).toEqual({ bank_name: "GUARANTY TRUST BANK", bank_code: "000013", code_from: "iswallet/taskiem" });
    expect(bankFields(byName, "n:Guaranty Trust Bank")).toEqual({ bank_name: "Guaranty Trust Bank" });
    expect(bankFields(wallet, "c:999")).toBeNull();
    // A saved account's bank is found again: by code from the same list, else by name.
    expect(bankValueOf(wallet, { bank_name: "Guaranty Trust Bank" })).toBe("c:000013");
    expect(bankValueOf(wallet, { bank_name: "x", bank_code: "000015", code_from: "iswallet/taskiem" })).toBe("c:000015");
    expect(bankValueOf(byName, { bank_name: "Kuda" })).toBe("");
  });

  it("searches banks by the start of their words", () => {
    const banks = [
      { code: "1", name: "Guaranty Trust Bank" },
      { code: "2", name: "Zenith Bank" },
      { code: "3", name: "First Bank of Nigeria" },
      { code: "4", name: "First City Monument Bank" },
      { code: "5", name: "GTBank" },
    ];
    const names = (typed: string, chosen = "") => filterBanks(banks, typed, chosen).map((b) => b.name);
    expect(names("")).toHaveLength(5);
    expect(names("guar")).toEqual(["Guaranty Trust Bank"]);
    expect(filterBanks([...banks, { code: "6", name: "Kuda Microfinance Bank" }], "fi").map((b) => b.name)).toEqual(["First Bank of Nigeria", "First City Monument Bank"]);
    expect(names("first")).toEqual(["First Bank of Nigeria", "First City Monument Bank"]);
    expect(names("first city")).toEqual(["First City Monument Bank"]);
    expect(names("gtb")).toEqual(["GTBank"]);
    expect(names("bank")).toHaveLength(5); // "bank" alone is not a word to search by
    expect(names("zzz")).toEqual([]);
    expect(names("zzz", "c:2")).toEqual(["Zenith Bank"]); // the chosen one stays
  });

  it("words the kinds and checks their forms", () => {
    expect(kindChoice("person")).toBe("A person");
    expect(kindChoice("provider_account")).toBe("An account with a provider");
    expect(detailsProblem("place", { area: "Yaba" })).toBe("Give the place its street and number.");
    expect(detailsProblem("text", { body: "  " })).toBe("Write the text.");
    expect(detailsProblem("provider_account", { provider: "DSTV", number: "123" })).toBe("Choose what the provider is.");
    expect(detailsProblem("person", {})).toBeNull();
    expect(savedSub({ ...bayo, kind: "person", hint: "" })).toBe("@bayo");
  });

  it("says where names are checked, and to whom the connect step is shown", () => {
    expect(providerLine({ connected: { channel: "iswallet/taskiem", name: "Your Taskiem wallet", wallet: true }, owner: true })).toBe(
      "Names are checked with the bank through your Taskiem wallet.",
    );
    expect(providerLine({ connected: { channel: "paystack/main", name: "Paystack", wallet: false }, owner: false })).toMatch(/through Paystack\.$/);
    expect(showConnectStep({ connected: null, owner: true })).toBe(true);
    expect(showConnectStep({ connected: null, owner: false })).toBe(false);
    expect(savedBase(true)).toBe("/v1/me/saved");
  });
});

describe("saved things in the picker", () => {
  const text: SavedThing = { ...bayo, id: "t", kind: "text", name: "bank-details", title: "Our bank details", hint: "2 lines of text", details: { body: "Pay to Bola\nGTBank 0123" } };
  const mine: SavedThing = { ...bayo, id: "m", owner: "mine", name: "bank-details", title: "My bank details", kind: "text", hint: "1 line of text", details: { body: "Mine" } };
  it("offers one's own first, texts with their words, and says whose when two share a name", () => {
    const s = savedSuggestions([mine], [text, bayo], false);
    expect(s.map((x) => x.id)).toEqual(["m", "t", "1"]);
    expect(s[0]).toMatchObject({ kind: "saved", name: "bank-details", body: "Mine", sub: "My bank details · 1 line of text · Yours" });
    expect(s[1]?.sub).toBe("Our bank details · 2 lines of text · The business's");
    expect(s[2]).toMatchObject({ savedKind: "bank_account", body: undefined, sub: "Bayo (rice) · GTBank ••6789" });
  });
  it("offers texts only where nothing else can be shared yet", () => {
    expect(savedSuggestions([], [text, bayo], true).map((x) => x.id)).toEqual(["t"]);
  });
  it("asks before sharing as the server says, a made-up kind's sensitive fields too", () => {
    const car: SavedThing = { ...bayo, id: "c", kind: "custom", kind_name: "Car", name: "van", title: "Van", details: {}, share_sensitive: true };
    expect(savedSensitive(car)).toBe(true);
    expect(savedSensitive({ ...bayo, share_sensitive: false })).toBe(false);
    // An older server says nothing: the kinds' own rule.
    expect(savedSensitive({ kind: "person", details: { phone: "+2348031234567" } })).toBe(true);
    expect(savedSuggestions([], [car], false)[0]?.savedSensitive).toBe(true);
  });
});

describe("kinds the owner makes", () => {
  const fields: KindField[] = [
    { key: "plate_number", label: "Plate number", type: "text", required: true },
    { key: "value", label: "Value", type: "money" },
    { key: "insurance_due", label: "Insurance due", type: "date" },
    { key: "chassis_number", label: "Chassis number", type: "text", required: true, sensitive: true },
  ];
  it("shows money as naira, a date as a day, and leaves a masked value as it is", () => {
    expect(shownValue(fields[1] as KindField, "4500000")).toBe("₦4,500,000");
    expect(shownValue(fields[2] as KindField, "2027-03-03")).toBe("3 Mar 2027");
    expect(shownValue(fields[3] as KindField, "••3456")).toBe("••3456");
  });
  it("asks for each required field, a sensitive one kept counting", () => {
    expect(valuesProblem(fields, { plate_number: "" })).toBe("Plate number is needed.");
    expect(valuesProblem(fields, { plate_number: "LND 1", chassis_number: "" }, { chassis_number: "••3456" })).toBeNull();
  });
  it("checks a kind before it is sent", () => {
    expect(kindProblem("", fields)).toMatch(/name/);
    expect(kindProblem("Car", [])).toMatch(/at least one field/);
    expect(kindProblem("Car", [{ label: "A", type: "text" }, { label: "a", type: "text" }])).toMatch(/Two fields are called/);
    expect(kindProblem("Car", [{ label: "Fuel", type: "choice", options: ["Petrol", " "] }])).toMatch(/at least 2 choices/);
    expect(kindProblem("Car", fields)).toBeNull();
  });
});

describe("filling a form from a saved thing (§6.4)", () => {
  it("words a place as one address line, the landmark in brackets", () => {
    expect(placeAddress({ line: "12 Allen Avenue", area: "Ikeja", landmark: "By the blue gate" })).toBe("12 Allen Avenue, Ikeja (By the blue gate)");
    expect(placeAddress({ line: "12 Allen Avenue", area: "Ikeja" })).toBe("12 Allen Avenue, Ikeja");
    expect(placeAddress({ line: " 3 Bode Thomas ", area: " ", landmark: "" })).toBe("3 Bode Thomas");
    expect(placeAddress({ landmark: "Opposite the church" })).toBe("Opposite the church");
    expect(placeAddress({})).toBe("");
  });
  it("fits a long place into the limit: the landmark goes first, then the area", () => {
    const line = "12 Allen Avenue";
    const area = "Ikeja GRA, Lagos";
    expect(placeAddressFit({ line, area, landmark: "By the blue gate" }, 300)).toEqual({ address: "12 Allen Avenue, Ikeja GRA, Lagos (By the blue gate)", hint: "" });
    const long = "x".repeat(290);
    expect(placeAddressFit({ line, area, landmark: long }, 300)).toEqual({ address: "12 Allen Avenue, Ikeja GRA, Lagos", hint: "The landmark was left out to fit." });
    expect(placeAddressFit({ line, area: "y".repeat(290), landmark: "gate" }, 300)).toEqual({ address: line, hint: "The area and landmark were left out to fit." });
    const cut = placeAddressFit({ line: "z".repeat(310) }, 300);
    expect(cut.address).toHaveLength(300);
    expect(cut.hint).toMatch(/cut to 300 letters/);
  });
  it("fills the whole customer from a saved person: what it has none of is emptied", () => {
    expect(personFill({ title: "Bisi Okafor", details: { phone: "08031234567", email: "bisi@example.com" } })).toEqual({ name: "Bisi Okafor", phone: "08031234567", email: "bisi@example.com" });
    expect(personFill({ title: "Tunde", details: { phone: "08030000000" } })).toEqual({ name: "Tunde", phone: "08030000000", email: "" });
    expect(personFill({ title: "Kemi", details: { email: " " } })).toEqual({ name: "Kemi", phone: "", email: "" });
  });
  it("offers a person's or a place's phone, never a masked one", () => {
    expect(savedPhone({ kind: "person", details: { phone: " 0803 123 4567 " } })).toBe("0803 123 4567");
    expect(savedPhone({ kind: "place", details: { phone: "08037654321" } })).toBe("08037654321");
    expect(savedPhone({ kind: "person", details: { phone: "•••4567" } })).toBe("");
    expect(savedPhone({ kind: "text", details: { phone: "08030000000" } })).toBe("");
  });
  it("lists the dates saved on things, leaving out sensitive and partial ones", () => {
    const car: MadeKind = {
      id: "k1",
      name: "Car",
      count: 1,
      fields: [
        { key: "insurance_due", label: "Insurance due", type: "date" },
        { key: "bought", label: "Bought", type: "date", sensitive: true },
        { key: "plate", label: "Plate", type: "text" },
        { key: "service", label: "Service", type: "date" },
      ],
    };
    const thing = (x: Partial<SavedThing>): SavedThing => ({ ...bayo, ...x });
    const things = [
      thing({ id: "c", name: "car", title: "The car", kind: "custom", kind_id: "k1", details: {}, values: { insurance_due: "2027-03-03", bought: "••••", plate: "2027-01-01", service: "soon" } }),
      thing({ id: "m", name: "mum", title: "Mum", kind: "person", details: { birthday: "1970-03-21" } }),
      thing({ id: "t", name: "tunde", title: "Tunde", kind: "person", details: { birthday: "March" } }),
    ];
    const ds = savedDates(things, [car], new Date(2026, 10, 20));
    expect(ds.map((d) => [d.key, d.label, d.date])).toEqual([
      ["c:insurance_due", "Insurance due", "2027-03-03"],
      ["m:birthday", "Birthday", "2027-03-21"],
    ]);
    expect(nextBirthday("1970-11-20", new Date(2026, 10, 20))).toBe("2026-11-20");
    expect(nextBirthday("1960-02-29", new Date(2026, 10, 20))).toBe("2027-03-01");
    expect(ds.map(savedDateSub)[0]).toBe("@car · Insurance due · 3 Mar 2027");
    expect(savedDates(things, [])).toHaveLength(1);
  });
  it("finds a saved account's bank in a list by name, else by code", () => {
    const banks = [
      { code: "000013", name: "GTBank Plc" },
      { code: "000014", name: "Access Bank" },
    ];
    expect(bankCodeFor(banks, { bank_name: "GTBank", bank_code: "058" })).toBe("000013");
    expect(bankCodeFor(banks, { bank_name: "Somewhere", bank_code: "000014" })).toBe("000014");
    expect(bankCodeFor(banks, { bank_name: "Somewhere" })).toBe("");
  });
  it("says why a saved account's number was not filled", () => {
    expect(revealProblem({ body: { no_passkey: true } })).toBe("Add a passkey to use a saved account here.");
    expect(revealProblem(new Error("No passkey was used"))).toMatch(/No passkey was used/);
    expect(revealProblem({ name: "NotAllowedError" })).toMatch(/No passkey was used/);
    expect(revealProblem(new Error("boom"))).toMatch(/could not be filled/);
  });
});

describe("saving after an action", () => {
  it("makes a name from a title as the server does", () => {
    expect(savedNameFor("Bola Ade")).toBe("bola-ade");
    expect(savedNameFor("  Mama Put's Kitchen!! ")).toBe("mama-put-s-kitchen");
    expect(savedNameFor("Dangote Cement (Apapa) Nigeria Limited Ltd")).toBe("dangote-cement-apapa-nigeria-lim");
    expect(savedNameFor("Chíoma Ọkafor")).toBe("ch-oma-kafor");
    expect(savedNameFor("A")).toBe("");
    expect(savedNameFor("•••")).toBe("");
    expect(savedNameFor("abcdefghijklmnopqrstuvwxyzabcde f")).toBe("abcdefghijklmnopqrstuvwxyzabcde");
  });

  it("titles a typed place by its first part", () => {
    expect(placeTitleFor("12 Admiralty Way, Lekki")).toBe("12 Admiralty Way");
    expect(placeTitleFor("Shop 4 (Ikota Market)")).toBe("Shop 4");
    expect(placeTitleFor(", Lekki")).toBe("Delivery place");
  });

  it("offers a typed, new place to one who may manage saved things", () => {
    const base = { address: "12 Admiralty Way, Lekki", phone: " 0803 ", was: "", may: true };
    expect(placeOffer(base)).toEqual({ address: "12 Admiralty Way, Lekki", phone: "0803", title: "12 Admiralty Way", name: "12-admiralty-way" });
    expect(placeOffer({ ...base, may: false })).toBeNull();
    expect(placeOffer({ ...base, was: "12 Admiralty Way, Lekki" })).toBeNull();
    expect(placeOffer({ ...base, address: "  " })).toBeNull();
    // A saved place picked: not offered, unless changed after.
    expect(placeOffer({ ...base, picked: "12 Admiralty Way, Lekki" })).toBeNull();
    expect(placeOffer({ ...base, picked: "12 Admiralty Way" })?.name).toBe("12-admiralty-way");
    const offered = { address: "12 Admiralty Way, Lekki", phone: "0803", title: "12 Admiralty Way", name: "12-admiralty-way" };
    expect(placeBody(offered, "home")).toEqual({
      name: "home",
      title: "12 Admiralty Way",
      kind: "place",
      details: { line: "12 Admiralty Way, Lekki", phone: "0803" },
    });
    expect(placeBody({ ...offered, phone: "" })).toEqual({ name: "12-admiralty-way", title: "12 Admiralty Way", kind: "place", details: { line: "12 Admiralty Way, Lekki" } });
  });

  it("offers to save a new customer on their first order only", () => {
    const base = { customerOrders: 1, customerSaved: "", kind: "order", may: true, name: "Ngozi" };
    expect(customerOffer(base)).toEqual({ title: "Save Ngozi for next time", sub: "As @ngozi, with their phone and address", name: "ngozi" });
    expect(customerOffer({ ...base, name: "N•••" })).toEqual({
      title: "Save this customer for next time",
      sub: "Among your saved people, with their phone and address",
      name: "",
    });
    expect(customerOffer({ ...base, customerOrders: 2 })).toBeNull();
    expect(customerOffer({ ...base, customerOrders: undefined })).toBeNull();
    expect(customerOffer({ ...base, customerSaved: "ngozi" })).toBeNull();
    expect(customerOffer({ ...base, kind: "trade" })).toBeNull();
    expect(customerOffer({ ...base, may: false })).toBeNull();
    expect(customerOffer({ ...base, name: "" })).toBeNull();
  });
});
