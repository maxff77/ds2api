package account

import (
	"context"
	"time"

	"ds2api/internal/config"
)

// waiterRecheckInterval is how often a queued waiter re-evaluates limits that
// expire on their own rather than signalling.
const waiterRecheckInterval = 250 * time.Millisecond

func (p *Pool) Acquire(target string, exclude map[string]bool) (config.Account, bool) {
	p.mu.Lock()
	acc, ok := p.acquireLocked(target, normalizeExclude(exclude))
	inflight := p.inUse[acc.Identifier()]
	p.mu.Unlock()
	if ok {
		logAcquire(acc.Identifier(), inflight)
	}
	return acc, ok
}

// logAcquire records a hand-off. Deliberately called outside the pool mutex:
// the log sink is I/O and must not run under the pool's global lock.
func logAcquire(accountID string, inflight int) {
	config.Logger.Info("ds_acquire", "account", accountID, "inflight", inflight)
}

func (p *Pool) AcquireWait(ctx context.Context, target string, exclude map[string]bool) (config.Account, bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	exclude = normalizeExclude(exclude)
	for {
		if ctx.Err() != nil {
			return config.Account{}, false
		}

		p.mu.Lock()
		if acc, ok := p.acquireLocked(target, exclude); ok {
			inflight := p.inUse[acc.Identifier()]
			p.mu.Unlock()
			logAcquire(acc.Identifier(), inflight)
			return acc, true
		}
		if !p.canQueueLocked(target, exclude) {
			p.mu.Unlock()
			return config.Account{}, false
		}
		waiter := make(chan struct{})
		p.waiters = append(p.waiters, waiter)
		p.mu.Unlock()

		// Quarantine windows and the rolling hourly budget lapse with time, but
		// waiters are only ever woken by another request releasing. Without a
		// periodic re-check a waiter blocked on a time-based limit sleeps until
		// the caller's deadline -- and an inbound request carries none, so that
		// is "hang until the client disconnects". The same re-check recovers a
		// waiter that consumed a wakeup it could not act on.
		//
		// chisle: a poll, because the limits expire rather than signal. Give
		// Quarantine and RateLimiter a "next change" timer if this ever shows up
		// in a profile.
		recheck := time.NewTimer(waiterRecheckInterval)
		select {
		case <-ctx.Done():
			recheck.Stop()
			p.mu.Lock()
			p.removeWaiterLocked(waiter)
			p.mu.Unlock()
			return config.Account{}, false
		case <-waiter:
			recheck.Stop()
		case <-recheck.C:
			p.mu.Lock()
			p.removeWaiterLocked(waiter)
			p.mu.Unlock()
		}
	}
}

func (p *Pool) acquireLocked(target string, exclude map[string]bool) (config.Account, bool) {
	if target != "" {
		if exclude[target] || !p.canAcquireIDLocked(target) {
			return config.Account{}, false
		}
		acc, ok := p.store.FindAccount(target)
		if !ok {
			return config.Account{}, false
		}
		p.inUse[target]++
		p.bumpQueue(target)
		p.rateLimiter.Record(target)
		return acc, true
	}

	return p.tryAcquire(exclude)
}

func (p *Pool) tryAcquire(exclude map[string]bool) (config.Account, bool) {
	for i := 0; i < len(p.queue); i++ {
		id := p.queue[i]
		if exclude[id] || !p.canAcquireIDLocked(id) {
			continue
		}
		acc, ok := p.store.FindAccount(id)
		if !ok {
			continue
		}
		p.inUse[id]++
		p.bumpQueue(id)
		p.rateLimiter.Record(id)
		return acc, true
	}
	return config.Account{}, false
}

func (p *Pool) bumpQueue(accountID string) {
	for i, id := range p.queue {
		if id != accountID {
			continue
		}
		p.queue = append(p.queue[:i], p.queue[i+1:]...)
		p.queue = append(p.queue, accountID)
		return
	}
}

func normalizeExclude(exclude map[string]bool) map[string]bool {
	if exclude == nil {
		return map[string]bool{}
	}
	return exclude
}
