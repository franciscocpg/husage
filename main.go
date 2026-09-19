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
	refresh := flags.Duration("refresh", 30*time.Second, "local refresh interval (minimum 5s; API requests at most once per 5m)")
	zone := flags.String("timezone", "", "IANA timezone for reset times (default: system timezone)")
	config := flags.String("claude-dir", os.Getenv("CLAUDE_CONFIG_DIR"), "Claude configuration directory")
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
	if *demo {
		provider = subscription.Demo{}
	} else {
		provider = claude.New(claude.Options{Home: home, ConfigDir: expand(*config, home), SecureDir: expand(os.Getenv("CLAUDE_SECURESTORAGE_CONFIG_DIR"), home)})
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if *once || *jsonOut {
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
	_, err = tea.NewProgram(tui.New(ctx, provider, *refresh, loc, *demo), tea.WithContext(ctx), tea.WithOutput(out)).Run()
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
