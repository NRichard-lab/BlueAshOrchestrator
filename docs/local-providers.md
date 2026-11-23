# Local model endpoints

Orchestrator can discover model servers running on this machine and keep the
address of any others you name.

```bash
ao local-provider ls                                   # probe and list
ao local-provider ls --json                            # same, machine-readable
ao local-provider add <name> <base-url> [--kind ...]   # name another endpoint
ao local-provider rm <name>                            # forget one
```

## What is probed without being configured

| Name        | Address                  | Route asked     |
| ----------- | ------------------------ | --------------- |
| `ollama`    | `http://127.0.0.1:11434` | `/api/tags`     |
| `llama.cpp` | `http://127.0.0.1:8080`  | `/v1/models`    |

Both are loopback. A local engine normally binds `127.0.0.1`, and probing
anything else by default would have Orchestrator reaching across your network
without being asked. An engine that is not running is reported as unreachable
with the reason — that is the ordinary case, not an error.

## Naming another endpoint

Anything else serving models gets added by hand: a non-default port, LM Studio,
vLLM, a gateway, or a vendor's own local server.

```bash
ao local-provider add studio http://127.0.0.1:1234
ao local-provider add bionic http://127.0.0.1:9000
ao local-provider add remote-ollama http://10.0.0.5:11434 --kind ollama
```

`--kind` picks the request shape:

- `openai-compatible` (the default) asks `GET <base-url>/v1/models`.
- `ollama` asks `GET <base-url>/api/tags`.

Named endpoints are stored in `local-providers.json` under the Orchestrator data
directory (`AO_DATA_DIR`, default `~/.ao/data`). Built-ins are never written
there, and a stored endpoint pointing at a built-in's address is listed once.

### Unverified compatibility

`openai-compatible` is a de-facto interface, not a standard, and Orchestrator
has **not** verified any particular vendor against it. Discovery is honest about
this rather than claiming support:

- an endpoint that answers `/v1/models` with a model list is listed with its
  models;
- one that answers with something else is listed as unreachable, quoting what it
  actually said, which is how a wrong `--kind` or a wrong URL shows up.

**Bionic** (the LM Studio agent app) is supported only in this generic sense: it
is a name you can give an OpenAI-compatible address, typically
`http://127.0.0.1:1234` after enabling its Local Model API. Its protocol has
not been specially verified against Orchestrator, and nothing is hard-coded for
it beyond a Settings preset. If `ao local-provider ls` (or Settings → Local
models) shows it up with models, the `/v1/models` route works; if it reports
something else, that is the server's own answer and the endpoint needs a
different base URL or kind.

LAN and other non-loopback addresses are never probed until you add them. Only
use addresses on a network you trust.

## What discovery does and does not do

Discovery is read-only: it asks an endpoint what it serves and never sends a
prompt. Orchestrator does not call these endpoints to run a turn — the harness
does — so what discovery gives you is a base URL you can point a harness at,
plus confirmation that it is up and what it can run.

To make a harness use one, set the variable that harness reads, as a project
environment variable (Project settings → Environment) or in the harness's own
configuration. For example Aider reads `OLLAMA_API_BASE`, and most
OpenAI-compatible CLIs read `OPENAI_BASE_URL`; check the harness's own
documentation for the name it expects.
