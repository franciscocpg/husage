package subscription

import (
	"context"
	"time"
)

type Demo struct{}

func (Demo) Load(context.Context) ([]Account, error) {
	now := time.Now()
	return []Account{
		{ID: "demo-work", Provider: "Claude Code", Name: "Acme Team", Email: "you@example.com", Active: true, UpdatedAt: now, Source: "demo · sample data", Windows: []Window{
			{Label: "Current session", Used: 38, ResetsAt: now.Add(2 * time.Hour)},
			{Label: "Current week (all models)", Used: 48, ResetsAt: now.Add(4 * 24 * time.Hour)},
			{Label: "Current week (Fable)", Used: 63, ResetsAt: now.Add(4 * 24 * time.Hour)},
		}},
		{ID: "demo-personal", Provider: "Claude Code", Name: "Personal · Max", Email: "you@personal.dev", UpdatedAt: now, Source: "demo · sample data", Windows: []Window{
			{Label: "Current session", Used: 8, ResetsAt: now.Add(4 * time.Hour)},
			{Label: "Current week (all models)", Used: 82, ResetsAt: now.Add(2 * 24 * time.Hour)},
		}},
	}, nil
}
