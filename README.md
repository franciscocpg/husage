# husage

A quiet, keyboard-driven dashboard for your Claude Code and Codex subscriptions. Built with [Bubble Tea v2](https://github.com/charmbracelet/bubbletea) and Lip Gloss.

![husage showing two sample Claude subscriptions](docs/demo.png)

## Run

Requires Go 1.25 or newer and a terminal. Install the native Claude and/or Codex CLI for the subscriptions you want to monitor.

```sh
go run .
```

Or build a standalone binary:

```sh
make build
./bin/husage
```

Try the interface without accessing accounts or the network:

```sh
go run . --demo
```

The dashboard shows account names, email addresses, the active login for each provider, session usage, weekly usage, and model-specific limits when available. Subscriptions are grouped into provider sections with a heading and subscription count. Cards from the same provider appear side by side when space allows, with equal heights within each row. Extra cards wrap to another row; narrow terminals stack the cards vertically. Reset times use your system timezone. Usage bars turn amber at 75% and rose at 90%.

| Key | Action |
| --- | --- |
| `a` | Add a Claude or Codex profile (Tab selects the provider) |
| `c` | Edit configuration |
| `r` | Refresh data |
| `↑` / `↓`, `k` / `j` | Scroll |
| Page Up / Page Down, Ctrl+B / Ctrl+F | Scroll a page |
| Home / End, `g` / `G` | First / last account |
| `q`, Esc, Ctrl+C | Quit |

## Configuration

The dashboard automatically reloads every **5 minutes** by default. On the first interactive launch, husage creates `~/.config/husage/config.json`:

```json
{
  "refresh_interval": "5m"
}
```

Press **c** to edit the auto-reload interval. Use a duration such as `30s`, `5m`, `10m`, or `1h` (minimum `5s`). **Ctrl+U** clears the field, **Enter** saves and applies the interval immediately, and **Esc** discards edits. The next automatic reload is scheduled from the time you save. Changes persist across restarts; manual edits to the file take effect on the next launch.

`--refresh` overrides the saved interval for that launch without changing the file. Saving from the configuration screen replaces that override and persists the new value. Snapshot modes (`--once` and `--json`) do not create configuration files.

Each profile caches usage for five minutes, even if the dashboard reloads more frequently. Native credential changes can trigger an earlier read.

## Where usage comes from

husage reads the current Claude account from `~/.claude.json`, obtains its existing OAuth login from macOS Keychain or `.credentials.json`, and makes a read-only request to `https://api.anthropic.com/api/oauth/usage`. This is an internal Claude endpoint, so its schema or availability can change. The parser supports both `seven_day_*` buckets and newer `limits[].kind = weekly_scoped` model caps, including Fable.

The live dashboard combines independently authenticated Claude configurations. Each configuration keeps its own credentials, usage cache, and refresh cooldown. Subscriptions sharing an email stay separate when their organization IDs differ. Duplicate configurations of the same subscription produce one card, and a missing or expired login does not hide other accounts. The subscription matching the current shell's Claude configuration appears first with the active badge.

The direct provider requests usage at most once every five minutes per configuration during a run and honors longer `Retry-After` delays. Pressing `r` does not bypass this cooldown. Successful Claude usage reads are saved under `~/.cache/husage/claude/` in private files keyed by account and organization. The cache contains usage windows and their original timestamp, never credentials. Failed requests (including rate limits) display the last successful usage with the error and an explicit **Stale data** warning, including after restarting husage. A successful refresh replaces the cache and clears the warning. With no successful read yet, the card shows the error without usage. Missing or corrupt caches are ignored; cache write failures leave live usage visible with a warning. Data older than ten minutes is marked stale; a past reset is labeled rather than inventing fresh usage. Missing limits and reset times remain unavailable.

Usage polling never switches accounts or runs inference. When a profile's access token expires, husage delegates renewal to the native `claude auth login --claudeai` command using that profile's saved refresh token and scopes. Claude's [documented non-interactive login environment](https://code.claude.com/docs/en/env-vars) avoids opening a browser and lets Claude manage credential persistence. The refresh token is passed only in the child process environment, never in command arguments or terminal output. A usage HTTP 401 triggers at most one renewal and retry; HTTP 403 and rate limits do not trigger renewal. Failed renewals back off for five minutes. macOS may ask for access to the existing Keychain item. Credential storage follows [Claude Code's authentication documentation](https://code.claude.com/docs/en/authentication#credential-management).

If a refresh token has expired or been revoked, or its saved scopes are missing, automatic renewal cannot recover it. The dashboard shows a login command targeting that specific profile. Complete that login, then press **r**; husage detects the new credentials without a restart. Profile creation still asks you to confirm the interactive login command before running it. Concurrent husage renewals for the same credential store are serialized with a short-lived lock under `~/.config/husage/locks/`.

## Add a profile in the TUI

Press `a`, use **Tab** to select Claude or Codex, enter a profile name (for example, `personal`), and press Enter to preview the login command. Nothing is created or executed yet. Press Enter again to execute it: husage creates `~/.config/husage/claude/personal` or `~/.config/husage/codex/personal` and temporarily hands the terminal to `claude auth login --claudeai` or `codex login`. The command uses the new directory as `CLAUDE_CONFIG_DIR` or `CODEX_HOME`.

Only after the command exits successfully does husage append the directory to `~/.config/husage/profiles.json` and add the profile to the dashboard. A failed or cancelled login returns to the confirmation screen without registering the profile; Enter retries the login. If login succeeds but saving the list fails, Enter retries only the save. The native CLI's files in the prepared directory are retained, including after a failed login, so credentials are never removed as part of error recovery.

Names accept 1–48 letters, numbers, dashes, or underscores and must start with a letter or number. Esc cancels the form; before execution this leaves no files behind. After an unsuccessful attempt, you can reuse the same name even after closing the form or restarting husage: an unregistered directory without credential files or saved account metadata can be reused, preserving its setup files. Registered profiles, directories containing credentials or account metadata, symbolic links, and invalid metadata are rejected when starting a new setup. New profile directories and the JSON file are created with owner-only permissions. The child login process uses its own `CLAUDE_CONFIG_DIR` or `CODEX_HOME` and clears inherited credential overrides, exactly as shown in the preview. The command runs directly without a shell.

The action always saves to the default `~/.config/husage/profiles.json`, including when the current view was launched with `--profiles`, `--claude-dir`, or `--codex-home`. Those overrides continue to control future launches when explicitly supplied. Demo mode stays read-only and does not offer profile creation.

## Show two subscriptions

Keep your existing Claude login, and log into your second subscription in a separate native configuration. Select the other subscription/organization during authentication:

```sh
env -u CLAUDE_SECURESTORAGE_CONFIG_DIR \
  CLAUDE_CONFIG_DIR="$HOME/.claude-secondary" \
  claude auth login --claudeai
```

Then show both in husage:

```sh
./bin/husage --claude-dir current --claude-dir "$HOME/.claude-secondary"
```

`current` follows `CLAUDE_CONFIG_DIR` and `CLAUDE_SECURESTORAGE_CONFIG_DIR` from your shell, falling back to Claude's default login. Every explicit directory uses its own native credential store; it does not inherit the shell's secure-store override. Use the same absolute directory string used for login, because Claude derives its macOS Keychain service name from that string.

To make these profiles the default, create `~/.config/husage/profiles.json`:

```json
["current", "~/.claude-secondary"]
```

After that, just run `./bin/husage`. This file contains provider selections and directory paths only. husage never copies or saves tokens. `--profiles /path/to/profiles.json` selects a different list, and explicit `--claude-dir` arguments override its Claude entries; `--codex-home` overrides its Codex entries. Legacy string entries continue to select Claude profiles. The demo remains entirely offline and includes both providers.

For a secondary login that cannot be renewed automatically, repeat its login command above or open Claude Code with that same `CLAUDE_CONFIG_DIR`, then press **r** in husage.

## Codex subscriptions

Codex accounts are isolated by `CODEX_HOME`, which defaults to `~/.codex`. husage uses the installed CLI's [app-server account interface](https://learn.chatgpt.com/docs/app-server): `account/read` identifies the ChatGPT login and `account/rateLimits/read` returns its usage windows. Codex manages its native credentials and authentication renewal. husage never opens a model thread, generates tokens, or consumes reset credits.

The current Codex home is discovered automatically alongside your Claude profiles. To show multiple existing Codex homes:

```sh
./bin/husage --codex-home current --codex-home "$HOME/.codex-work"
```

To create another login, press **a**, select **Codex** with **Tab**, and follow the confirmation/login flow. To save homes that already exist, add typed entries to `~/.config/husage/profiles.json`, preserving your existing entries:

```json
[
  "current",
  "~/.claude-secondary",
  {"provider": "codex", "directory": "current"},
  {"provider": "codex", "directory": "~/.codex-work"}
]
```

For Codex entries, `current` resolves `CODEX_HOME` from your shell, falling back to `~/.codex`. A Codex configuration `--profile` is not a separate login home. On the first Codex addition through the TUI, husage also saves a `current` Codex entry to retain the automatically discovered account.

Cards show the home name, ChatGPT plan, account email, usage percentages, and reset times. Windows are labeled using their reported duration; plans that only expose a weekly limit show only that limit. Additional metered buckets appear separately. Distinct account/workspace IDs remain separate even when the email matches. API-key and cloud-provider authentication do not expose ChatGPT subscription usage and show an explanation instead. Missing or failed homes do not hide healthy subscriptions.

Codex polling is bounded to five minutes per home. Changes to file-based `auth.json` are detected on the next reload without reading or displaying token contents; keychain-only login changes are picked up on the next scheduled poll. App-server may update its normal native state/logs and renew credentials while serving these account requests. Its protocol is evolving; the integration was verified with Codex CLI 0.155.0.

## Options

```sh
./bin/husage --once                         # Styled snapshot, then exit
./bin/husage --json                         # Account and usage data; no tokens
./bin/husage --timezone America/Sao_Paulo
./bin/husage --refresh 10s                  # Local polling; minimum 5 seconds
./bin/husage --claude-dir ~/.claude-work     # Select another Claude configuration
./bin/husage --claude-dir current --claude-dir ~/.claude-secondary
./bin/husage --codex-home current --codex-home ~/.codex-work
./bin/husage --profiles /path/to/profiles.json
./bin/husage --demo --once --width 60
```

`CLAUDE_CONFIG_DIR` selects the Claude configuration directory. `CLAUDE_SECURESTORAGE_CONFIG_DIR`, when set, selects its credential store. Custom macOS configuration directories use Claude's hashed Keychain service suffix. API-key and cloud-provider billing are outside this subscription view.

## Development

```sh
make test
go vet ./...
```

The `internal/subscription.Provider` interface separates acquisition from rendering. Add another harness by implementing `Load(context.Context)` and returning account names, usage windows, timestamps, and source/error information. The adapters live in `internal/claude` and `internal/codex`; the Bubble Tea model lives in `internal/tui`.

Tests cover native Claude account discovery, multiple organizations sharing an email, profile lists and overrides, credential isolation, partial failures, duplicate subscriptions, missing and malformed metadata, scoped model limits, refresh cooldowns, rate-limit backoff, terminal widths, scrolling, and timezone conversion. The UI has also been exercised in a real PTY for resize, refresh, scrolling, and clean terminal restoration.
