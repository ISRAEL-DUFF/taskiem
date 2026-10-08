package catalogue

// ChecklistItem is one thing a reviewer confirms before approving
// (docs/connector-submissions.md, "Review checklist").
type ChecklistItem struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// Checklist is what a reviewer confirms, item by item, to approve.
var Checklist = []ChecklistItem{
	{"identity", "The publisher is who the namespace says, and the contact address answers"},
	{"classes", "Every action's class is honest: reads change nothing; idempotent writes send the engine's key where the provider deduplicates; reconcilable writes have a lookup that finds the effect; anything else is unsafe_write"},
	{"hosts", "The declared hosts belong to the provider the connector names, and nothing else is reached"},
	{"pii", "Every field holding personal data, in inputs and outputs, is declared under pii"},
	{"credentials", "Credentials go only to the provider, in the way its documentation says, and never into outputs or logs"},
	{"conformance", "The conformance cases exercise success, failure and (for writes) unknown outcomes, from recordings of the provider's real behaviour"},
	{"licence", "The licence and the attestation of original work are credible; nothing is copied from another product's connector"},
	{"docs", "Name, description, action titles and field descriptions are clear to a builder who has not read the provider's documentation"},
}

// ChecklistKeys lists the keys in order.
func ChecklistKeys() []string {
	out := make([]string, len(Checklist))
	for i, c := range Checklist {
		out[i] = c.Key
	}
	return out
}
