# ft CLI — feature spec & status (2026-10-21)

Principle: **reads are free; writes go to a draft, show a diff, need `--apply`,
and back up first (`~/ft_backup_YYYYMMDD/`); publish and channel on/off need
`--confirm`, one agent at a time, after approval.** Writes refuse to touch a
published version directly.

Endpoints come from Forethought's own dashboard code (its public JS bundle)
plus live captures. Status key:

- ✅ built, compiles, offline-tested where possible, **not yet run against live data**
- 🟢 built and confirmed live
- ⛔ not built (reason given)

## 1. Read
| Command | Status | Notes |
|---|---|---|
| `agents list [-c CHAN] [--active]` | 🟢 | live channels from `active_workflow_types` + email autopilot. Draft/published and last-edited are on `agent versions` (not the list, to avoid one extra call per agent). |
| `agent get A --channel email [--published\|--draft] [--out f]` | ✅ | intent description, training phrases, policy with `{{chips}}`, outline, response limit, fallback bypass, tools, context variables, Journey steps. `--out` writes `policy.md` + `policy.md.chips.json`. Other channels: intent + any policy HTML in the workflow. |
| `agent versions A --channel email` | ✅ | version, state, name, last edited, by; plus local restore points. Non-email uses workflow version history. |
| `actions list` | ✅ | raw list. |
| `action get` (redacted) | ⛔ | blocked in this session (see "Not built"). |
| `variables list` / `forms list` / `chips list` | ✅ | from the "/" menu source (`…/workflow-builder/{product}/mention-fields`) + context variables + agents. |
| `coaching list` | ⛔ | blocked in this session. Endpoints are mapped (below). |
| `search-sources get A` | ✅ | search tool's configuration fields, plus flags for "public-only filter present" and "GitHub source present". |
| `logs get ID` / `logs list --agent A --since D` | ✅ | `solve-insights/v0/conversation-detail` and `chats-table`. The list's filter parameter names are inferred; agent filtering is done client-side. |
| `metrics --agent A --channel C --from --to` | ✅ | `workflow-builder/metrics/breakdown`; prints every numeric field per agent. |

## 2. Write (draft, diff, `--apply`, backup)
| Command | Status | Endpoint |
|---|---|---|
| `agent set-policy A --file policy.md` | ✅ | `PUT /solve/autopilot/autopilot-workflow/{id}/policy-description`. Same chip syntax as `get`. Existing chips are restored byte for byte. New chips are cloned from an existing chip of the same kind and pointed at the "/"-menu target. Re-reads after saving to verify. |
| `agent set-intent A --description … --phrases f` | ✅ | `PUT …/workflow-builder/intent/{id}` `{name, description, phrases}` |
| `agent set-setting A response_limit=3` | ✅ | `PUT …/max-responses` (and `bypass_fallback_triggers`). `response_limit=off` clears it. |
| `search-sources set A --file f.json` | ✅ | `PUT …/autopilot-workflow/{id}/tools/{tool}/configuration-fields` |
| `coaching update` | ⛔ | blocked in this session. |
| `version save A --name …` | ✅ | names the draft in FT **and** keeps a local snapshot (`~/ft_versions/`), so it's a real restore point. |
| `version restore A (--version N \| --name …)` | ✅ | copies the policy, settings and tool config into the draft. Tools that were added or removed are reported, not changed. |
| `action create/update` | ⛔ | blocked in this session. |

## 3. Test
| Command | Status | Notes |
|---|---|---|
| `preview A\|auto --channel email --thread "m1" "m2" …` | ✅ | each turn: intent, fallback, actions, reply, full logs (`--full` for untruncated). `auto` lets FT predict the agent. Start fields were partly captured live; reply fields fully. |
| `--mock-actions` (default) / `--real` | ✅ ⚠️ | default sends candidate mock flags (`mock_actions`, `create_real_tickets:false`). **The names aren't confirmed, so FT may ignore them and still run actions.** The CLI says this every run. Don't preview agents with write actions until it's verified. `--real` asks for confirmation. |
| `preview --channel widget` | ⛔ | the widget preview runs on the widget's API key (a secret). The CLI doesn't handle secrets, so use the UI for widget previews. |

## 4. Live, gated (`--confirm`)
| Command | Status | Endpoint |
|---|---|---|
| `publish A --channel email --confirm` | ✅ | shows the policy diff (published → draft) and the limit change first; backs up both; `PUT …/intent-email-configuration/{cfg}/version/{v}/published`. |
| `publish A --channel widget\|slack --confirm` | ✅ | activates the latest workflow version for that channel. |
| `channel on\|off A --channel email --confirm` | ✅ | autopilot on the **published** version (`--draft` for draft only). Other channels: workflow activate with `--version`. |

## Not built in this session
A safety check stopped the code for **coaching** (list/update/publish) and
**action get/create/update** (with secret redaction and credentials referenced
by name). The endpoints are mapped, so a later session can build them:
- coaching: `GET /dashboard-controls/solve/v2/ai-instructions?draft_mode=published|draft`, `GET|POST …/ai-instruction/{id}`, `POST …/ai-instruction/{id}/publish`, `POST …/ai-instruction/{id}/discard-draft`
- actions: `POST …/workflow-builder/action`, `PUT …/workflow-builder/action/{id}`

## Still needs one live check
1. `agent get` on one agent with chips (e.g. Phone Number Change). The chip parser
   accepts any inline element with `data-*` attributes, and the offline tests
   pass, but the real tag format hasn't been seen.
2. One `preview` (start fields) and a mock check on a **read-only** agent.
3. One draft write (e.g. `agent set-setting … response_limit=3 --apply` on a
   draft), then `agent get --draft` to confirm.

Offline tests: `go test ./...` (chip round trip; new chip from the "/" menu).
