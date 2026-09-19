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
	"github.com/franciscocpg/husage/internal/config"
	"github.com/franciscocpg/husage/internal/profile"
	"github.com/franciscocpg/husage/internal/subscription"
	"github.com/franciscocpg/husage/internal/tui"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "husage:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("husage", flag.ContinueOnError)
	demo := flags.Bool("demo", false, "show sample subscriptions without accessing local accounts")
	once := flags.Bool("once", false, "print a snapshot and exit")
	jsonOut := flags.Bool("json", false, "print subscription data as JSON and exit")
	refresh := flags.Duration("refresh", config.DefaultRefresh, "override saved auto-reload interval for this launch (minimum 5s; API requests at most once per 5m)")
	zone := flags.String("timezone", "", "IANA timezone for reset times (default: system timezone)")
	var dirs profileDirs
	flags.Var(&dirs, "claude-dir", "Claude configuration directory; repeat for multiple subscriptions, or use current")
	profilesPath := flags.String("profiles", "", "JSON profile list (default: ~/.config/husage/profiles.json, if present)")
	width := flags.Int("width", 80, "snapshot width (20–200 columns)")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
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
	var profileActions *tui.ProfileActions
	if *demo {
		provider = subscription.Demo{}
	} else {
		active, profiles, err := claudeProfiles(home, dirs, *profilesPath)
		if err != nil {
			return err
		}
		group := claude.NewProfiles(active, profiles)
		provider = group
		store := profile.Store{Home: home}
		profileActions = &tui.ProfileActions{Directory: store.Directory,
			Login: func(ctx context.Context, name string) tea.ExecCommand { return profile.NewLogin(ctx, store, name) },
			Register: func(ctx context.Context, name string) (string, error) {
				dir, err := store.Register(ctx, name)
				if err == nil {
					group.Add(claude.Options{Home: home, ConfigDir: dir})
				}
				return dir, err
			}}
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
		WithProfileActions(profileActions).
		WithConfigActions(&tui.ConfigActions{Save: configStore.Save})
	_, err = tea.NewProgram(model, tea.WithContext(ctx), tea.WithOutput(out)).Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
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
