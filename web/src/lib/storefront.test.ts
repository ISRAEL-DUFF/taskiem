import { describe, expect, it } from "vitest";
import {
  cleanColumns,
  formFrom,
  requestFrom,
  sameRequest,
  tidyMessage,
  columnsFor,
  catalogueTables,
  formatPrice,
  guessColumns,
  hoursErrors,
  hoursFromForm,
  hoursToForm,
  imageProblem,
  pastMidnight,
  sameAsMonday,
  slugError,
  slugify,
  thumb,
  type Hours,
} from "./storefront";
import type { Column, Table } from "./tables";

/** a[i], which the test knows is there. */
function at<T>(a: T[] | undefined, i: number): T {
  const v = a?.[i];
  if (v === undefined) throw new Error(`no element ${i}`);
  return v;
}

const col = (name: string, type: Column["type"], personal = false): Column => ({ name, title: "", type, required: false, personal });

const table = (columns: Column[]): Table => ({
  id: "t1",
  name: "menu",
  title: "Menu",
  description: "",
  columns,
  postgres_table: "menu",
  connection: "data_tables",
  created_by: "",
  created_at: "",
  updated_at: "",
});

describe("prices", () => {
  it("shows kobo as naira", () => {
    expect(formatPrice(150000)).toBe("₦1,500");
    expect(formatPrice(150050)).toBe("₦1,500.50");
    expect(formatPrice(150005)).toBe("₦1,500.05");
    expect(formatPrice(0)).toBe("₦0");
    expect(formatPrice(99)).toBe("₦0.99");
    expect(formatPrice(123456789)).toBe("₦1,234,567.89");
    expect(formatPrice(-2000)).toBe("-₦20");
  });
});

describe("addresses", () => {
  it("suggests a slug from a name", () => {
    expect(slugify("Mama Put's Kitchen")).toBe("mama-put-s-kitchen");
    expect(slugify("  Adé & Sons Ltd. ")).toBe("ade-and-sons-ltd");
    expect(slugify("A".repeat(29) + " bc")).toBe("a".repeat(29));
    expect(slugify("")).toBe("");
  });
  it("checks a slug's shape", () => {
    expect(slugError("mama-put")).toBeNull();
    for (const bad of ["", "ab", "Mama", "a_b_c", "-abc", "abc-", "a--b", "a".repeat(31)]) expect(slugError(bad), bad).not.toBeNull();
  });
});

describe("opening hours", () => {
  const api: Hours = {
    days: [[{ open: "08:00", close: "18:30" }], [{ open: "08:00", close: "12:00" }, { open: "13:00", close: "18:00" }], null, [], null, null, null],
    note: "Closed on public holidays",
  };

  it("turns the API's hours into seven days and back", () => {
    const f = hoursToForm(api);
    expect(f.days).toHaveLength(7);
    expect(at(f.days, 0)).toEqual({ open: true, spans: [{ open: "08:00", close: "18:30" }] });
    expect(at(f.days, 1).spans).toHaveLength(2);
    expect(at(f.days, 2).open).toBe(false);
    expect(at(f.days, 3).open).toBe(false);
    expect(f.note).toBe("Closed on public holidays");
    expect(hoursFromForm(f)).toEqual({ ...api, days: api.days.map((d) => (d && d.length ? d : null)) });
  });

  it("fills in missing or short hours", () => {
    const f = hoursToForm(undefined);
    expect(f.days.every((d) => !d.open && d.spans.length === 1)).toBe(true);
    expect(hoursFromForm(f)).toEqual({ days: [null, null, null, null, null, null, null] });
    expect(hoursToForm({ days: [[{ open: "09:00", close: "10:00" }]] }).days).toHaveLength(7);
  });

  it("drops the openings of a closed day and trims the note", () => {
    const f = hoursToForm(api);
    at(f.days, 0).open = false;
    f.note = "  ";
    const h = hoursFromForm(f);
    expect(h.days[0]).toBeNull();
    expect(h.note).toBeUndefined();
  });

  it("copies Monday", () => {
    const f = sameAsMonday(hoursToForm(api), 4);
    expect(at(f.days, 4)).toEqual(at(f.days, 0));
    expect(at(f.days, 4).spans).not.toBe(at(f.days, 0).spans);
    expect(at(f.days, 1).spans).toHaveLength(2);
  });

  it("reports bad hours", () => {
    const f = hoursToForm(api);
    expect(hoursErrors(f)).toEqual([]);
    at(at(f.days, 0).spans, 0).close = "08:00";
    at(at(f.days, 1).spans, 1).open = "";
    f.note = "x".repeat(121);
    expect(hoursErrors(f).map((e) => e.day)).toEqual([0, 1, -1]);
    // A closed day's openings are not checked.
    at(f.days, 0).open = false;
    expect(hoursErrors(f).map((e) => e.day)).toEqual([1, -1]);
  });

  it("knows an opening that runs past midnight", () => {
    expect(pastMidnight({ open: "20:00", close: "02:00" })).toBe(true);
    expect(pastMidnight({ open: "08:00", close: "18:00" })).toBe(false);
  });
});

describe("catalogue columns", () => {
  const cols = [
    col("name", "text"),
    col("owner_name", "text", true),
    col("price_kobo", "integer"),
    col("cost", "number"),
    col("in_stock", "boolean"),
    col("phone", "phone", true),
    col("notes", "json"),
    col("image", "text"),
  ];

  it("offers only columns of allowed types that are not personal", () => {
    expect(columnsFor(cols, ["text"]).map((c) => c.name)).toEqual(["name", "image"]);
    expect(columnsFor(cols, ["integer", "number"]).map((c) => c.name)).toEqual(["price_kobo", "cost"]);
    expect(columnsFor(cols, ["boolean"]).map((c) => c.name)).toEqual(["in_stock"]);
    expect(columnsFor([col("secret", "boolean", true)], ["boolean"])).toEqual([]);
  });

  it("lists the tables that can be a catalogue", () => {
    expect(catalogueTables([table(cols), table([col("who", "text", true), col("n", "integer")])])).toHaveLength(1);
  });

  it("guesses a mapping from column names", () => {
    expect(guessColumns(table(cols))).toEqual({ name: "name", price: "price_kobo", price_unit: "kobo", photo: "image", available: "in_stock" });
    expect(guessColumns(table([col("dish", "text"), col("price", "number")]))).toEqual({ name: "dish", price: "price", price_unit: "naira" });
  });

  it("leaves out empty fields", () => {
    expect(cleanColumns({ name: "name", price: "", price_unit: "kobo", photo: "image" })).toEqual({ name: "name", photo: "image" });
    expect(cleanColumns({ name: "name", price: "cost", price_unit: "" })).toEqual({ name: "name", price: "cost", price_unit: "naira" });
  });
});

describe("images", () => {
  it("checks type and size before uploading", () => {
    expect(imageProblem({ type: "image/png", size: 1000 })).toBeNull();
    expect(imageProblem({ type: "image/gif", size: 1000 })).toMatch(/JPEG, PNG or WebP/);
    expect(imageProblem({ type: "image/jpeg", size: 6 << 20 })).toMatch(/5 MB/);
  });
  it("picks the thumbnail", () => {
    expect(thumb({ "2000": "big", "320": "small" })).toBe("small");
    expect(thumb(undefined)).toBeUndefined();
  });
});

describe("the editor's form", () => {
  it("starts a new storefront empty", () => {
    const f = formFrom(null);
    expect(requestFrom(f)).toEqual({
      slug: "",
      draft: { name: "", hours: { days: [null, null, null, null, null, null, null] }, template: "classic", accent: "green" },
      catalogue_table: null,
      catalogue_columns: {},
    });
  });

  it("saves what it loaded unchanged, and notices a change", () => {
    const sf = {
      slug: "mama-put",
      draft: { name: "Mama Put", tagline: "Hot food", hours: { days: [[{ open: "08:00", close: "18:00" }], null, null, null, null, null, null] }, template: "bold" as const, accent: "red", language: "en" },
      catalogue_table: "t1",
      catalogue_columns: { name: "name", price: "price", price_unit: "naira" as const },
    };
    const f = formFrom(sf);
    expect(requestFrom(f)).toEqual(sf);
    expect(sameRequest(f, formFrom(sf))).toBe(true);
    expect(sameRequest({ ...f, text: { ...f.text, about: "  " } }, f)).toBe(true);
    expect(sameRequest({ ...f, text: { ...f.text, about: "Since 1999" } }, f)).toBe(false);
    // No table: no mapping.
    expect(requestFrom({ ...f, table: null }).catalogue_columns).toEqual({});
  });

  it("tidies the server's refusals", () => {
    expect(tidyMessage("storefront: invalid: the name: too short")).toBe("The name: too short");
    expect(tidyMessage("storefront: the address is taken: mama")).toBe("The address is taken: mama");
    expect(tidyMessage('storefront: invalid: publicnames: reserved name: "www"')).toBe('Reserved name: "www"');
    expect(tidyMessage("an image is at most 5 MB")).toBe("An image is at most 5 MB");
  });
});
