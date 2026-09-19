package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/franciscocpg/husage/internal/subscription"
)

type identity struct {
	AccountID string `json:"accountUuid"`
	OrgID     string `json:"organizationUuid"`
	Name      string `json:"organizationName"`
	Email     string `json:"emailAddress"`
}

func (i identity) key() string { return i.AccountID + ":" + i.OrgID }
func (i identity) valid() bool { return i.AccountID != "" && i.OrgID != "" }

func readIdentity(path string) (identity, error) {
	var config struct {
		Account identity `json:"oauthAccount"`
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return identity{}, err
	}
	if err := json.Unmarshal(b, &config); err != nil {
		return identity{}, errors.New("could not parse Claude account metadata")
	}
	return config.Account, nil
}

type switcherProfile struct {
	Name      string `json:"name"`
	Email     string `json:"email"`
	AccountID string `json:"account_uuid"`
	OrgID     string `json:"org_id"`
	OrgName   string `json:"org_name"`
}
type cachedUsage struct {
	CheckedAt    time.Time  `json:"checked_at"`
	Session      *float64   `json:"five_hour_pct"`
	Week         *float64   `json:"seven_day_pct"`
	Model        *float64   `json:"model_pct"`
	ModelLabel   string     `json:"model_label"`
	SessionReset *time.Time `json:"reset_at"`
	WeekReset    *time.Time `json:"seven_day_reset_at"`
	ModelReset   *time.Time `json:"model_reset_at"`
	Source       string     `json:"source"`
}

// Read the switcher's existing telemetry only. Never rotate accounts, refresh
// OAuth tokens, or launch inference requests to obtain usage.
func loadSwitcher(dir string, active identity) ([]subscription.Account, error) {
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return nil, err
	}
	var state struct {
		Profiles []switcherProfile      `json:"profiles"`
		Usage    map[string]cachedUsage `json:"usage_cache"`
	}
	if json.Unmarshal(b, &state) != nil {
		return nil, errors.New("could not parse Claude switcher state; press r to retry")
	}
	accounts := make([]subscription.Account, 0, len(state.Profiles))
	seen := map[string]bool{}
	for _, p := range state.Profiles {
		id := identity{AccountID: p.AccountID, OrgID: p.OrgID}
		key := id.key()
		if !id.valid() {
			key = "profile:" + p.Name
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		name := p.OrgName
		if name == "" {
			name = p.Name
		}
		a := subscription.Account{ID: key, Provider: "Claude Code", Name: name, Email: p.Email, Active: active.valid() && active.key() == key, Source: "switcher"}
		if u, ok := state.Usage[p.Name]; ok {
			a.UpdatedAt = u.CheckedAt
			if u.Source != "" {
				a.Source = "switcher · " + u.Source
			}
			add := func(label string, used *float64, reset *time.Time) {
				if used == nil {
					return
				}
				w := subscription.Window{Label: label, Used: *used}
				if reset != nil {
					w.ResetsAt = *reset
				}
				a.Windows = append(a.Windows, w)
			}
			add("Current session", u.Session, u.SessionReset)
			add("Current week (all models)", u.Week, u.WeekReset)
			if u.ModelLabel != "" {
				add(fmt.Sprintf("Current week (%s)", u.ModelLabel), u.Model, u.ModelReset)
			}
		}
		if len(a.Windows) == 0 {
			a.Error = "No usage reported yet. Open Claude Code or wait for the switcher."
		}
		accounts = append(accounts, a)
	}
	sort.SliceStable(accounts, func(i, j int) bool {
		if accounts[i].Active != accounts[j].Active {
			return accounts[i].Active
		}
		return accounts[i].Name < accounts[j].Name
	})
	return accounts, nil
}
