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

		acc, acquired, woken := p.waitForSlot(ctx, waiter, target, exclude)
		if acquired {
			return acc, true
		}
		if !woken {
			return config.Account{}, false
		}
		// Woken by a release: notifyWaiterLocked already dequeued us, so the
		// loop re-queues from the top.
	}
}

// waitForSlot blocks until the account can be acquired, the caller gives up, or
// a release wakes this waiter.
//
// Quarantine windows and the rolling hourly budget lapse with time rather than
// signalling, so the wait also re-checks on a timer. The queue slot is held
// across those re-checks: dropping it and re-taking it at the loop top lets a
// newcomer claim it, which refuses the caller that has waited longest.
func (p *Pool) waitForSlot(ctx context.Context, waiter chan struct{}, target string, exclude map[string]bool) (config.Account, bool, bool) {
	for {
		recheck := time.NewTimer(waiterRecheckInterval)
		select {
		case <-ctx.Done():
			recheck.Stop()
			p.mu.Lock()
			p.removeWaiterLocked(waiter)
			p.mu.Unlock()
			return config.Account{}, false, false

		case <-waiter:
			recheck.Stop()
			return config.Account{}, false, true

		case <-recheck.C:
			p.mu.Lock()
			acc, ok := p.acquireLocked(target, exclude)
			if !ok {
				// Slot deliberately retained; keep waiting.
				p.mu.Unlock()
				continue
			}
			p.removeWaiterLocked(waiter)
			inflight := p.inUse[acc.Identifier()]
			p.mu.Unlock()
			logAcquire(acc.Identifier(), inflight)
			return acc, true, false
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
