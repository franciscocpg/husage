package subscription

import (
	"context"
	"testing"
	"time"
)

type reloadStartProvider struct{ start *time.Time }

func (p reloadStartProvider) Load(ctx context.Context) ([]Account, error) {
	*p.start = ReloadStart(ctx, time.Time{})
	return nil, nil
}

func TestCombinedGivesProvidersOneReloadStart(t *testing.T) {
	var first, second time.Time
	if _, err := (Combined{reloadStartProvider{&first}, reloadStartProvider{&second}}).Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if first.IsZero() || !first.Equal(second) {
		t.Fatal(first, second)
	}
}
