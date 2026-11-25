package whatsapp

// The follow-up of a form left half-filled on WhatsApp
// (docs/spec/forms-that-sell.md §6.4, §19): one message by an approved
// template from the business's own number, never free-form, with a
// button that opens the link restoring the buyer's choice and a quick
// reply that stops these messages. Follow-ups go by text, email or
// WhatsApp: this is the WhatsApp one, a library template submitted to
// each business's own WhatsApp Business Account as the others are, and
// sent only where Meta holds it approved there with our words. Its
// category is Meta's for that account (TemplateCategory), not ours: a
// utility one reaches a buyer the form told, a marketing one only a
// buyer who asked for it on the form. Nothing in the deployment's
// environment switches it on or off (the founder, 25 November 2026).

// FollowupStopPayload is the payload of the template's Stop button.
const FollowupStopPayload = "form_followup:stop"

// FollowupLinkPath is where the template's Finish my order button opens
// on the form host: /back/<token>, which leads to the storefront's link.
const FollowupLinkPath = "/back/"

// TplFinishOrder is the follow-up's template. It is submitted as utility
// (it is about the one order the buyer started); Meta may file it as
// marketing in an account, and its category there decides who may get
// it. It is a business's own number's only, never the shared number's.
var TplFinishOrder = Template{
	Name: "taskiem_finish_order", Category: "UTILITY", OwnOnly: true,
	Body:     "[{{1}}] You started an order for {{2}} and did not finish. Tap Finish my order to finish it, or Stop to get no more messages like this.",
	Vars:     []string{"business", "what"},
	Examples: []string{"Ada Stores", "Smart Ear Cleaner (2 items, ₦45,000)"},
	Buttons: []TemplateButton{
		{Type: "url", Text: "Finish my order", Host: HostForms, Path: FollowupLinkPath + "{{1}}", Var: "link", Example: "k7m2p9q4wxk7m2p9q4wxk7m2p9q4wxab"},
		{Type: "quick_reply", Text: "Stop"},
	},
	Purpose: "The one follow-up of an order form a buyer left half-filled, from the business's own number only: {{2}} is the product and package; Finish my order opens the link that restores their choice, Stop stops these messages from the business.",
}
