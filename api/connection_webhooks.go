package api

import (
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
		path := base + "/" + tenant.String() + "/connectors/" + url.PathEscape(c.Ref()) + "/" + url.PathEscape(name)
		q := url.Values{"env": {"prod"}, "connection": {connection}}
		var scheme, field string
		if spec.Verify != nil {
			scheme, field = spec.Verify.Scheme, spec.Verify.SecretField
		}
		label := fieldLabel(c.Manifest, field)
		switch scheme {
		case "path_secret":
			h.Placeholder = "{" + field + "}"
			h.URL = path + "/prod/" + url.PathEscape(connection) + "/" + h.Placeholder
		case "query_secret":
			h.Placeholder = "{" + field + "}"
			h.URL = path + "?" + q.Encode() + "&" + url.QueryEscape(spec.Verify.Query) + "=" + h.Placeholder
		default:
			h.URL = path + "?" + q.Encode()
		}
		h.Verification = howVerified(c.Manifest.Name, scheme, label, h.Placeholder)
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
