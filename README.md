# husage

A quiet, keyboard-driven dashboard for your coding harness subscriptions. Built with [Bubble Tea v2](https://github.com/charmbracelet/bubbletea) and Lip Gloss. Claude Code is the first provider.

![husage showing two sample Claude subscriptions](docs/demo.png)

## Run

Requires Go 1.25 or newer and a terminal.

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

The dashboard shows account names, email addresses, the active Claude login, session usage, weekly usage, and model-specific limits when available. Reset times use your system timezone. Usage bars turn amber at 75% and rose at 90%.

| Key | Action |
| --- | --- |
| `a` | Add a Claude profile |
| `r` | Refresh data |
| `↑` / `↓`, `k` / `j` | Scroll |
| Page Up / Page Down, Ctrl+B / Ctrl+F | Scroll a page |
| Home / End, `g` / `G` | First / last account |
| `q`, Esc, Ctrl+C | Quit |

## Where usage comes from

husage reads the current Claude account from `~/.claude.json`, obtains its existing OAuth login from macOS Keychain or `.credentials.json`, and makes a read-only request to `https://api.anthropic.com/api/oauth/usage`. This is an internal Claude endpoint, so its schema or availability can change. The parser supports both `seven_day_*` buckets and newer `limits[].kind = weekly_scoped` model caps, including Fable.

The live dashboard combines independently authenticated Claude configurations. Each configuration keeps its own credentials, usage cache, and refresh cooldown. Subscriptions sharing an email stay separate when their organization IDs differ. Duplicate configurations of the same subscription produce one card, and a missing or expired login does not hide other accounts. The subscription matching the current shell's Claude configuration appears first with the active badge.

The direct provider requests usage at most once every five minutes per configuration during a run and honors longer `Retry-After` delays. Pressing `r` does not bypass this cooldown. Network failures preserve the last successful usage with a visible error. Data older than ten minutes is marked stale; a past reset is labeled rather than inventing fresh usage. Missing limits and reset times remain unavailable.

Usage polling never switches accounts, runs inference, or refreshes OAuth tokens. Profile creation delegates login to the Claude CLI after you confirm its command; Claude manages the credentials in its native store. If an existing login expires, open Claude Code to renew it, then restart husage. macOS may ask for access to the existing Keychain item. Credential storage follows [Claude Code's authentication documentation](https://code.claude.com/docs/en/authentication#credential-management).

## Add a profile in the TUI

Press `a`, enter a profile name (for example, `personal`), and press Enter to preview the login command. Nothing is created or executed yet. Press Enter again to execute it: husage creates `~/.config/husage/claude/personal` and temporarily hands the terminal to `claude auth login --claudeai` so you can complete authentication.

Only after the command exits successfully does husage append the directory to `~/.config/husage/profiles.json` and add the profile to the dashboard. A failed or cancelled login returns to the confirmation screen without registering the profile; Enter retries the login. If login succeeds but saving the list fails, Enter retries only the save. Claude's files in the prepared directory are retained, including after a failed login, so credentials are never removed as part of error recovery.

Names accept 1–48 letters, numbers, dashes, or underscores and must start with a letter or number. Esc cancels the form; before execution this leaves no files behind. Existing profiles are preserved; duplicate names and existing directories are rejected when starting a new setup. The profile directory and JSON file are created with owner-only permissions. The child login process uses its own `CLAUDE_CONFIG_DIR` and clears inherited credential overrides, exactly as shown in the preview. The command runs directly without a shell.

The action always saves to the default `~/.config/husage/profiles.json`, including when the current view was launched with `--profiles` or `--claude-dir`. Those overrides continue to control future launches when explicitly supplied. Demo mode stays read-only and does not offer profile creation.

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

After that, just run `./bin/husage`. This file contains directory paths only. husage never copies or saves tokens. `--profiles /path/to/profiles.json` selects a different list, and explicit `--claude-dir` arguments override the list. The two-account demo remains entirely offline.

For an expired secondary login, repeat its login command above or open Claude Code with that same `CLAUDE_CONFIG_DIR`, then restart husage.

## Options

```sh
./bin/husage --once                         # Styled snapshot, then exit
./bin/husage --json                         # Account and usage data; no tokens
./bin/husage --timezone America/Sao_Paulo
./bin/husage --refresh 10s                  # Local polling; minimum 5 seconds
./bin/husage --claude-dir ~/.claude-work     # Select another Claude configuration
./bin/husage --claude-dir current --claude-dir ~/.claude-secondary
./bin/husage --profiles /path/to/profiles.json
./bin/husage --demo --once --width 60
```

`CLAUDE_CONFIG_DIR` selects the Claude configuration directory. `CLAUDE_SECURESTORAGE_CONFIG_DIR`, when set, selects its credential store. Custom macOS configuration directories use Claude's hashed Keychain service suffix. API-key and cloud-provider billing are outside this subscription view.

## Development

```sh
make test
go vet ./...
```

The `internal/subscription.Provider` interface separates acquisition from rendering. Add another harness by implementing `Load(context.Context)` and returning account names, usage windows, timestamps, and source/error information. The Claude adapter lives in `internal/claude`; the Bubble Tea model lives in `internal/tui`.

Tests cover native Claude account discovery, multiple organizations sharing an email, profile lists and overrides, credential isolation, partial failures, duplicate subscriptions, missing and malformed metadata, scoped model limits, refresh cooldowns, rate-limit backoff, terminal widths, scrolling, and timezone conversion. The UI has also been exercised in a real PTY for resize, refresh, scrolling, and clean terminal restoration.
