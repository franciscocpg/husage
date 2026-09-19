package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/franciscocpg/husage/internal/subscription"
)

const dashboardURL = "https://api2.cursor.sh/aiserver.v1.DashboardService/"

type identity struct {
	AuthID   string `json:"authId"`
	UserID   int64  `json:"userId"`
	Email    string `json:"email"`
	TeamID   int64  `json:"teamId"`
	TeamName string `json:"teamName"`
}

func (i identity) key() string { return fmt.Sprintf("cursor:%s:%d:%d", i.AuthID, i.UserID, i.TeamID) }
func (i identity) valid() bool { return i.AuthID != "" || i.UserID > 0 }

type usage struct {
	BillingCycleEnd json.Number `json:"billingCycleEnd"` // protobuf int64: epoch milliseconds
	Plan            *struct {
		TotalPercent *float64 `json:"totalPercentUsed"`
		AutoPercent  *float64 `json:"autoPercentUsed"`
		APIPercent   *float64 `json:"apiPercentUsed"`
		Included     float64  `json:"includedSpend"` // protobuf scalar: omitted means zero
		Limit        *float64 `json:"limit"`
	} `json:"planUsage"`
	Spend *struct {
		Used  float64  `json:"individualUsed"` // protobuf scalar: omitted means zero
		Limit *float64 `json:"individualLimit"`
	} `json:"spendLimitUsage"`
}

func (u usage) windows() ([]subscription.Window, error) {
	var reset time.Time
	if u.BillingCycleEnd != "" {
		ms, err := u.BillingCycleEnd.Int64()
		if err != nil || ms < 0 {
			return nil, errors.New("Cursor returned an invalid billing cycle.")
		}
		if ms > 0 {
			reset = time.UnixMilli(ms)
		}
	}
	var windows []subscription.Window
	add := func(label string, percent *float64) error {
		if percent == nil {
			return nil
		}
		if math.IsNaN(*percent) || math.IsInf(*percent, 0) || *percent < 0 {
			return errors.New("Cursor returned an invalid usage percentage.")
		}
		windows = append(windows, subscription.Window{Label: label, Used: *percent, ResetsAt: reset})
		return nil
	}
	if u.Plan != nil {
		total := u.Plan.TotalPercent
		if total == nil && u.Plan.Limit != nil && *u.Plan.Limit > 0 {
			percentage := u.Plan.Included / *u.Plan.Limit * 100
			total = &percentage
		}
		for _, w := range []struct {
			label   string
			percent *float64
		}{{"Included usage", total}, {"Auto usage", u.Plan.AutoPercent}, {"API-model usage", u.Plan.APIPercent}} {
			if err := add(w.label, w.percent); err != nil {
				return nil, err
			}
		}
	}
	if u.Spend != nil && u.Spend.Limit != nil && *u.Spend.Limit > 0 {
		percent := u.Spend.Used / *u.Spend.Limit * 100
		if err := add("On-demand spend limit", &percent); err != nil {
			return nil, err
		}
	}
	if len(windows) == 0 {
		return nil, errors.New("Cursor did not report percentage-based usage limits for this plan. Check cursor.com/dashboard for spend details.")
	}
	return windows, nil
}

// These are read-only Connect RPCs used by Cursor CLI's usage display. No agent
// session or inference request is created, and no billing setting is changed.
func (p *Provider) call(ctx context.Context, method, token string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url+method, bytes.NewBufferString("{}"))
	if err != nil {
		return errors.New("Cannot prepare Cursor usage request.")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("User-Agent", "husage/0.1.0")
	resp, err := p.client.Do(req)
	if err != nil {
		return errors.New("Cannot reach Cursor usage. Retrying in 5 minutes.")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return errors.New("Cursor rejected this login. Open Cursor CLI to renew it, or run cursor-agent login, then press r.")
	case http.StatusTooManyRequests:
		delay := 5 * time.Minute
		if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 && seconds <= 86400 {
			delay = max(delay, time.Duration(seconds)*time.Second)
		} else if when, err := http.ParseTime(resp.Header.Get("Retry-After")); err == nil {
			delay = max(delay, when.Sub(p.now()))
		}
		p.next = p.now().Add(delay)
		return fmt.Errorf("Cursor rate limit; retry after %s.", p.next.Local().Format("3:04pm"))
	default:
		return fmt.Errorf("Cursor usage unavailable (HTTP %d). Retrying in 5 minutes.", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result); err != nil {
		return errors.New("Cursor returned an invalid usage response.")
	}
	return nil
}
