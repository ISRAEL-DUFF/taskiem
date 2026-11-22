import { expect, test, type Page } from "@playwright/test";
import { axe } from "./a11y";
import { doneWithStep } from "./builder";
import { confirmEmail } from "./confirm";

// Your tools (made tools M1, decision 0070): an owner makes a workflow
// started by hand a tool in the builder (Use it from chat), publishes it,
// finds it under Your tools in the chat's tools and by "/", runs it and
// sees its answer; made to always ask, it asks yes first.

// axe is injected into the page, which the Content Security Policy would refuse.
test.use({ bypassCSP: true });

const H = { headers: { "X-Taskiem-Request": "1" } };

async function signIn(page: Page) {
  await page.goto("/login?with=email");
  await page.getByLabel("Email").fill("owner@e2e.test");
  await page.getByLabel("Password").fill("correct horse battery");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
}

/** A draft workflow started by hand that gives back what it was told. */
async function draft(page: Page, name: string): Promise<string> {
  const definition = {
    schema: "wd/v1",
    id: `wf_${name.replace(/[^A-Za-z]+/g, "")}`,
    version: 1,
    name,
    trigger: { type: "manual" },
    inputs: {
      schema: {
        type: "object",
        required: ["what"],
        properties: {
          what: { type: "string", title: "What for" },
          amount: { type: "integer", title: "Amount" },
        },
      },
    },
    steps: [{ id: "note", type: "transform", config: { output: { said: "=trigger.body.what" } } }],
    outputs: [{ name: "what", type: "string", title: "What for", description: "What it was for", value: "=steps.note.output.said" }],
  };
  const r = await page.request.post("/v1/workflows", { ...H, data: { name, definition } });
  expect(r.ok(), await r.text()).toBeTruthy();
  return ((await r.json()) as { id: string }).id;
}

test("an owner makes a workflow a tool, uses it from chat, and makes it ask first", async ({ page }) => {
  test.setTimeout(120_000);
  await signIn(page);
  const id = await draft(page, "Petty cash note");

  // Use it from chat, in the builder's Settings.
  await page.goto(`/workflows/${id}`);
  await page.getByRole("tab", { name: "Settings" }).click();
  const card = page.getByTestId("use-from-chat");
  await card.getByRole("switch", { name: "Use it from chat" }).check();
  await expect(card.getByLabel("Its name")).toHaveValue("Petty cash note");
  await expect(card.getByLabel("The word to call it by")).toHaveValue("petty-cash-note");
  await expect(card).toContainText("Typed as /petty-cash-note");
  await card.getByLabel("What it is for").fill("Note what money went on");
  // A sentence, picked from what it gives back: words, then the value.
  await expect(card.getByRole("radio", { name: /A sentence/ })).toBeChecked();
  await card.getByLabel("Words, part 1").fill("Noted: ");
  await expect(card.getByLabel("Value, part 2")).toHaveValue("what");
  await expect(card).toContainText("It says: Noted: [What for]");
  expect(await axe(page)).toEqual([]);
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(page.getByText(/is published; new runs use it/)).toBeVisible();
  await expect(card.getByTestId("tool-care")).toContainText("Answers at once");

  // Your tools in the Taskiem chat: "/" and the start of its name.
  await page.goto("/chat?with=taskiem");
  const box = page.getByLabel("Message to Taskiem");
  await box.fill("/petty");
  const panel = page.getByTestId("tools-panel");
  await expect(panel.getByTestId("your-tools")).toHaveText("Your tools");
  await panel.getByRole("button", { name: /Petty cash note/ }).click();
  const sheet = page.getByTestId("made-tool-sheet");
  await expect(sheet.getByRole("heading", { name: "Petty cash note" })).toBeVisible();
  await sheet.getByLabel("What for").fill("Diesel");
  await sheet.getByLabel("Amount").fill("4500");
  expect(await axe(page)).toEqual([]);
  await sheet.getByRole("button", { name: "Go" }).click();
  // It only reads: started at once, with what it read, then its answer.
  await expect(sheet.getByLabel("What was read")).toContainText("Diesel");
  await expect(sheet.getByTestId("tool-answer")).toHaveText(/Noted: Diesel/, { timeout: 30_000 });
  await expect(sheet.getByTestId("tool-answer")).toHaveAttribute("data-status", "completed");
  expect(await axe(page)).toEqual([]);
  await sheet.getByRole("button", { name: "Close" }).click();

  // The owner makes it always ask, on its page: then it asks yes first.
  await page.goto("/tools/petty-cash-note");
  const pageSheet = page.getByTestId("made-tool-sheet");
  await expect(pageSheet).toBeVisible();
  await pageSheet.getByRole("button", { name: "Close" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Petty cash note" })).toBeVisible();
  await page.getByRole("switch", { name: "Always ask before it runs" }).check();
  await expect(page.getByText("It asks yes before it runs")).toBeVisible();
  await expect(page.getByTestId("tool-page")).toContainText("Asks yes first");
  await page.getByRole("button", { name: "Use Petty cash note" }).click();
  await pageSheet.getByLabel("What for").fill("Fuel");
  await pageSheet.getByRole("button", { name: "Next" }).click();
  await expect(pageSheet.getByLabel("What will run")).toContainText("Fuel");
  await expect(pageSheet).toContainText("Check what will run, then say yes.");
  expect(await axe(page)).toEqual([]);
  await pageSheet.getByRole("button", { name: "Yes", exact: true }).click();
  await expect(pageSheet.getByTestId("tool-answer")).toHaveText(/Noted: Fuel/, { timeout: 30_000 });

  // A taken word is said plainly: another workflow asking for the same one.
  const other = await draft(page, "Another note");
  await page.goto(`/workflows/${other}`);
  await page.getByRole("tab", { name: "Settings" }).click();
  const second = page.getByTestId("use-from-chat");
  await second.getByRole("switch", { name: "Use it from chat" }).check();
  await second.getByLabel("The word to call it by").fill("petty-cash-note");
  await page.getByRole("button", { name: "Publish" }).click();
  // What it is for is needed: said by the field's name, not the shape's.
  await expect(second.getByTestId("tool-problems")).toContainText("Say in a line what it is for");
  // The draft it saved is open again before it is changed.
  await expect(page.getByRole("combobox", { name: "Version" })).toHaveValue("2");
  await expect(second.getByLabel("What it is for")).toHaveValue("");
  await second.getByLabel("What it is for").fill("Another note");
  await page.getByRole("button", { name: "Publish" }).click();
  await expect(second.getByTestId("tool-problems")).toContainText("/petty-cash-note is already used by “Petty cash note”. Choose another word.");
});

/** Publishes a workflow (and so its tool) from its definition. */
async function publish(page: Page, definition: Record<string, unknown>): Promise<string> {
  const r = await page.request.post("/v1/workflows", { ...H, data: { name: definition.name, definition } });
  expect(r.ok(), await r.text()).toBeTruthy();
  const id = ((await r.json()) as { id: string }).id;
  const p = await page.request.post(`/v1/workflows/${id}/versions/1/publish`, H);
  expect(p.ok(), await p.text()).toBeTruthy();
  return id;
}

/** Opens one of the person's tools from the Taskiem chat by its word. */
async function openTool(page: Page, word: string, name: string) {
  await page.goto("/chat?with=taskiem");
  await page.getByLabel("Message to Taskiem").fill(`/${word}`);
  await page.getByTestId("tools-panel").getByRole("button", { name: new RegExp(name) }).click();
  const sheet = page.getByTestId("made-tool-sheet");
  await expect(sheet.getByRole("heading", { name })).toBeVisible();
  return sheet;
}

// Buttons (made tools M2): "Open tickets" lists, each row's Assign to me
// is pressed by its token and opens the assign tool filled in; the run it
// starts spends it, and the next person is told who took it.
test("a list's Assign to me opens its tool filled in, once: the next person is told who took it", async ({ page, browser }) => {
  test.setTimeout(120_000);
  await signIn(page);
  await publish(page, {
    schema: "wd/v1", id: "wf_assignTicket", version: 1, name: "Assign a ticket", trigger: { type: "manual" },
    inputs: { schema: { type: "object", required: ["ref"], properties: { ref: { type: "string", title: "Ticket" } } } },
    outputs: [{ name: "ref", type: "string", title: "Ticket", description: "The ticket", value: "=steps.take.output.ref" }],
    tool: { name: "Assign a ticket", command: "assign-ticket", about: "Take a ticket", answer: { type: "text", text: "='Yours: ' + outputs.ref" } },
    steps: [{ id: "take", type: "transform", config: { output: { ref: "=trigger.body.ref" } } }],
  });
  await publish(page, {
    schema: "wd/v1", id: "wf_openTickets", version: 1, name: "Open tickets", trigger: { type: "manual" },
    inputs: { schema: { type: "object", properties: {} } },
    outputs: [{ name: "tickets", type: "list", title: "Tickets", description: "Open ones", value: "=steps.find.output.tickets" }],
    tool: {
      name: "Open tickets", command: "open-tickets", about: "The open tickets",
      answer: {
        type: "list", from: "tickets", title: "Open tickets", item: { title: "=item.title", sub: "=item.ref" },
        actions: [{ label: "Assign to me", tool: "assign-ticket", inputs: { ref: "=item.ref" }, once: true }],
      },
    },
    steps: [{ id: "find", type: "transform", config: { output: { tickets: [{ ref: "T-1", title: "Printer jam" }, { ref: "T-2", title: "No Wi-Fi" }] } } }],
  });
  const add = await page.request.post("/v1/members", { ...H, data: { email: "tools-bo@e2e.test", password: "correct horse battery", roles: ["operator"] } });
  expect(add.ok(), await add.text()).toBeTruthy();
  confirmEmail("tools-bo@e2e.test");

  // The owner lists the tickets and takes No Wi-Fi.
  let sheet = await openTool(page, "open-tickets", "Open tickets");
  const listed = page.waitForResponse((r) => /\/v1\/runs\/[^/]+\/answer$/.test(r.url()) && r.ok());
  await sheet.getByRole("button", { name: "Go" }).click();
  await expect(sheet.getByTestId("tool-answer")).toContainText("No Wi-Fi", { timeout: 30_000 });
  const run = /\/v1\/runs\/([^/]+)\/answer$/.exec((await listed).url())?.[1] ?? "";
  await sheet.getByRole("button", { name: "See all" }).click();
  const pressed = page.waitForRequest((q) => q.url().includes("/v1/tools/buttons/") && q.method() === "POST");
  await sheet.getByRole("button", { name: /^Assign to me\s*: No Wi-Fi$/ }).click();
  const token = (await pressed).url().split("/v1/tools/buttons/")[1] ?? "";
  // The assign tool, filled in from the row.
  await expect(sheet.getByRole("heading", { name: "Assign a ticket" })).toBeVisible();
  await expect(sheet.getByLabel("Ticket")).toHaveValue("T-2");
  const started = page.waitForRequest((q) => q.url().endsWith("/v1/tools/assign-ticket/start"));
  await sheet.getByRole("button", { name: "Go" }).click();
  expect((await started).postDataJSON()).toMatchObject({ input: { ref: "T-2" }, button: token });
  await expect(sheet.getByTestId("tool-answer")).toHaveText(/Yours: T-2/, { timeout: 30_000 });
  await sheet.getByRole("button", { name: "Close" }).click();

  // Bo sees the same list (as one shared with the team would show it) and
  // presses the same row: already done, by the owner, and greyed.
  const ctx = await browser.newContext();
  const bo = await ctx.newPage();
  await bo.goto("/login?with=email");
  await bo.getByLabel("Email").fill("tools-bo@e2e.test");
  await bo.getByLabel("Password").fill("correct horse battery");
  await bo.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(bo.getByRole("heading", { level: 1, name: /^Good (morning|afternoon|evening)/ })).toBeVisible();
  await bo.route(/\/v1\/runs\/[^/]+\/answer$/, async (route) =>
    route.fulfill({ response: await route.fetch({ url: route.request().url().replace(/runs\/[^/]+\/answer$/, `runs/${run}/answer`) }) }),
  );
  sheet = await openTool(bo, "open-tickets", "Open tickets");
  await sheet.getByRole("button", { name: "Go" }).click();
  await expect(sheet.getByTestId("tool-answer")).toContainText("No Wi-Fi", { timeout: 30_000 });
  await sheet.getByRole("button", { name: "See all" }).click();
  const take = sheet.getByRole("button", { name: /^Assign to me\s*: No Wi-Fi$/ });
  await take.click();
  await expect(sheet.getByRole("status").filter({ hasText: "Already done" })).toContainText(/Already done: .+ took it at \d\d:\d\d/);
  await expect(take).toBeDisabled();
  await expect(sheet.getByRole("heading", { name: "Open tickets" })).toBeVisible();
  expect(await axe(bo)).toEqual([]);
  // The other row's button still opens the tool.
  await sheet.getByRole("button", { name: /^Assign to me\s*: Printer jam$/ }).click();
  await expect(sheet.getByLabel("Ticket")).toHaveValue("T-1");
  await ctx.close();
});

// A question inside a run (made tools M4): in the builder, a form step asks
// the person who started it and offers the business's saved places; run
// from the web's Taskiem chat, the run asks there with the place as a reply
// button, and the press (an "fq:" reply) answers it and the run goes on.
test("a run asks the person who started it in the Taskiem chat, with a saved place as a button", async ({ page }) => {
  test.setTimeout(150_000);
  // A business of its own, so its saved places are only this one.
  const signup = await page.request.post("/v1/signup", {
    ...H,
    data: { tenant: "Starter Stores", email: `starter-${Date.now()}@tools.test`, name: "Owner", password: "correct horse battery" },
  });
  expect(signup.status(), await signup.text()).toBe(201);
  const place = await page.request.post("/v1/saved", { ...H, data: { name: "ikeja", title: "Ikeja branch", kind: "place", details: { line: "12 Allen Avenue", area: "Ikeja" } } });
  expect(place.ok(), await place.text()).toBeTruthy();
  const made = await page.request.post("/v1/workflows", {
    ...H,
    data: {
      name: "Count stock",
      definition: {
        schema: "wd/v1", id: "wf_countStock", version: 1, name: "Count stock", trigger: { type: "manual" },
        inputs: { schema: { type: "object", properties: {} } },
        outputs: [{ name: "branch", type: "string", title: "Branch", description: "Where", value: "=steps.where.output.branch" }],
        tool: { name: "Count stock", command: "count-stock", about: "Count a branch's stock", answer: { type: "text", text: "='Counting at ' + outputs.branch" } },
        steps: [{ id: "where", type: "form", config: { title: "Which branch?", to: { role: "owner" }, schema: { type: "object", required: ["branch"], properties: { branch: { type: "string", title: "Branch" } } } } }],
      },
    },
  });
  expect(made.ok(), await made.text()).toBeTruthy();
  const id = ((await made.json()) as { id: string }).id;

  // The builder: who answers is the person who started it, said plainly;
  // its one text question offers the saved places.
  await page.goto(`/workflows/${id}`);
  await page.getByTestId("node-where").click();
  await page.getByLabel("Send to").selectOption({ label: "The person who started it" });
  await expect(page.getByTestId("form-step-starter")).toHaveText("Asked in the chat they started it from; else in the app, under To fill in.");
  await page.getByRole("checkbox", { name: /Offer “Our saved places” as the answers/ }).check();
  expect(await axe(page)).toEqual([]);
  await doneWithStep(page);
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.locator(".builder-saved")).toHaveText("Saved");
  const latest = (await (await page.request.get(`/v1/workflows/${id}`)).json()) as { latest_version?: number };
  const v = latest.latest_version ?? 2;
  const def = (await (await page.request.get(`/v1/workflows/${id}/versions/${v}`)).json()) as {
    definition: { steps: { config: { to: unknown; schema: { properties: Record<string, Record<string, unknown>> } } }[] };
  };
  const step = def.definition.steps[0]?.config;
  expect(step?.to).toBe("starter");
  expect(step?.schema.properties.branch?.["x-saved"]).toBe("place");
  const pub = await page.request.post(`/v1/workflows/${id}/versions/${v}/publish`, H);
  expect(pub.ok(), await pub.text()).toBeTruthy();

  // Run from the Taskiem chat: it asks a person, so yes first.
  const sheet = await openTool(page, "count-stock", "Count stock");
  await sheet.getByRole("button", { name: "Next" }).click();
  const confirmed = page.waitForResponse((r) => r.url().endsWith("/v1/tools/count-stock/confirm") && r.ok());
  await sheet.getByRole("button", { name: "Yes", exact: true }).click();
  const run = ((await (await confirmed).json()) as { run_id: string }).run_id;
  await expect(sheet.getByTestId("tool-answer")).toBeVisible({ timeout: 30_000 });
  await sheet.getByRole("button", { name: "Close" }).click();

  // Asked in the chat, the saved place a reply button; pressed, its fq: id
  // goes back as the chat's reply, and the run goes on with it.
  const chat = page.getByRole("region", { name: "Conversation with Taskiem" });
  const asked = chat.getByRole("group", { name: "Replies" }).last();
  await expect(asked.getByRole("button", { name: "Ikeja branch" })).toBeVisible({ timeout: 60_000 });
  await expect(chat).toContainText("Which branch?");
  expect(await axe(page)).toEqual([]);
  const said = page.waitForRequest((q) => q.url().endsWith("/v1/taskiem-chat") && q.method() === "POST");
  await asked.getByRole("button", { name: "Ikeja branch" }).click();
  expect((await said).postDataJSON()).toMatchObject({ reply: expect.stringMatching(/^fq:/) as unknown, text: "Ikeja branch" });
  await expect(chat).toContainText("Which branch?: Ikeja branch. Thank you.");
  // The run went on with the answer.
  await expect
    .poll(async () => ((await (await page.request.get(`/v1/runs/${run}/answer`)).json()) as { text?: string }).text, { timeout: 60_000 })
    .toBe("Counting at Ikeja branch");
});
