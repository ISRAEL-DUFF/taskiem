// Package schemas embeds the frozen JSON Schemas so every binary validates
// against the same bytes that are published.
package schemas

import _ "embed"

//go:embed wd-v1.schema.json
var WDv1 []byte

//go:embed connector-v1.schema.json
var ConnectorV1 []byte

// CapabilitiesV1 is the capability set rails declare (decision 0044): not
// a schema itself, but one input and one output schema per capability.
//
//go:embed capabilities-v1.json
var CapabilitiesV1 []byte
