package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
	_ "time/tzdata"

	tea "charm.land/bubbletea/v2"
	"github.com/franciscocpg/husage/internal/claude"
	"github.com/franciscocpg/husage/internal/codex"
	"github.com/franciscocpg/husage/internal/codexcli"
	"github.com/franciscocpg/husage/internal/config"
	"github.com/franciscocpg/husage/internal/cursor"
	"github.com/franciscocpg/husage/internal/profile"
	"github.com/franciscocpg/husage/internal/subscription"
	"github.com/franciscocpg/husage/internal/tui"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "husage:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("husage", flag.ContinueOnError)
	showVersion := flags.Bool("version", false, "print version and commit, then exit")
	demo := flags.Bool("demo", false, "show sample subscriptions without accessing local accounts")
	once := flags.Bool("once", false, "print a snapshot and exit")
	jsonOut := flags.Bool("json", false, "print subscription data as JSON and exit")
	refresh := flags.Duration("refresh", config.DefaultRefresh, "override saved auto-reload interval for this launch (minimum 5s; API requests at most once per 5m)")
	zone := flags.String("timezone", "", "IANA timezone for reset times (default: system timezone)")
	var dirs profileDirs
	var codexDirs profileDirs
	flags.Var(&dirs, "claude-dir", "Claude configuration directory; repeat for multiple subscriptions, or use current")
	flags.Var(&codexDirs, "codex-home", "Codex home directory; repeat for multiple subscriptions, or use current for CODEX_HOME")
	profilesPath := flags.String("profiles", "", "JSON profile list (default: ~/.config/husage/profiles.json, if present)")
	width := flags.Int("width", 80, "snapshot width (20–200 columns)")
	debug := flags.Bool("debug", false, "append redacted Claude login renewal diagnostics to ~/.config/husage/debug.log")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if *showVersion {
		_, err := fmt.Fprintf(out, "husage %s (commit %s)\n", version, commit)
		return err
	}
	if *refresh < 5*time.Second {
		return fmt.Errorf("--refresh must be at least 5s")
	}
	if *width < 20 || *width > 200 {
		return fmt.Errorf("--width must be between 20 and 200")
	}
	loc, err := location(*zone)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	var provider subscription.Provider
	var profileActions []*tui.ProfileActions
	var removeActions *tui.RemoveActions
	var loginActions *tui.LoginActions
	if *demo {
		provider = subscription.Demo{}
	} else {
		active, profiles, err := claudeProfiles(home, dirs, *profilesPath)
		if err != nil {
			return err
		}
		group := claude.NewProfiles(active, profiles)
		if *debug {
			debugFile, err := openDebugLog(home)
			if err != nil {
				return err
			}
			defer debugFile.Close()
			group.SetDebugLog(claude.NewDebugLog(debugFile))
		}
		codexOptions, err := codexProfiles(home, codexDirs, *profilesPath)
		if err != nil {
			return err
		}
		codexGroup := codex.NewProfiles(codexOptions)
		cursorOptions, err := cursorProfile(home, *profilesPath)
		if err != nil {
			return err
		}
		cursorProvider := cursor.New(cursorOptions)
		provider = subscription.Combined{group, codexGroup, cursorProvider}
		loginActions = &tui.LoginActions{
			Command: profile.ExistingLoginCommand,
			Login: func(ctx context.Context, target subscription.LoginTarget) tea.ExecCommand {
				return profile.NewExistingLogin(ctx, target)
			},
			Succeeded: provider.(subscription.LoginRefresher).LoginSucceeded,
		}
		store := profile.Store{Home: home}
		profileActions = append(profileActions, &tui.ProfileActions{Directory: store.Directory,
			Login: func(ctx context.Context, name string) tea.ExecCommand { return profile.NewLogin(ctx, store, name) },
			Register: func(ctx context.Context, name string) (string, error) {
				dir, err := store.Register(ctx, name)
				if err == nil {
					group.Add(claude.Options{Home: home, ConfigDir: dir})
				}
				return dir, err
			}})
		codexStore := profile.Store{Home: home, Provider: "codex"}
		cursorStore := profile.Store{Home: home, Provider: "cursor"}
		removalPath := expand(*profilesPath, home)
		shownPath := removalPath
		if shownPath == "" {
			shownPath = store.ConfigPath()
		}
		currentClaude := active.ConfigDir
		if currentClaude == "" {
			currentClaude = filepath.Join(home, ".claude")
		}
		currentCodex := expand(os.Getenv("CODEX_HOME"), home)
		if currentCodex == "" {
			currentCodex = filepath.Join(home, ".codex")
		}
		removeActions = &tui.RemoveActions{Path: shownPath, OverrideHint: len(dirs) > 0 || len(codexDirs) > 0,
			Remove: func(ctx context.Context, a subscription.Account) error {
				switch a.Provider {
				case "Claude Code":
					return group.Remove(a.ID, func(paths []string) error { return store.Remove(ctx, removalPath, currentClaude, paths) })
				case "Codex":
					return codexGroup.Remove(a.ID, func(paths []string) error { return codexStore.Remove(ctx, removalPath, currentCodex, paths) })
				case "Cursor":
					return cursorProvider.Remove(a.ID, func(paths []string) error {
						return cursorStore.Remove(ctx, removalPath, cursor.NativeDirectory(home), paths)
					})
				default:
					return fmt.Errorf("Unsupported subscription provider")
				}
			}}
		profileActions = append(profileActions, &tui.ProfileActions{Name: "Codex", Kind: "codex", Directory: codexStore.Directory, Command: codexcli.LoginCommand,
			Login: func(ctx context.Context, name string) tea.ExecCommand { return profile.NewLogin(ctx, codexStore, name) },
			Register: func(ctx context.Context, name string) (string, error) {
				dir, err := codexStore.Register(ctx, name)
				if err == nil {
					codexGroup.Add(codex.Options{Home: dir})
				}
				return dir, err
			}})
		profileActions = append(profileActions, &tui.ProfileActions{Name: "Cursor", Kind: "cursor", Existing: true, ConfigPath: shownPath,
			Register: func(ctx context.Context, _ string) (string, error) {
				err := cursorProvider.Enable(ctx, func() error { return cursorStore.EnableCurrent(ctx, removalPath) })
				if err != nil {
					return "", err
				}
				return cursor.NativeDirectory(home), nil
			}})
	}
	// Bubble Tea ignores parent terminal signals while a child owns the terminal,
	// so cancelling login with Ctrl+C returns to the form instead of quitting us.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if *once || *jsonOut {
		ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		accounts, err := provider.Load(ctx)
		if err != nil {
			return err
		}
		if *jsonOut {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(accounts)
		}
		_, err = fmt.Fprintln(out, tui.Snapshot(accounts, *width, loc, time.Now()))
		return err
	}
	configStore := config.Store{Home: home}
	settings, err := configStore.Load()
	if err != nil {
		return err
	}
	interval, _ := settings.Interval()
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "refresh" {
			interval = *refresh
		}
	})
	if err := configStore.Ensure(ctx); err != nil {
		return fmt.Errorf("initialize configuration: %w", err)
	}
	model := tui.New(ctx, provider, interval, loc, *demo).
		WithProfileProviders(profileActions).
		WithRemoveActions(removeActions).
		WithLoginActions(loginActions).
		WithConfigActions(&tui.ConfigActions{Save: configStore.Save})
	_, err = tea.NewProgram(model, tea.WithContext(ctx), tea.WithOutput(out)).Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func debugLogPath(home string) string {
	return filepath.Join(home, ".config", "husage", "debug.log")
}

func openDebugLog(home string) (*os.File, error) {
	path := debugLogPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create debug log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("open debug log: %w", err)
	}
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return nil, fmt.Errorf("secure debug log: %w", err)
	}
	return f, nil
}

func expand(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

func location(zone string) (*time.Location, error) {
	if zone != "" {
		return time.LoadLocation(zone)
	}
	if z := os.Getenv("TZ"); z != "" {
		if loc, err := time.LoadLocation(z); err == nil {
			return loc, nil
		}
	}
	if p, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if _, z, ok := strings.Cut(p, "zoneinfo/"); ok {
			if loc, err := time.LoadLocation(z); err == nil {
				return loc, nil
			}
		}
	}
	return time.Local, nil
}
