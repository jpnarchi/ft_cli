# ft — Forethought CLI

A command-line tool for **your own** Forethought account. It does what you'd do
in the Agent Builder UI: read and audit agents, edit email policies and
settings, edit coaching and actions, run email previews and read their logs,
and publish or switch channels when a change is approved.

> **LLMs and agents:** read [AGENTS.md](AGENTS.md) before running anything. It
> says which commands need a human's approval, gives the standard workflows,
> and lists FT behaviours that aren't obvious from the UI.
> [SPEC.md](SPEC.md) has each command's status and which endpoints are
> confirmed live.

## Safety model

| Kind | How it's gated | Examples |
|---|---|---|
| **Read** | free | `agents list`, `agent get`, `coaching list`, `logs get` |
| **Draft write** | diff first; nothing is written without `--apply`; backup before writing | `agent set-policy`, `agent set-setting`, `coaching update`, `action create` |
| **Live change** | diff first; nothing happens without `--confirm`; backup before writing | `publish`, `channel on\|off`, `coaching publish`, `action update`, `agent set-intent` |
| **Preview** | runs the agent's **real** actions; asks for a typed `yes` when a tool could change something | `preview` |

- **Backups:** `~/ft_backup_YYYYMMDD/<name>__<kind>__<time>.json` is written
  before every write. Secrets in them are redacted.
- **Never live directly:** draft writes don't touch what contributors see.
  Email changes go live only through `publish` (or the Journey tab's Publish
  button in the UI).
- **No secrets shown or stored:** credentials in actions and tools (API-key
  headers, tokens) show as `[redacted]` in all output, in `--out` files, in
  backups and in snapshots. On update, the CLI restores the real value from
  FT. New credentials go in as `${env:NAME}` and are read from your
  environment only when the request is sent.
- **Your password never leaves your machine.** `ft login` uses AWS Cognito SRP,
  which sends only a zero-knowledge proof, then caches a token that refreshes
  itself (`~/.ft_cli_token.json`, mode 600).

## Setup

Create `~/.ft_cli_creds` with your own values (never commit it):

```
FT_API_BASE=https://your-api.example.com
FT_SUBDOMAIN=your-org
FT_EMAIL=you@example.com
FT_COGNITO_CLIENT_ID=...
FT_COGNITO_REGION=...
FT_COGNITO_POOL_ID=...
```

```bash
chmod 600 ~/.ft_cli_creds
```

```bash
go build -o ft .
```

```bash
./ft login
```

`login` asks for your password without showing it. Then check it works:

```bash
./ft agents list
```

`FT_API_BASE` (the dashboard API host), `FT_EMAIL` and the three `FT_COGNITO_*`
values are required; `FT_SUBDOMAIN` names your org; `FT_PASSWORD` is optional
(if it's missing you're prompted).

## Naming things

- **Agents** (`A`): exact name, intent id, or any unique part of the name,
  case-insensitive. `"refund"` finds "Refund Request". If the text matches
  more than one agent, the error lists them.
- **Coaching** (`C`): id, exact name, or unique part of the name. Two
  instructions can share a name; then use the id from `coaching list`.
- **Actions**: id, exact name, or unique part of the name.
- **Channels** (`--channel` / `-c`): `email` (the default), `widget`, `slack`,
  `api`, `voice`, `web_call`, `playbook`.

## Commands

### Auth
| Command | What it does |
|---|---|
| `login` | Sign in (SRP) and cache tokens |
| `whoami` | Signed-in user and org |
| `token` | Print the current ID token (for scripts) |

### Read
| Command | What it does |
|---|---|
| `agents list [-c CHAN] [--active]` | Every agent: name, type, the channels it's live on, id |
| `agent get A [--channel email] [--published\|--draft] [--out policy.md] [--raw]` | Intent description, training phrases, the policy as readable text with `{{chips}}`, settings, tools, outline. `--out` writes `policy.md` plus `policy.md.chips.json` for editing |
| `agent versions A` | Email versions (state, name, last edited) and local restore points |
| `actions list [--json]` | Workflow actions as a table; `--json` gives the full objects, redacted |
| `action get A [--out action.json]` | One action, redacted; `--out` writes a file ready for `action update` |
| `coaching list [--draft] [--agent A]` | Coaching (AI instructions): active, live/draft, scope (which agent), channels |
| `coaching get C [--out file.md] [--json]` | One instruction's text (the draft, if there is one) |
| `variables list` / `forms list` / `chips list` | What can go in a policy through the "/" menu, as `{{kind: label}}` tokens |
| `search-sources get A` | The search tool's sources and filters |
| `logs get CONV_ID [--full]` | One conversation's log trace |
| `logs list [--agent A] [--since DATE] [--channel C]` | Recent conversations (the filter names are inferred; agent filtering happens on your machine) |
| `metrics [--agent A] [--channel C] [--from D] [--to D]` | Per-agent metrics |
| `lint [A]` | Flags policy problems: support-email leaks, "mark Solved" leaks, GitHub sources, internal-guide quotes, empty chips |
| `snapshot` | Saves every agent's config (redacted) to `./snapshots/<time>/` |
| `get PATH [--show-secrets]` | Raw GET of any dashboard-api path, redacted unless `--show-secrets` |

### Draft writes (`--apply`)
| Command | What it does |
|---|---|
| `agent set-policy A --file policy.md` | Replaces the email policy with your edited text. Paragraphs you didn't change are kept exactly as stored; only edited blocks are rebuilt |
| `agent set-setting A response_limit=3 [bypass_fallback_triggers=true]` | Response limit (`off` clears it) and bypass of fallback triggers |
| `search-sources set A --file fields.json [--tool ID]` | The search tool's configuration (start from `search-sources get`) |
| `version save A --name "…"` | Names the draft in FT and keeps a local restore point in `~/ft_versions/` |
| `version restore A (--version N \| --name "…")` | Copies policy, settings and tool config from a version or restore point into the draft |
| `coaching update C [--file f.md] [--name N] [--active on\|off]` | Edits the coaching draft |
| `coaching discard C` | Drops the coaching draft; the published version stays |
| `action create --file action.json` | New action (secrets as `${env:NAME}`). No agent uses it until you add it in the builder |

### Live changes (`--confirm`)
| Command | What it does |
|---|---|
| `publish A --channel email` | Publishes the email draft; shows the live → draft diff first |
| `publish A --channel slack\|widget\|…` | Activates the latest workflow version on that channel |
| `channel on\|off A --channel C [--draft]` | Email: autopilot on/off on the live version (`--draft`: draft only). Other channels: activate/deactivate, as the UI's toggle does |
| `coaching publish C` | Publishes the coaching draft; shows the diff first |
| `action update A --file action.json` | Actions have no draft: this changes every agent that uses the action. Runs FT's impact check first |
| `agent set-intent A [--name N] [--description TEXT\|FILE] [--phrases FILE] [--add-phrase "…"]… [--remove-phrase "…"]…` | The Edit Agent dialog. No draft: routing changes on every channel as soon as it's saved |

### Test
| Command | What it does |
|---|---|
| `preview A\|auto --channel email --thread "msg 1" "msg 2" … [--full]` | Multi-turn email preview. Each turn shows the predicted intent, fallback, actions run, the reply and the log trace. `auto` lets FT pick the agent |

**Previews run real actions.** FT has no mock for email previews. Before
sending, the CLI lists the agent's tools that could change something
(non-GET API calls, handoffs, ticket actions, unknown tools) and asks for a
typed `yes`. `auto` always asks, because it can route to any agent.

### Other
`policy`, `versions`, `stats` (older read commands), and `capture-help`,
which prints a DevTools snippet that records the method, path and field names
(never values) of the requests the UI makes. It's for checking an endpoint
the CLI doesn't know yet.

## Editing a policy: the text format

`agent get A --out policy.md` writes the policy as readable text:

```
## Target Audience

You are a support assistant handling email from customers…

* Point them to the self-serve option first.
5. Check {{var: Persona Verification Passed}} before replying.

If nothing fits, reroute to {{reroute: Fallback}}.
```

- `##`/`###` are headings, `*` bullets, `1.` numbered items (a list can start
  at any number), `**bold**`, `*italic*`. A blank line separates blocks.
- `{{kind: label}}` is a "/" chip. Existing chips are put back exactly as
  stored (`policy.md.chips.json` holds their markup; keep it next to the file).
  The same chip used twice gets ` #2`, ` #3`.
- **New chips:** a `{{var: …}}` can name any variable from
  `ft variables list`. A new reroute or action chip works only if that same
  destination already appears in the policy; otherwise add it once in the UI.

Then `agent set-policy A --file policy.md` shows the diff and how many blocks
it keeps and rebuilds; add `--apply` to write it.

## Files

| Path | What |
|---|---|
| `~/.ft_cli_creds` | your login details (mode 600) |
| `~/.ft_cli_token.json` | cached tokens (mode 600) |
| `~/ft_backup_YYYYMMDD/` | backups made before every write (redacted) |
| `~/ft_versions/<agent>/` | local restore points from `version save` (redacted) |
| `./snapshots/<time>/` | output of `ft snapshot` (redacted) |

## Code map

| File | What's in it |
|---|---|
| `main.go` | command routing, `help` text, `login`, `get` |
| `config.go`, `auth.go`, `srp.go` | creds file, token cache, Cognito SRP login and refresh |
| `agents.go` | agent list, name resolution, live channels |
| `autoflow.go` | email versions, the draft, autopilot workflow, `agent get`, the "/" menu |
| `chips.go`, `blocks.go` | policy HTML ↔ text; block-preserving merge on write |
| `writes.go` | `set-policy`, `set-setting`, `set-intent`, search sources, versions, `publish`, `channel` |
| `coaching.go`, `actions.go` | coaching and action commands |
| `redact.go` | secret redaction, restore on write, `${env:NAME}` |
| `test.go`, `endpoints_write.go` | `preview` and its side-effect check |
| `insights.go` | `logs`, `metrics` |
| `audit.go`, `policy.go` | `lint`, `snapshot`, older read commands |
| `util.go` | flag parsing, confirm prompt, backups, diff |
| `*_test.go` | offline tests: run `go test ./...` |
