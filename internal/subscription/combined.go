package subscription

import (
	"context"
	"errors"
	"sync"
)

// Combined keeps each provider's cards visible when another provider fails.
type Combined []Provider

func (providers Combined) LoginSucceeded(target LoginTarget) {
	for _, p := range providers {
		if p, ok := p.(LoginRefresher); ok {
			p.LoginSucceeded(target)
		}
	}
}

func (providers Combined) Load(ctx context.Context) ([]Account, error) {
	items := make([][]Account, len(providers))
	errs := make([]error, len(providers))
	var wg sync.WaitGroup
	for i, p := range providers {
		wg.Add(1)
		go func(i int, p Provider) { defer wg.Done(); items[i], errs[i] = p.Load(ctx) }(i, p)
	}
	wg.Wait()
	var accounts []Account
	for _, list := range items {
		accounts = append(accounts, list...)
	}
	if len(accounts) > 0 {
		return accounts, nil
	}
	return accounts, errors.Join(errs...)
}
