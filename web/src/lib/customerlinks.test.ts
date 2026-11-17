import { describe, expect, it } from "vitest";
import { appNote, appThreadLine, buttonWords, mayOffer, offerCount, ORDERS_OFF_WORDS, takesChatsHint } from "./customerlinks";

describe("customers chatting in the app, the business's side", () => {
  it("names the storefront's button choices", () => {
    expect(buttonWords("both")).toBe("Both: Chat with us and Chat on WhatsApp");
    expect(buttonWords("taskiem")).toBe("Chat with us (in Taskiem) only");
    expect(buttonWords("whatsapp")).toBe("Chat on WhatsApp only");
  });

  it("counts an offer sent, and who was skipped", () => {
    expect(offerCount(0, 0)).toBe("No customer has said yes to your offers yet.");
    expect(offerCount(1, 0)).toBe("Sent to 1 customer.");
    expect(offerCount(3, 2)).toBe("Sent to 3 customers. 2 customers skipped: they had two offers this week, or stopped messages.");
  });

  it("marks a thread in the app, typing, blocked or stopped", () => {
    expect(appThreadLine({ line: "whatsapp" })).toBe("Customer on WhatsApp");
    expect(appThreadLine({})).toBe("Customer on WhatsApp");
    expect(appThreadLine({ line: "app" })).toBe("Customer in the app");
    expect(appThreadLine({ line: "app", typing: true })).toBe("typing…");
    expect(appThreadLine({ line: "app", blocked: true })).toBe("In the app · you blocked them");
    expect(appThreadLine({ line: "app", stopped: true })).toBe("In the app · stopped messages");
  });

  it("says when a reply cannot go: no window in the app, only blocks", () => {
    expect(appNote({})).toBeNull();
    expect(appNote({ blocked: true })?.text).toBe("You blocked this customer. Unblock them to write to them.");
    expect(appNote({ stopped: true, notice: "This customer has stopped messages" })?.text).toBe("This customer has stopped messages");
    expect(appNote({ stopped: true })?.text).toBe("This customer has stopped messages.");
  });

  it("offers only to an app customer who said yes", () => {
    expect(mayOffer({ line: "app", offers: "yes" })).toBe(true);
    expect(mayOffer({ line: "app", offers: "no" })).toBe(false);
    expect(mayOffer({ line: "app", offers: "unasked" })).toBe(false);
    expect(mayOffer({ line: "whatsapp", offers: "yes" })).toBe(false);
  });

  it("says chats are closed while the Orders area is off, and how to open them", () => {
    expect(ORDERS_OFF_WORDS).toMatch(/Orders area is off, so chats are closed/);
    expect(ORDERS_OFF_WORDS).toMatch(/Turn Orders on/);
  });
});

describe("taking chats, with the business's own number (§11)", () => {
  it("says the link offers WhatsApp alone when chats in Taskiem are off and WhatsApp is still offered", () => {
    expect(takesChatsHint({ own_number: "+2348000000020", link_whatsapp: true })).toMatch(/^Off: your link offers Chat on WhatsApp only/);
    expect(takesChatsHint({ own_number: "+2348000000020" })).toMatch(/Chat on WhatsApp only/);
    expect(takesChatsHint({ own_number: "+2348000000020", link_whatsapp: false })).toBe("Off: your link says you are not taking chats. Chats already open go on.");
    expect(takesChatsHint({})).toBe("Off: your link says you are not taking chats. Chats already open go on.");
  });
});
