// The demo's WhatsApp, for the how-to videos (video/howto/fakes.mjs stands
// in for Meta's Graph API): the platform number, Ada's Kitchen's own
// number, and a customer's message delivered as WhatsApp's webhook does,
// signed with the app secret the demo server is started with.
import { createHmac } from "node:crypto";

export const PLATFORM = { pnid: "106540352242922", token: "EAAdemo" };
export const ADA = { pnid: "222000333444", waba: "333000555666", token: "EAAada", display: "+234 800 000 0010", name: "Ada's Kitchen" };
export const APP_SECRET = "demo-app-secret";

/** The environment the demo server is started with for these videos. */
export const SERVER_ENV = {
  TASKIEM_WHATSAPP_PHONE_NUMBER_ID: PLATFORM.pnid,
  TASKIEM_WHATSAPP_ACCESS_TOKEN: PLATFORM.token,
  TASKIEM_WHATSAPP_APP_SECRET: APP_SECRET,
  TASKIEM_WHATSAPP_VERIFY_TOKEN: "demo-verify",
  TASKIEM_WHATSAPP_TOKEN_KEY: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
  TASKIEM_WHATSAPP_GRAPH_URL: "http://127.0.0.1:12626/v25.0",
  TASKIEM_WHATSAPP_DISPLAY_NUMBER: "+234 800 000 0001",
  TASKIEM_WHATSAPP_TEMPLATES_ASSUME_APPROVED: "true",
  TASKIEM_AI_PROVIDER: "selfhosted",
  TASKIEM_AI_BASE_URL: "http://127.0.0.1:12930/v1",
  TASKIEM_AI_MODEL: "demo",
};

/** A customer's text to Ada's own number, as WhatsApp's webhook sends it. */
export async function customerSays(request, base, { from, name, text }) {
  const body = JSON.stringify({
    object: "whatsapp_business_account",
    entry: [{ id: ADA.waba, changes: [{ field: "messages", value: {
      messaging_product: "whatsapp",
      metadata: { display_phone_number: ADA.display.replace(/\D/g, ""), phone_number_id: ADA.pnid },
      contacts: [{ profile: { name }, wa_id: from }],
      messages: [{ from, id: `wamid.cust${Date.now()}`, timestamp: String(Math.floor(Date.now() / 1000)), type: "text", text: { body: text } }],
    } }] }],
  });
  const sig = "sha256=" + createHmac("sha256", APP_SECRET).update(body).digest("hex");
  const r = await request.post(base + "/channels/whatsapp", { headers: { "Content-Type": "application/json", "X-Hub-Signature-256": sig }, data: body });
  if (r.status() !== 200) throw new Error(`webhook: ${r.status()} ${await r.text()}`);
}

// node video/howto/whatsapp.mjs env: the server's environment, as shell exports.
if (process.argv[1] && process.argv[1].endsWith("whatsapp.mjs") && process.argv[2] === "env") {
  for (const [k, v] of Object.entries(SERVER_ENV)) console.log(`export ${k}='${v}'`);
}
