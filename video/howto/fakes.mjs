// Stand-ins for the how-to videos that need WhatsApp and the model
// (video/howto/README.md): run beside the demo world, as the demo's fake
// Xenibase stands in for stock and photos.
//
//   node video/howto/fakes.mjs &
//
// - WhatsApp's Graph API at 127.0.0.1:12626: the platform number and Ada's
//   Kitchen's own number. Messages sent are accepted (and logged); the own
//   number answers who it is. Nothing leaves the machine.
// - A model at 127.0.0.1:12930 (OpenAI-compatible, TASKIEM_AI_PROVIDER=
//   selfhosted): it answers the order reader for the messages the videos
//   send, with the reading a model gives for them. Matching what was said
//   to the business's own products and prices stays Taskiem's own code.
//   Any other request is answered empty, so that feature stays quiet.
import { createServer } from "node:http";
import { appendFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { URL } from "node:url";
import { ADA, PLATFORM } from "./whatsapp.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const LOG = path.join(here, "..", "out", "howto", "fakes.log");
const log = (what, v) => appendFileSync(LOG, `${new Date().toISOString()} ${what} ${JSON.stringify(v).slice(0, 600)}\n`);


// What the reader is told for each message a video sends, by its words.
const READINGS = new Map([
  [
    "Good afternoon! Please I want 2 jollof rice, 1 peppered chicken and 2 zobo. Deliver to 14 Adeola Odeku, VI.",
    {
      orderable: true,
      lines: [
        { item: "Jollof rice", quantity: 2, choices: [] },
        { item: "Peppered chicken", quantity: 1, choices: [] },
        { item: "Zobo", quantity: 2, choices: [] },
      ],
      fulfilment: "delivery",
      address: "14 Adeola Odeku, VI",
      when: "",
      note: "",
      answers: [],
    },
  ],
]);

let sent = 0;
const graph = createServer((req, res) => {
  let body = "";
  req.on("data", (c) => (body += c));
  req.on("end", () => {
    const url = new URL(req.url ?? "/", "http://graph");
    const json = (status, v) => (res.writeHead(status, { "Content-Type": "application/json" }), res.end(JSON.stringify(v)));
    const auth = req.headers.authorization ?? "";
    if (req.method === "POST" && /\/messages$/.test(url.pathname) && (auth === `Bearer ${PLATFORM.token}` || auth === `Bearer ${ADA.token}`)) {
      const m = JSON.parse(body || "{}");
      log("whatsapp.send", { to: m.to, type: m.type, text: m.text?.body, template: m.template?.name });
      return json(200, { messaging_product: "whatsapp", contacts: [{ input: m.to, wa_id: String(m.to).replace(/^\+/, "") }], messages: [{ id: `wamid.demo${++sent}` }] });
    }
    if (auth === `Bearer ${ADA.token}` && req.method === "GET" && url.pathname === `/v25.0/${ADA.pnid}`) {
      return json(200, { display_phone_number: ADA.display, verified_name: ADA.name, id: ADA.pnid });
    }
    if (auth === `Bearer ${ADA.token}` && url.pathname === `/v25.0/${ADA.waba}/message_templates`) {
      return json(200, { data: [], paging: { cursors: { before: "a", after: "b" } } });
    }
    log("whatsapp.unknown", { method: req.method, path: url.pathname });
    res.writeHead(404).end();
  });
});

const model = createServer((req, res) => {
  let body = "";
  req.on("data", (c) => (body += c));
  req.on("end", () => {
    const answer = (content) => {
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ id: "demo", object: "chat.completion", model: "demo", choices: [{ index: 0, finish_reason: "stop", message: { role: "assistant", content } }], usage: { prompt_tokens: 0, completion_tokens: 0 } }));
    };
    let q = {};
    try {
      q = JSON.parse(body || "{}");
    } catch {
      // not JSON: answered empty below
    }
    const msgs = q.messages ?? [];
    const system = msgs.filter((m) => m.role === "system").map((m) => (typeof m.content === "string" ? m.content : JSON.stringify(m.content))).join("\n");
    const user = msgs.filter((m) => m.role === "user").map((m) => (typeof m.content === "string" ? m.content : JSON.stringify(m.content))).join("\n");
    if (system.includes("Catalogue:")) {
      const said = [...READINGS].find(([words]) => user.includes(words));
      log("model.reader", { user: user.slice(-200), known: !!said });
      return answer(JSON.stringify(said ? said[1] : { orderable: false, lines: [], fulfilment: "", address: "", when: "", note: "", answers: [] }));
    }
    log("model.other", { system: system.slice(0, 160), user: user.slice(0, 160) });
    return answer("{}");
  });
});

graph.listen(12626, "127.0.0.1", () => model.listen(12930, "127.0.0.1", () => console.log("fakes up: graph 12626, model 12930")));
