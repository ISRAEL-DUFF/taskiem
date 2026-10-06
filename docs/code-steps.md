# Code steps

A `code` step runs a short function on the step's input and returns its output. JavaScript and TypeScript run in QuickJS, Python in CPython; both are compiled to WebAssembly and run in a fresh instance for every execution, so nothing is shared between runs or tenants (spec 7).

```json
{"id": "fees", "type": "code", "input": {"items": "=trigger.body.items"},
 "config": {"language": "python", "secrets": ["fx_key"], "source": "def main(input, host):\n    ..."}}
```

## The function

| Language | Entry point |
| --- | --- |
| JavaScript, TypeScript | `export default async function (input, host) { ... }` (TypeScript types are stripped when the version is saved) |
| Python | `def main(input, host):` (or `def main(input):`) |

`input` is the step's `input`, evaluated. The return value must be JSON: it becomes the step's output. In Python, `Decimal` values are returned as strings (so amounts stay exact), dates as ISO 8601, and sets and tuples as lists.

## The host

| | JavaScript | Python |
| --- | --- | --- |
| Log a line | `host.log(...)`, `console.log(...)` | `print(...)`, `host.log(...)` |
| A secret the step declares in `secrets` | `host.secret("name")` | `host.secret("name")` |
| The run's logical time (ISO 8601) | `host.now()` | `host.now()`; `time.time()` returns the same instant |
| HTTP | `await host.fetch(url, {method, headers, body})` → `{status, headers, body, json(), text()}` | `host.fetch(url, method="GET", headers=None, body="")` → `.status`, `.headers`, `.body`, `.json()`, `.text()` |

`fetch` goes through the egress guard: only hosts on the environment's allow-list (Secrets & settings → Allowed hosts), never private or metadata addresses. Responses are cut at 1 MB. Logs are kept with the step's result, up to 16 KB: do not log secrets or personal data.

## Limits

| | Default | Per step |
| --- | --- | --- |
| Time | 10 s wall clock | `limits.cpu` |
| Memory | 64 MB (JavaScript); 256 MB for Python, a cap shared by every Python instance in the worker | `limits.memory_mb` (JavaScript only) |
| Output | 256 KB of JSON | — |

A step that throws, times out, runs out of memory or returns too much fails without retrying: the same code on the same input would fail the same way. Errors give the exception and the line (`KeyError: 'rate' (line 4)`).

## Python

CPython 3.12 for WASI, with its standard library: `json`, `decimal`, `datetime`, `re`, `math`, `statistics`, `hashlib`, `hmac`, `base64`, `uuid`, `csv`, `collections`, `itertools`, `functools`, `dataclasses`, `typing`, `urllib.parse` and the rest. There is no `pip install`: third-party packages are not available. Modules that need sockets, processes, threads or native code (`socket`, `ssl`, `http`, `urllib.request`, `subprocess`, `threading`, `asyncio`, `ctypes`, `sqlite3`, …) fail on import with a clear message; use `host.fetch` for HTTP. There is no filesystem.

A Python step starts in about half a second, against a few milliseconds for JavaScript: prefer JavaScript for small, hot transformations and Python where its standard library (`decimal` for money, `csv`, `statistics`) earns its keep. The first Python step after a worker starts may wait a few seconds while the interpreter is compiled; set `TASKIEM_WASM_CACHE` to a writable directory to keep the compiled form across restarts.

The interpreter's provenance and checksum are recorded in `engine/sandbox/pywasm/PROVENANCE.md`. The boundary is WebAssembly: the interpreter gets no preopened directories, sockets or environment, whatever Python code does.
