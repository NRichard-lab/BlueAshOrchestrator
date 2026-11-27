# Local model endpoints

Blue Ash AI can discover model servers running on this machine, keep the
address of any others you name, and **link** one endpoint + model to the
**Local** coding harness so it appears beside Claude and Cursor in project and
session pickers.

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
anything else by default would have Blue Ash AI reaching across your network
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

Named endpoints are stored in `local-providers.json` under the Blue Ash AI data
directory (`AO_DATA_DIR`, default `~/.ao/data`). Built-ins are never written
there, and a stored endpoint pointing at a built-in's address is listed once.

### Unverified compatibility

`openai-compatible` is a de-facto interface, not a standard, and Blue Ash AI
has **not** verified any particular vendor against it. Discovery is honest about
this rather than claiming support:

- an endpoint that answers `/v1/models` with a model list is listed with its
  models;
- one that answers with something else is listed as unreachable, quoting what it
  actually said, which is how a wrong `--kind` or a wrong URL shows up.

**Bionic** (the LM Studio agent app) is supported only in this generic sense: it
is a name you can give an OpenAI-compatible address, typically
`http://127.0.0.1:1234` after enabling its Local Model API. Its protocol has
not been specially verified against Blue Ash AI, and nothing is hard-coded for
it beyond a Settings preset. If `ao local-provider ls` (or Settings → Local
models) shows it up with models, the `/v1/models` route works; if it reports
something else, that is the server's own answer and the endpoint needs a
different base URL or kind.

LAN and other non-loopback addresses are never probed until you add them. Only
use addresses on a network you trust.

## Local coding harness

Discovery alone does not generate tokens. To use a local endpoint as a **worker**:

1. Install **OpenCode 2** (Local reuses its workspace tools and permission modes;
   Blue Ash AI does not ship a second tool runtime).
2. In Settings → Local models, choose an endpoint and a discovered (or supported
   manual) model, then **Link Local worker**. An optional API key is stored only
   in `local-harness.json` under the data directory (mode `0600` on Unix) and is
   never returned by the list API or written to logs.
3. Select **Local** in a project worker picker or new-session picker, the same
   way you select Claude or Cursor.

At launch, Blue Ash AI injects an OpenAI-compatible provider (`ao-local`) into
OpenCode’s config with the linked `baseURL` and model. Normal coding turns then
call that endpoint’s `/v1/chat/completions` (including tool calls). Project
permission modes (`default`, `accept-edits`, `auto`, `bypass-permissions`) map to
OpenCode’s existing controls.

Capabilities, stated honestly:

- **Requires** OpenCode 2 on PATH (or a managed install AO can resolve).
- **Requires** a linked endpoint + model; until linked, Local is not authorized
  for launch.
- **Tools** are OpenCode’s workspace-scoped tools under AO’s permission modes.
- **Compatibility** of a given Ollama / llama.cpp / LM Studio / Bionic build is
  whatever that server’s OpenAI-compatible surface actually implements.

## What discovery does and does not do

Discovery is read-only: it asks an endpoint what it serves and never sends a
prompt. The only HTTP requests Blue Ash AI makes during discovery are:

| Kind | Probe |
| --- | --- |
| `ollama` | `GET <base-url>/api/tags` |
| `openai-compatible` | `GET <base-url>/v1/models` |

Generation for the Local harness is a separate path: OpenCode (launched by AO)
calls `/v1/chat/completions` on the linked endpoint. Other harnesses may still
read `OLLAMA_API_BASE` / `OPENAI_BASE_URL` if you copy those from Settings; that
env-copy path remains available but is not required for Local once linked.
