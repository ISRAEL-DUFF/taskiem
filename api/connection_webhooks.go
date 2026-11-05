package api

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/israel-duff/taskiem/engine/connector"
)

// moneyWebhook is an address a business sets in a rail's dashboard so that
// Taskiem hears the connection's money events, workflow or not: the money
// log records every one heard in prod (decision 0045; spec 7.3). The
// Connections page shows it on the connection's row.
type moneyWebhook struct {
	Trigger string `json:"trigger"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	// Events are the normalised events it carries (payment.received).
	Events []string `json:"events"`
	// Verification is how Taskiem tells the provider's deliveries from
	// anyone else's, in plain words; it names the connection field, never
	// its value.
	Verification string `json:"verification"`
	// Placeholder, when set, is the part of URL the business replaces with
	// a connection field Taskiem does not show again (path_secret,
	// query_secret): the address carries a secret.
	Placeholder string `json:"placeholder,omitempty"`
	// Where is where in the provider's dashboard it is pasted.
	Where string `json:"where"`
	// PollMinutes: Taskiem also asks the provider this often, so the
	// address is optional (no-technical-steps N4).
	PollMinutes int `json:"poll_minutes,omitempty"`
}

// hooksBase is where providers reach /hooks.
func (s *Server) hooksBase() string {
	if s.HooksURL != "" {
		return strings.TrimRight(s.HooksURL, "/")
	}
	if s.PublicURL != "" {
		return strings.TrimRight(s.PublicURL, "/") + "/hooks"
	}
	return "/hooks"
}

// latestConnector is the newest major of the connector a connection names
// (connections store the id), as the money log's poller picks it.
func latestConnector(reg connector.Lookup, id string) *connector.Connector {
	var best *connector.Connector
	bestMajor := -1
	for _, c := range reg.List() {
		if c.Manifest.ID != id {
			continue
		}
		_, m, _ := strings.Cut(c.Ref(), "@")
		major, err := strconv.Atoi(m)
		if err != nil {
			major = 0
		}
		if major > bestMajor {
			best, bestMajor = c, major
		}
	}
	return best
}

// moneyWebhooks are the addresses for one prod connection of c: one per
// trigger its events come from, except triggers Taskiem registers at the
// provider itself. A connector without events has none.
func moneyWebhooks(c *connector.Connector, base string, tenant uuid.UUID, connection string) []moneyWebhook {
	if c == nil || len(c.Manifest.Events) == 0 {
		return nil
	}
	events := map[string][]string{}
	for name, ev := range c.Manifest.Events {
		events[ev.Trigger] = append(events[ev.Trigger], name)
	}
	triggers := make([]string, 0, len(events))
	for t := range events {
		triggers = append(triggers, t)
	}
	slices.Sort(triggers)
	var out []moneyWebhook
	for _, name := range triggers {
		spec, ok := c.Manifest.Triggers[name]
		if !ok || spec.Type != "webhook" || spec.Registered() {
			continue
		}
		slices.Sort(events[name])
		h := moneyWebhook{Trigger: name, Title: spec.Title, Events: events[name], Where: spec.Where}
		if h.Title == "" {
			h.Title = humanName(name)
			h.Title = strings.ToUpper(h.Title[:1]) + h.Title[1:]
		}
		if h.Where == "" {
			h.Where = "In " + c.Manifest.Name + "'s dashboard, under webhooks."
		}
		if spec.Poll != nil {
			h.PollMinutes = int(spec.Poll.Interval().Minutes())
		}
		var scheme, field string
		if spec.Verify != nil {
			scheme, field = spec.Verify.Scheme, spec.Verify.SecretField
		}
		h.URL, h.Placeholder = triggerURL(c, base, tenant, name, spec, "prod", connection, "")
		h.Verification = howVerified(c.Manifest.Name, scheme, fieldLabel(c.Manifest, field), h.Placeholder)
		out = append(out, h)
	}
	return out
}

// triggerURL is the address a provider delivers a connection's trigger
// to. For a trigger verified by a secret in the URL (path_secret,
// query_secret), secret is put in it when given; otherwise the address
// carries placeholder, the part the business replaces with it.
func triggerURL(c *connector.Connector, base string, tenant uuid.UUID, name string, spec connector.TriggerSpec, env, connection, secret string) (addr, placeholder string) {
	path := base + "/" + tenant.String() + "/connectors/" + url.PathEscape(c.Ref()) + "/" + url.PathEscape(name)
	q := url.Values{"env": {env}, "connection": {connection}}
	if spec.Verify == nil {
		return path + "?" + q.Encode(), ""
	}
	tok := secret
	if tok == "" {
		placeholder = "{" + spec.Verify.SecretField + "}"
		tok = placeholder
	}
	switch spec.Verify.Scheme {
	case "path_secret":
		return path + "/" + url.PathEscape(env) + "/" + url.PathEscape(connection) + "/" + tok, placeholder
	case "query_secret":
		return path + "?" + q.Encode() + "&" + url.QueryEscape(spec.Verify.Query) + "=" + tok, placeholder
	}
	return path + "?" + q.Encode(), ""
}

// connectionWebhook is a webhook address that carries a secret Taskiem
// made for a connection (generate: webhook_token), shown once, when the
// connection is saved, for the business to paste into the provider's
// dashboard. Taskiem does not show it again: to get a new one, connect
// again.
type connectionWebhook struct {
	Trigger string `json:"trigger"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Where   string `json:"where"`
}

// refuseMadeFields refuses credentials that name a field Taskiem makes
// itself (generate: webhook_token): nobody chooses, sets or clears its
// value, so a guessable one cannot be put in its place.
func refuseMadeFields(c *connector.Connector, creds map[string]string) error {
	for _, k := range c.Manifest.WebhookTokenFields() {
		if _, ok := creds[k]; ok {
			return fmt.Errorf("%w: Taskiem makes %s itself when the connection is saved; leave it out", errBadRequest, k)
		}
	}
	return nil
}

// withMadeFields is creds with a fresh random token in each field Taskiem
// makes (generate: webhook_token), and those tokens on their own.
func withMadeFields(c *connector.Connector, creds map[string]string) (map[string]string, map[string]string, error) {
	keys := c.Manifest.WebhookTokenFields()
	if len(keys) == 0 {
		return creds, nil, nil
	}
	out := make(map[string]string, len(creds)+len(keys))
	for k, v := range creds {
		out[k] = v
	}
	made := map[string]string{}
	for _, k := range keys {
		tok, err := connector.NewWebhookToken()
		if err != nil {
			return nil, nil, err
		}
		out[k], made[k] = tok, tok
	}
	return out, made, nil
}

// connectionWebhooks are the addresses, with their tokens, of c's triggers
// verified by a token Taskiem made (made, by field), for a connection
// just saved.
func connectionWebhooks(c *connector.Connector, base string, tenant uuid.UUID, env, connection string, made map[string]string) []connectionWebhook {
	if len(made) == 0 {
		return nil
	}
	var out []connectionWebhook
	for _, name := range slices.Sorted(maps.Keys(c.Manifest.Triggers)) {
		spec := c.Manifest.Triggers[name]
		if spec.Type != "webhook" || spec.Verify == nil || (spec.Verify.Scheme != "query_secret" && spec.Verify.Scheme != "path_secret") {
			continue
		}
		tok := made[spec.Verify.SecretField]
		if tok == "" {
			continue
		}
		h := connectionWebhook{Trigger: name, Title: spec.Title, Where: spec.Where}
		if h.Title == "" {
			h.Title = humanName(name)
			h.Title = strings.ToUpper(h.Title[:1]) + h.Title[1:]
		}
		if h.Where == "" {
			h.Where = "In " + c.Manifest.Name + "'s dashboard, under webhooks."
		}
		h.URL, _ = triggerURL(c, base, tenant, name, spec, env, connection, tok)
		out = append(out, h)
	}
	return out
}

// fieldLabel is the name the person knows a connection field by.
func fieldLabel(m *connector.Manifest, key string) string {
	for _, f := range m.Auth.Fields {
		if f.Key == key && f.Label != "" {
			return f.Label
		}
	}
	return key
}

// howVerified says, in plain words, how a trigger's deliveries are checked.
func howVerified(service, scheme, label, placeholder string) string {
	switch scheme {
	case "hmac_sha256", "hmac_sha512", "hmac_sha1", "hmac_sha256_timestamped", "slack_v0":
		return service + " signs each delivery with this connection's " + label + "; Taskiem refuses any delivery whose signature does not match."
	case "header_secret", "bearer", "basic":
		return service + " sends this connection's " + label + " with each delivery; Taskiem refuses any delivery without it."
	case "path_secret", "query_secret":
		return "The address carries this connection's " + label + ", which Taskiem does not show again: replace " + placeholder + " with it. Taskiem refuses any delivery without it."
	case "connector":
		return "Taskiem checks each delivery with this connection's " + label + ", as " + service + " documents, and refuses any other."
	case "none", "":
		return service + " does not sign its deliveries."
	}
	return "Taskiem checks each delivery as " + service + " documents."
}
