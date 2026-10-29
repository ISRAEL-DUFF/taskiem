# 0056 — The vendored CPython is allowed under the PSF licence

Date: 2026-10-28 · Status: Accepted · Amends [0007](0007-licence-policy.md)

## Context

Python code steps run CPython 3.12.0 built for WebAssembly, vendored in `engine/sandbox/pywasm` and embedded in the `taskiem` binary ([PROVENANCE.md](../../engine/sandbox/pywasm/PROVENANCE.md)). The build is Apache-2.0; CPython itself is under the PSF License Agreement (SPDX `Python-2.0`). Decision 0007 lists `Python-2.0` for TypeScript's dependency tree only. When the licence gate began scanning vendored code, it flagged the module, and the exception that kept the gate passing was marked as awaiting approval.

## Decision

`Python-2.0` is allowed for the vendored CPython in `engine/sandbox/pywasm`, and nowhere else outside TypeScript's dependency tree. Any other use still needs its own entry. The PSF licence's notice ships with it: `engine/sandbox/pywasm/LICENSE-CPython` is CPython 3.12.0's own `LICENSE`, unmodified, beside the build's `LICENSE`.

## Why

The PSF licence is permissive and GPL-compatible: it allows use, modification and redistribution in a product, asking only that the licence and copyright notice be kept with Python or any derivative version. It does not reach code that links to or embeds the interpreter, so it does not affect how Taskiem is licensed. The module is taken unmodified, runs inside the WebAssembly sandbox, and is pinned by its SHA-256.

## Alternatives

- Remove the module: Python code steps would stop working until another runtime was found, and every alternative interpreter of the same completeness is CPython under the same licence.
- Allow `Python-2.0` everywhere: broader than this one need; each further use should be looked at.

## Provenance

Our own policy. Licence identifiers are SPDX.
