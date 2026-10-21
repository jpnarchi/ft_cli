# ft_cli

A command-line tool for an account in a compatible agent dashboard: read and audit agents,
tune/inspect autoflow policies, run multi-turn previews, read their logs, and
toggle agents on/off — the things you do daily in the Agent Builder UI.

Your password never leaves your machine. `ft login` uses AWS Cognito SRP, which
sends only a zero-knowledge proof, then caches a token that auto-refreshes.

## Setup
```bash
# 1. creds (your own account; password optional — you'll be prompted)
printf 'FT_API_BASE=https://your-api.example.com\nFT_SUBDOMAIN=your-org\nFT_EMAIL=you@example.com\nFT_COGNITO_CLIENT_ID=...\nFT_COGNITO_REGION=...\nFT_COGNITO_POOL_ID=...\n' > ~/.ft_cli_creds
chmod 600 ~/.ft_cli_creds

# 2. build
go build -o ft .

# 3. log in (prompts for password, hidden) and verify
./ft login
./ft agents
```

## Commands
| Command | Status | What it does |
|---|---|---|
| `login` / `token` | ✅ confirmed | SRP login; cached + auto-refreshing token |
| `whoami` | ✅ | signed-in user + org |
| `agents` | ✅ | list agents (name, type, id) |
| `policy [-t] <agent>` | ✅ | full config; `-t` prints just the policy text with chips rendered |
| `actions` | ✅ | list workflow actions |
| `versions <agent>` | ✅ | version history |
| `stats [--days N]` | ✅ | per-agent email volume |
| `snapshot` | ✅ | save all agent configs to `./snapshots/<ts>/` |
| `lint [<agent>]` | ✅ | flag support-email / Solved-leak / GitHub-source / empty-chip |
| `logs <id>` | ✅ | fetch a preview conversation's logs |
| `get <path>` | ✅ | raw read-only GET |
| `preview <agent> -m "..." [-m "..."]` | ⚠️ needs field confirm | multi-turn email preview; prints intent, actions, fallback, reply per turn |
| `toggle <agent> on\|off` | ⚠️ endpoint unverified | turn an agent on/off (asks to confirm) |
| `capture-help` | ✅ | prints a console snippet to confirm the ⚠️ endpoints safely |

Publishing a policy is intentionally **not** in the CLI — that stays in the UI,
by a human, so nothing reaches contributors without review.

## Confirming the ⚠️ commands
Run `./ft capture-help`, paste the snippet into the dashboard's DevTools console,
perform the one action (a preview send, or toggling an agent), and share the
printed `method / path / fields` — it contains no values, tokens, or account
data. Those few names finalize `preview` and `toggle`.

## API endpoints
- Agents: `GET /dashboard-controls/solve/v2/intents?...product=workflow_builder`
- Intent config: `GET /dashboard-controls/solve/v2/workflow-builder/intent/{id}`
- Email/policy config: `GET /solve/email/email-builder/intent_definition_id/{id}/email-configurations`
- Versions: `GET /dashboard-controls/solve/autoflow/workflow-builder/{wf}/v2/version-history`
- Actions: `GET /dashboard-controls/solve/v2/workflow-builder/actions`
- Preview start: `POST /solve/email/preview` (multipart)
- Preview reply: `POST /solve/email/preview/{session}/reply` (JSON: preview_session_id, reply_text, custom_css, ai_instruction_filters_for_preview)
