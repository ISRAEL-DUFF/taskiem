# CPython for WebAssembly (WASI)

`python.wasm.gz` is CPython 3.12.0 built for `wasm32-wasi` by VMware Labs
([webassembly-language-runtimes](https://github.com/vmware-labs/webassembly-language-runtimes)),
with the standard library embedded in the module. It reports
`3.12.0 (tags/v3.12.0:0fb18b0, Dec 11 2023, 11:45:20) [Clang 16.0.0 ]`. It was taken unmodified from the npm package
[`@antonz/python-wasi@3.12.0`](https://www.npmjs.com/package/@antonz/python-wasi)
(`dist/python.wasm`; tarball SHA-1 `b8bde4f8a0131c10870c4eefb059affd5221a76b`,
as the registry lists it) and compressed with `gzip -9 -n`.

| | |
| --- | --- |
| `python.wasm` SHA-256 | `e5dc5a398b07b54ea8fdb503bf68fb583d533f10ec3f930963e02b9505f7a763` |
| Size | 26,267,204 bytes |
| Licences | Apache-2.0 (the build, see `LICENSE`); CPython under the PSF License Agreement |

`pywasm.Module` refuses a module whose SHA-256 differs. Before a production
release, compare the hash with the matching asset of VMware Labs' Python
3.12.0 release (not reachable from the session that vendored it), and
record the result here.

The module imports only `wasi_snapshot_preview1`. Taskiem gives it no
preopened directories, no sockets, and no environment, so its socket and
file functions have nothing to reach; the WebAssembly sandbox is the
security boundary, not the interpreter.
