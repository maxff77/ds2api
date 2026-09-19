package account

import (
	"sort"
	"sync"
	"time"

	"ds2api/internal/config"
)

type Pool struct {
	store                  *config.Store
	mu                     sync.Mutex
	queue                  []string
	inUse                  map[string]int
	waiters                []chan struct{}
	maxInflightPerAccount  int
	recommendedConcurrency int
	maxQueueSize           int
	globalMaxInflight      int
	quarantine             *Quarantine
	rateLimiter            *RateLimiter
}

func NewPool(store *config.Store) *Pool {
	maxPer := 2
	budget := 0
	if store != nil {
		maxPer = store.RuntimeAccountMaxInflight()
		budget = store.RuntimeAccountMaxPerHour()
	}
	p := &Pool{
		store:                 store,
		inUse:                 map[string]int{},
		maxInflightPerAccount: maxPer,
		quarantine:            NewQuarantine(24 * time.Hour),
		rateLimiter:           NewRateLimiter(budget),
	}
	p.Reset()
	return p
}

func (p *Pool) Reset() {
	accounts := p.store.Accounts()
	sort.SliceStable(accounts, func(i, j int) bool {
		iHas := accounts[i].Token != ""
		jHas := accounts[j].Token != ""
		if iHas == jHas {
			return i < j
		}
		return iHas
	})
	ids := make([]string, 0, len(accounts))
	for _, a := range accounts {
		id := a.Identifier()
		if id != "" {
			ids = append(ids, id)
		}
	}
	if p.store != nil {
		p.maxInflightPerAccount = p.store.RuntimeAccountMaxInflight()
		p.rateLimiter.SetBudget(p.store.RuntimeAccountMaxPerHour())
	} else {
		p.maxInflightPerAccount = maxInflightFromEnv()
	}
	recommended := defaultRecommendedConcurrency(len(ids), p.maxInflightPerAccount)
	queueLimit := maxQueueFromEnv(recommended)
	globalLimit := recommended
	if p.store != nil {
		queueLimit = p.store.RuntimeAccountMaxQueue(recommended)
		globalLimit = p.store.RuntimeGlobalMaxInflight(recommended)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.drainWaitersLocked()
	p.queue = ids
	p.inUse = map[string]int{}
	p.recommendedConcurrency = recommended
	p.maxQueueSize = queueLimit
	p.globalMaxInflight = globalLimit
	config.Logger.Info(
		"[init_account_queue] initialized",
		"total", len(ids),
		"max_inflight_per_account", p.maxInflightPerAccount,
		"global_max_inflight", p.globalMaxInflight,
		"recommended_concurrency", p.recommendedConcurrency,
		"max_queue_size", p.maxQueueSize,
	)
}

func (p *Pool) Release(accountID string) {
	if accountID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	count := p.inUse[accountID]
	if count <= 0 {
		return
	}
	if count == 1 {
		delete(p.inUse, accountID)
		p.notifyWaiterLocked()
		return
	}
	p.inUse[accountID] = count - 1
	p.notifyWaiterLocked()
}

func (p *Pool) Status() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	available := make([]string, 0, len(p.queue))
	inUseAccounts := make([]string, 0, len(p.inUse))
	inUseSlots := 0
	quarantined := 0
	for _, id := range p.queue {
		if p.quarantine.Active(id) {
			quarantined++
			continue
		}
		// canAcquireIDLocked is the single gate the pool actually uses, so ask
		// it rather than re-deriving availability and drifting from it.
		if p.canAcquireIDLocked(id) {
			available = append(available, id)
		}
	}
	for id, count := range p.inUse {
		if count > 0 {
			inUseAccounts = append(inUseAccounts, id)
			inUseSlots += count
		}
	}
	sort.Strings(inUseAccounts)
	return map[string]any{
		"available":                len(available),
		"quarantined":              quarantined,
		"in_use":                   inUseSlots,
		"total":                    len(p.store.Accounts()),
		"available_accounts":       available,
		"in_use_accounts":          inUseAccounts,
		"max_inflight_per_account": p.maxInflightPerAccount,
		"global_max_inflight":      p.globalMaxInflight,
		"recommended_concurrency":  p.recommendedConcurrency,
		"waiting":                  len(p.waiters),
		"max_queue_size":           p.maxQueueSize,
	}
}

// QuarantineAccount holds accountID out of rotation until its window lapses.
func (p *Pool) QuarantineAccount(accountID, reason string) {
	p.quarantine.Ban(accountID, reason)
	p.mu.Lock()
	p.notifyWaiterLocked()
	p.mu.Unlock()
}

// ReleaseAccount returns a quarantined account to rotation immediately.
func (p *Pool) ReleaseAccount(accountID string) {
	p.quarantine.Release(accountID)
	p.mu.Lock()
	p.notifyWaiterLocked()
	p.mu.Unlock()
}

// QuarantinedAccounts lists every account currently held out of rotation.
func (p *Pool) QuarantinedAccounts() []BanRecord {
	return p.quarantine.List()
}

// ReleaseUnused returns an account that was acquired but never used to serve a
// request, refunding the hourly budget hit taken at acquisition.
func (p *Pool) ReleaseUnused(accountID string) {
	p.rateLimiter.Refund(accountID)
	p.Release(accountID)
}
