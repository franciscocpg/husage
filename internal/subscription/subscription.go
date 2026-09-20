// Package subscription defines the provider-neutral data displayed by husage.
package subscription

import (
	"context"
	"time"
)

type Window struct {
	Label    string    `json:"label"`
	Used     float64   `json:"used_percent"`
	ResetsAt time.Time `json:"resets_at,omitzero"`
}

type Account struct {
	ID            string       `json:"id"`
	Provider      string       `json:"provider"`
	Name          string       `json:"name"`
	Email         string       `json:"email"`
	Active        bool         `json:"active"`
	Windows       []Window     `json:"windows"`
	UpdatedAt     time.Time    `json:"updated_at"`
	Source        string       `json:"source"`
	Error         string       `json:"error,omitempty"`
	Warning       string       `json:"warning,omitempty"` // Concise recovery guidance for the dashboard.
	LoginRequired bool         `json:"login_required,omitempty"`
	Login         *LoginTarget `json:"-"` // Native profile selected by the provider, never inferred from a label.
	Stale         bool         `json:"stale,omitempty"`
}

type LoginTarget struct {
	Provider        string
	Directory       string
	SecureDirectory string
}

// LoginRefresher bypasses only the selected native profile's cooldown after login.
type LoginRefresher interface {
	LoginSucceeded(LoginTarget)
}

// Provider keeps authentication and data acquisition outside the terminal UI.
type Provider interface {
	Load(context.Context) ([]Account, error)
}
