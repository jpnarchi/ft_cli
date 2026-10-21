# ft CLI — feature spec & status (2026-10-21)

Principle: **reads are free; writes go to a draft, show a diff, need `--apply`,
and back up first (`~/ft_backup_YYYYMMDD/`); anything that goes live (publish,
channel on/off, coaching publish, action update) needs `--confirm`, one item at
a time, after approval.** Writes refuse to touch a published version directly.
**Secrets are never printed or saved**: action/tool credentials show as
`[redacted]` everywhere (output, `--out` files, backups, snapshots) and are
restored from FT's current value on write; new ones are passed as `${env:NAME}`.

Endpoints and request bodies come from Forethought's own dashboard code (its
public JS bundle) plus live reads. Status key:

- 🟢 confirmed live (read against the real account)
- ✅ built and offline-tested; request shape taken from the dashboard bundle, not yet sent live
- ⛔ not built (reason given)

## 1. Read
| Command | Status | Notes |
|---|---|---|
| `agents list [-c CHAN] [--active]` | 🟢 | |
| `agent get A --channel email [--published\|--draft] [--out f]` | 🟢 | Fixed 2026-10-21: email versions are `configuration_version` + `is_in_use` (the old parser found none). Uses the draft if there is one, else the live version. Chips are `<span class="…-node" data-flow-id id name>`; labels come from `name`. |
| `agent versions A --channel email` | 🟢 | |
| `actions list [--json]` | 🟢 | Table by default; `--json` is redacted (was printing an API key). |
| `action get A [--out f.json]` | 🟢 | Redacted; `--out` writes a file ready for `action update`. |
| `coaching list [--draft] [--agent A]` / `coaching get C` | 🟢 | Scope shows the agent the instruction is attached to; one pointing at a workflow id no current agent owns shows as `workflow <id>` (usually a leftover). Duplicate names must use the id. |
| `variables list` / `forms list` / `chips list` | 🟢 | |
| `search-sources get A` | ✅ | Reads tool settings from `tool_fields` (was `configuration_fields`, which GET never returns). Redacted. |
| `logs get` / `logs list` / `metrics` | ✅ | `logs list` filter names are inferred; agent filtering is client-side. |
| `get PATH [--show-secrets]` | 🟢 | Redacted unless `--show-secrets`. |

## 2. Write (draft, diff, `--apply`, backup)
| Command | Status | Notes |
|---|---|---|
| `agent set-policy A --file policy.md` | 🟢 | Live 2026-10-21 on an agent with email off that had never been live: wrote a heading/paragraph/list, FT stored it unchanged in editor markup, then reverted to empty. Block-preserving: unchanged paragraphs/headings/lists (and spacer paragraphs) are written back byte for byte; edited blocks are rebuilt in the editor's markup (`editor-paragraph`, `data-flow-id`, `<span>` runs, `ol start`/`li value`). Verified offline on a real 63-block policy: unchanged → identical (36/36 content blocks); one sentence edited → 35 kept, 1 rebuilt. Refuses if the current policy can't round-trip losslessly. New chips: variables are built from the "/" menu; a reroute/action only if the same destination already appears in the policy. |
| `agent set-setting A response_limit=…` | 🟢 | Live 2026-10-21 on the same agent: — → 3 → off, each read back. |
| `search-sources set`, `version save/restore` | ✅ | Tool configs restore `[redacted]` values from the current config before writing. |
| `coaching update C [--file] [--name] [--active on\|off]` | ✅ | `POST /ai-instruction/{id}` with the full object (what the UI does), then re-read to verify. |
| `coaching discard C` | ✅ | `POST /ai-instruction/{id}/discard-draft`; backs up the draft first. |
| `action create --file f.json` | ✅ | `POST …/workflow-builder/action`; refuses duplicate names and `[redacted]` marks. Not used by any agent until added in the builder. |

**Draft vs live (checked 2026-10-21):** FT's draft usually has the same
number as the live version (e.g. both v14). A UI autosave on such an agent
edited the v14 autopilot workflow, created no v15, and flipped
`needs_to_be_published` to true; contributors only see it after Publish (the
Journey tab). The CLI writes the same working copy, so its draft writes behave
like the UI's autosave, and `publish` is still the only step that goes live.

## 3. Test
| Command | Status | Notes |
|---|---|---|
| `preview A\|auto --channel email --thread "m1" "m2" …` | ✅ | **FT has no mock for email previews** (bundle check: the UI sends only query, context variables, intent and coaching filters). The old `mock_actions` / `create_real_tickets` flags were invented and are removed. The CLI now lists the agent's tools that could change something (non-GET API calls, handoffs, ticket actions, anything unknown) and needs a typed `yes`; `auto` always asks. GET API calls and search tools run without asking. Gate checked live (declined, nothing sent). |
| `preview --channel widget` | ⛔ | needs the widget's API key (a secret); use the UI. |
| Agent test cases with mocked actions | ⛔ | FT's real mocking lives in its agent test-case feature (`mock_actions: [{action_id, outputs}]`). Not built. |

## 4. Live, gated (`--confirm`)
| Command | Status | Endpoint |
|---|---|---|
| `publish A --channel email --confirm` | ✅ | `PUT …/intent-email-configuration/{cfg}/version/{v}/published`. |
| `publish A --channel widget\|slack\|… --confirm` | ✅ | `PUT …/workflow/{channel wf}/activate {version, workflow_types}` with the latest version. |
| `channel on\|off A --channel email --confirm` | ✅ | `PUT …/version/{v}/autopilot-enabled` on the live version. |
| `channel on\|off A --channel widget\|slack\|api\|voice\|web_call\|playbook --confirm` | ✅ | Fixed 2026-10-21 to match the dashboard's toggle: `PUT …/workflow/{id}/activate` or `/deactivate` with that channel's types as a delta (widget is `flamethrower-live/-preview`, Slack `slack-live/-preview`, …), on the channel's workflow id from the agents list (`slack_workflow_id` etc., falling back to `intent_workflow_id`). The old code sent the full list to `activate` only, so `off` never turned anything off, and `widget` built a type that doesn't exist. Dry runs checked; not sent live (every candidate is live to contributors). |
| `coaching publish C --confirm` | ✅ | `POST /ai-instruction/{id}/publish`; shows published → draft diff first. |
| `agent set-intent A [--name] [--description] [--phrases f] [--add-phrase …] [--remove-phrase …] --confirm` | ✅ | The Edit Agent dialog: `PUT …/workflow-builder/intent/{id} {name, description, phrases}` (same body as the dashboard). **No draft**: routing changes on every live channel when saved, so it needs `--confirm` (was `--apply`). Phrase flags repeat; duplicates are skipped, a missing phrase to remove is an error. Dry run checked on a live agent. |
| `action update A --file f.json --confirm` | ✅ | `PUT …/workflow-builder/action/{id}`. Actions have no draft, so this is live for every agent using it. Runs FT's `update-impact` check first; a 409 means FT blocked it because agents would break. `action_type` can't change. |

## Still open
1. First live sends of the ✅ write commands (coaching update/publish, action
   create/update, set-intent), each on a low-risk item, with approval.
2. A first live `channel on|off` on a non-email channel (needs an agent where a
   brief toggle is acceptable).

Offline tests: `go test ./...` (chip round trips on the real markup, block
preservation, redaction/restore, env refs).
