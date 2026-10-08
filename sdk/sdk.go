// Package sdk embeds the TypeScript SDK (@taskiem/sdk) so the taskiem binary
// can compile .flow.ts files to definitions and generate code from them
// without Node (spec 10.1, 10.2).
package sdk

import "embed"

//go:generate go run ../tools/sdkgen src/connectors.gen.ts

// Source holds the SDK's TypeScript sources (tests excluded).
//
//go:embed src/*.ts
var Source embed.FS
