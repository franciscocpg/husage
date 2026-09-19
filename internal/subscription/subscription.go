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
	ID        string    `json:"id"`
	Provider  string    `json:"provider"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	Active    bool      `json:"active"`
	Windows   []Window  `json:"windows"`
	UpdatedAt time.Time `json:"updated_at"`
	Source    string    `json:"source"`
	Error     string    `json:"error,omitempty"`
	Stale     bool      `json:"stale,omitempty"`
}

// Provider keeps authentication and data acquisition outside the terminal UI.
type Provider interface {
	Load(context.Context) ([]Account, error)
}
