// Package examples embeds the example WebAssembly connector, which
// `taskiem connector init` copies to start a new connector
// (docs/connector-sdk.md).
package examples

import "embed"

// WasmConnector is examples/wasm-connector: its handlers, native test,
// manifest, fixtures and conformance cases.
//
//go:embed wasm-connector/main.go wasm-connector/main_test.go wasm-connector/manifest.yaml wasm-connector/testdata
var WasmConnector embed.FS

// WasmConnectorID is the example's connector id, replaced when it is
// scaffolded.
const WasmConnectorID = "x_example_ledger"
