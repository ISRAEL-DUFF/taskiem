// The demo's WhatsApp, for the how-to videos (video/howto/fakes.mjs stands
// in for Meta's Graph API): the platform number, Ada's Kitchen's own
// number, and a customer's message delivered as WhatsApp's webhook does,
// signed with the app secret the demo server is started with.
import { createHmac } from "node:crypto";

export const PLATFORM = { pnid: "106540352242922", waba: "106540352242900", token: "EAAdemo", display: "+234 800 000 0001" };
const GRAPH = "http://127.0.0.1:12626";
export const ADA = { pnid: "222000333444", waba: "333000555666", token: "EAAada", display: "+234 800 000 0010", name: "Ada's Kitchen" };
export const APP_SECRET = "demo-app-secret";
/** The app secret of Ada's own Meta app (how-to 13), 32 characters as Meta's are. */
export const ADA_APP_SECRET = "ada0kitchen0own0meta0app0secret0";

/** The environment the demo server is started with for these videos. */
export const SERVER_ENV = {
  TASKIEM_WHATSAPP_PHONE_NUMBER_ID: PLATFORM.pnid,
  TASKIEM_WHATSAPP_ACCESS_TOKEN: PLATFORM.token,
  TASKIEM_WHATSAPP_APP_SECRET: APP_SECRET,
  TASKIEM_WHATSAPP_VERIFY_TOKEN: "demo-verify",
  TASKIEM_WHATSAPP_TOKEN_KEY: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
  TASKIEM_WHATSAPP_GRAPH_URL: "http://127.0.0.1:12626/v25.0",
  TASKIEM_WHATSAPP_DISPLAY_NUMBER: PLATFORM.display,
  TASKIEM_WHATSAPP_TEMPLATES_ASSUME_APPROVED: "true",
  TASKIEM_AI_PROVIDER: "selfhosted",
  TASKIEM_AI_BASE_URL: "http://127.0.0.1:12930/v1",
  TASKIEM_AI_MODEL: "demo",
};

// A message to a number, as WhatsApp's webhook delivers it, signed.
// ownApp: the number is on the business's own Meta app, whose deliveries
// come to that number's own address, signed with that app's secret.
async function deliver(request, base, to, { from, name, message, ownApp = false }) {
  const body = JSON.stringify({
    object: "whatsapp_business_account",
    entry: [{ id: to.waba, changes: [{ field: "messages", value: {
      messaging_product: "whatsapp",
      metadata: { display_phone_number: to.display.replace(/\D/g, ""), phone_number_id: to.pnid },
      contacts: [{ profile: { name }, wa_id: from }],
      messages: [{ from, id: `wamid.in${Date.now()}${Math.random().toString(36).slice(2, 6)}`, timestamp: String(Math.floor(Date.now() / 1000)), ...message }],
    } }] }],
  });
  const sig = "sha256=" + createHmac("sha256", ownApp ? ADA_APP_SECRET : APP_SECRET).update(body).digest("hex");
  const r = await request.post(base + (ownApp ? `/channels/whatsapp/n/${to.pnid}` : "/channels/whatsapp"), { headers: { "Content-Type": "application/json", "X-Hub-Signature-256": sig }, data: body });
  if (r.status() !== 200) throw new Error(`webhook: ${r.status()} ${await r.text()}`);
}

/** A customer's text to Ada's own number (ownApp: on her own Meta app). */
export async function customerSays(request, base, { from, name, text, ownApp = false }) {
  await deliver(request, base, ADA, { from, name, ownApp, message: { type: "text", text: { body: text } } });
}

/** A person's text to Taskiem's own number (staff, owners). */
export async function says(request, base, { from, name, text }) {
  await deliver(request, base, PLATFORM, { from, name, message: { type: "text", text: { body: text } } });
}

/** A person taps a button: a template's quick reply (payload) or a message's button (id), on the number the message came from (`on`, its phone number id; Taskiem's own number by default). */
export async function taps(request, base, { from, name, payload, id, title = "", on = PLATFORM.pnid }) {
  const message = payload
    ? { type: "button", button: { payload, text: title } }
    : { type: "interactive", interactive: { type: "button_reply", button_reply: { id, title } } };
  await deliver(request, base, on === ADA.pnid ? ADA : PLATFORM, { from, name, message });
}

/** What the WhatsApp stand-in sent to a number, oldest first. */
export async function sentTo(request, number) {
  const r = await request.get(`${GRAPH}/demo/sent?to=${number}`);
  return r.json();
}

// node video/howto/whatsapp.mjs env: the server's environment, as shell exports.
if (process.argv[1] && process.argv[1].endsWith("whatsapp.mjs") && process.argv[2] === "env") {
  for (const [k, v] of Object.entries(SERVER_ENV)) console.log(`export ${k}='${v}'`);
}
