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
| `r` | Refresh data |
| `↑` / `↓`, `k` / `j` | Scroll |
| Page Up / Page Down, Ctrl+B / Ctrl+F | Scroll a page |
| Home / End, `g` / `G` | First / last account |
| `q`, Esc, Ctrl+C | Quit |

## Where usage comes from

husage reads the current Claude account from `~/.claude.json`, obtains its existing OAuth login from macOS Keychain or `.credentials.json`, and makes a read-only request to `https://api.anthropic.com/api/oauth/usage`. This is an internal Claude endpoint, so its schema or availability can change. The parser supports both `seven_day_*` buckets and newer `limits[].kind = weekly_scoped` model caps, including Fable.

The live dashboard currently shows the subscription for the selected Claude configuration. Use `--claude-dir` to select another configuration. The two-account demo uses sample data only.

The direct provider requests usage at most once every five minutes per account during a run and honors longer `Retry-After` delays. Pressing `r` does not bypass this cooldown. Network failures preserve the last successful usage with a visible error. Data older than ten minutes is marked stale; a past reset is labeled rather than inventing fresh usage. Missing limits and reset times remain unavailable.

husage never switches accounts, runs inference, refreshes OAuth tokens, or writes credentials. If your login expires, open Claude Code to renew it, then restart husage. macOS may ask for access to the existing Keychain item. Credential storage follows [Claude Code's authentication documentation](https://code.claude.com/docs/en/authentication#credential-management).

## Options

```sh
./bin/husage --once                         # Styled snapshot, then exit
./bin/husage --json                         # Account and usage data; no tokens
./bin/husage --timezone America/Sao_Paulo
./bin/husage --refresh 10s                  # Local polling; minimum 5 seconds
./bin/husage --claude-dir ~/.claude-work     # Select another Claude configuration
./bin/husage --demo --once --width 60
```

`CLAUDE_CONFIG_DIR` selects the Claude configuration directory. `CLAUDE_SECURESTORAGE_CONFIG_DIR`, when set, selects its credential store. Custom macOS configuration directories use Claude's hashed Keychain service suffix. API-key and cloud-provider billing are outside this subscription view.

## Development

```sh
make test
go vet ./...
```

The `internal/subscription.Provider` interface separates acquisition from rendering. Add another harness by implementing `Load(context.Context)` and returning account names, usage windows, timestamps, and source/error information. The Claude adapter lives in `internal/claude`; the Bubble Tea model lives in `internal/tui`.

Tests cover native Claude account discovery, custom configuration selection, missing and malformed metadata, scoped model limits, failed responses, refresh cooldowns, rate-limit backoff, account isolation, terminal widths, scrolling, and timezone conversion. The UI has also been exercised in a real PTY for resize, refresh, scrolling, and clean terminal restoration.
