package whatsapp

import (
	"fmt"
	"regexp"
	"strings"
)

// The follow-up of a form left half-filled on WhatsApp
// (docs/spec/forms-that-sell.md §6.4, L4): one message by an approved
// template from the business's own number, never free-form, with a
// button that opens the link restoring the buyer's choice and a quick
// reply that stops these messages. Meta has not approved such a template
// yet and its category (utility or marketing) is not settled, so the
// deployment names it: with TASKIEM_WA_FOLLOWUP_TEMPLATE unset there is
// none, and every follow-up goes by text or email.

// FollowupStopPayload is the payload of the template's Stop button.
const FollowupStopPayload = "form_followup:stop"

// FollowupLinkPath is where the template's Finish my order button opens
// on the form host: /back/<token>, which leads to the storefront's link.
const FollowupLinkPath = "/back/"

var (
	templateNameRe = regexp.MustCompile(`^[a-z0-9_]{1,512}$`)
	templateLangRe = regexp.MustCompile(`^[a-z]{2,3}(_[A-Z]{2})?$`)
)

// FollowupTemplate is the follow-up's template under the name, language
// and category the deployment gives it ("" language: the deployment's).
func FollowupTemplate(name, language, category string) Template {
	return Template{
		Name: name, Category: strings.ToUpper(category), Language: language,
		Body:     "[{{1}}] You started an order for {{2}} and did not finish. Tap Finish my order to finish it, or Stop to get no more messages like this.",
		Vars:     []string{"business", "what"},
		Examples: []string{"Ada Stores", "Smart Ear Cleaner (2 items, ₦45,000)"},
		Buttons: []TemplateButton{
			{Type: "url", Text: "Finish my order", Host: HostForms, Path: FollowupLinkPath + "{{1}}", Var: "link", Example: "k7m2p9q4wxk7m2p9q4wxk7m2p9q4wxab"},
			{Type: "quick_reply", Text: "Stop"},
		},
		Purpose: "The one follow-up of an order form a buyer left half-filled, from the business's own number only: {{2}} is the product and package; Finish my order opens the link that restores their choice, Stop stops these messages from the business.",
	}
}

// FollowupTemplateFromEnv reads the follow-up's template from the
// environment: nil (off) when TASKIEM_WA_FOLLOWUP_TEMPLATE is unset. Its
// category (TASKIEM_WA_FOLLOWUP_TEMPLATE_CATEGORY, utility or marketing)
// must be given with it, since a marketing one reaches only a buyer who
// asked for it on the form; its language
// (TASKIEM_WA_FOLLOWUP_TEMPLATE_LANGUAGE) is the deployment's when unset.
func FollowupTemplateFromEnv(lookup func(string) (string, bool)) (*Template, error) {
	get := func(k string) string {
		v, _ := lookup(k)
		return strings.TrimSpace(v)
	}
	name := get("TASKIEM_WA_FOLLOWUP_TEMPLATE")
	if name == "" {
		return nil, nil
	}
	if !templateNameRe.MatchString(name) {
		return nil, fmt.Errorf("TASKIEM_WA_FOLLOWUP_TEMPLATE: %q is not a template name (lowercase letters, digits and _)", name)
	}
	if _, ok := TemplateNamed(name); ok {
		return nil, fmt.Errorf("TASKIEM_WA_FOLLOWUP_TEMPLATE: %q is a library template; name the follow-up's own", name)
	}
	category := strings.ToLower(get("TASKIEM_WA_FOLLOWUP_TEMPLATE_CATEGORY"))
	if category != "utility" && category != "marketing" {
		return nil, fmt.Errorf("TASKIEM_WA_FOLLOWUP_TEMPLATE_CATEGORY must be utility or marketing (the category Meta approved %s in), not %q", name, category)
	}
	language := get("TASKIEM_WA_FOLLOWUP_TEMPLATE_LANGUAGE")
	if language != "" && !templateLangRe.MatchString(language) {
		return nil, fmt.Errorf("TASKIEM_WA_FOLLOWUP_TEMPLATE_LANGUAGE: %q is not a WhatsApp language code (en, en_US, ha)", language)
	}
	t := FollowupTemplate(name, language, category)
	return &t, nil
}

// OptIn reports whether a template reaches only people who asked for it:
// a marketing one (docs/spec/taskiem-for-everyone.md, offers and consent).
func (t Template) OptIn() bool { return Category(t) == "marketing" }
