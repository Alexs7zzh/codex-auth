package app

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"codex-auth/internal/quota"
	"codex-auth/internal/store"
	"golang.org/x/sync/errgroup"
)

type DeleteResult struct {
	RemovedKey   string
	Replacement  *store.Account
	SelectedKey  string
	Warning      string
	StateChanged bool
}

type Runtime struct {
	store    *store.Store
	provider quota.Provider
	state    store.State
}

func Load(home string, provider quota.Provider) (*Runtime, []store.Account, string, error) {
	fileStore := store.New(home)
	discovery, err := fileStore.Discover()
	if err != nil {
		return nil, nil, "", err
	}

	if localSnapshot, ok := quota.LoadRecentLocalSnapshot(home); ok {
		for i := range discovery.Accounts {
			if discovery.Accounts[i].Current && !discovery.Accounts[i].Quota.HasData {
				discovery.Accounts[i].Quota = localSnapshot
			}
		}
	}

	warning := codexWarning()
	return &Runtime{
		store:    fileStore,
		provider: provider,
		state:    discovery.State,
	}, discovery.Accounts, warning, nil
}

func (r *Runtime) RefreshAll(ctx context.Context, accounts []store.Account) map[string]quota.Snapshot {
	results := make(map[string]quota.Snapshot, len(accounts))
	type item struct {
		key      string
		snapshot quota.Snapshot
	}

	eg, groupCtx := errgroup.WithContext(ctx)
	eg.SetLimit(4)

	channel := make(chan item, len(accounts))
	for _, account := range accounts {
		account := account
		eg.Go(func() error {
			snapshot, err := r.provider.Fetch(groupCtx, account.File)
			if err != nil {
				snapshot = account.Quota
				snapshot.Loading = false
				snapshot.Stale = true
				snapshot.Source = firstNonEmpty(snapshot.Source, "cached")
				if snapshot.HasData {
					snapshot.Error = err.Error()
				} else {
					snapshot = quota.Empty("")
				}
			}
			channel <- item{key: account.Key, snapshot: snapshot}
			return nil
		})
	}

	_ = eg.Wait()
	close(channel)

	for result := range channel {
		results[result.key] = result.snapshot
		if r.state.Quotas == nil {
			r.state.Quotas = map[string]quota.Snapshot{}
		}
		r.state.Quotas[result.key] = result.snapshot
	}

	_ = r.persistOrder(accounts)
	return results
}

func (r *Runtime) CommitAlias(accounts []store.Account, account store.Account, alias string) (store.Account, error) {
	alias = strings.TrimSpace(alias)
	if err := store.ValidateAlias(alias); err != nil {
		return store.Account{}, err
	}
	if duplicateAlias(accounts, account, alias) {
		return store.Account{}, fmt.Errorf("name already exists")
	}

	var path string
	var err error
	switch {
	case account.Saved && account.Alias == alias:
		path = account.FilePath
	case account.Saved:
		path, err = r.store.RenameAccount(account.Alias, alias)
	case !account.Saved:
		path, err = r.store.SaveAccount(account.Raw, alias)
	default:
		err = fmt.Errorf("unsupported account state")
	}
	if err != nil {
		return store.Account{}, err
	}

	account.Saved = true
	account.Alias = alias
	account.DisplayName = alias
	account.FilePath = path

	if account.Current {
		if err := r.store.Activate(account, alias); err != nil {
			return store.Account{}, err
		}
	}

	if err := r.persistOrder(replaceAccount(accounts, account)); err != nil {
		return store.Account{}, err
	}
	return account, nil
}

func (r *Runtime) DeleteAccount(accounts []store.Account, account store.Account) (DeleteResult, error) {
	if !account.Saved {
		return DeleteResult{}, fmt.Errorf("unsaved account cannot be deleted")
	}
	if err := r.store.DeleteAccount(account.Alias); err != nil {
		return DeleteResult{}, err
	}

	result := DeleteResult{
		RemovedKey:   account.Key,
		SelectedKey:  account.Key,
		StateChanged: true,
	}

	if account.Current {
		account.Saved = false
		account.Alias = ""
		account.DisplayName = account.DefaultLabel
		account.FilePath = ""
		if err := r.store.ClearCurrentAlias(); err != nil {
			return DeleteResult{}, err
		}
		result.Replacement = &account
		result.SelectedKey = account.Key
	}

	nextAccounts := removeAccount(accounts, account.Key)
	if result.Replacement != nil {
		nextAccounts = append(nextAccounts, *result.Replacement)
	}
	if err := r.persistOrder(nextAccounts); err != nil {
		return DeleteResult{}, err
	}
	return result, nil
}

func (r *Runtime) SwitchAccount(accounts []store.Account, account store.Account) error {
	alias := ""
	if account.Saved {
		alias = account.Alias
	}
	if err := r.store.Activate(account, alias); err != nil {
		return err
	}

	next := make([]store.Account, len(accounts))
	copy(next, accounts)
	for i := range next {
		next[i].Current = next[i].Key == account.Key
	}
	return r.persistOrder(next)
}

func (r *Runtime) persistOrder(accounts []store.Account) error {
	order := make([]string, 0, len(accounts))
	for _, account := range accounts {
		order = append(order, account.Key)
	}
	r.state.Order = order
	return r.store.SaveState(r.state)
}

func duplicateAlias(accounts []store.Account, current store.Account, alias string) bool {
	for _, account := range accounts {
		if !account.Saved {
			continue
		}
		if account.Key == current.Key && strings.EqualFold(account.Alias, alias) {
			continue
		}
		if strings.EqualFold(account.Alias, alias) {
			return true
		}
	}
	return false
}

func replaceAccount(accounts []store.Account, updated store.Account) []store.Account {
	next := make([]store.Account, len(accounts))
	copy(next, accounts)
	for i := range next {
		if next[i].Key == updated.Key {
			next[i] = updated
		}
	}
	return next
}

func removeAccount(accounts []store.Account, key string) []store.Account {
	next := make([]store.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Key == key {
			continue
		}
		next = append(next, account)
	}
	return next
}

func codexWarning() string {
	output, err := exec.Command("pgrep", "-x", "codex").CombinedOutput()
	if err == nil && strings.TrimSpace(string(output)) != "" {
		return "warning: codex is running; switching affects new sessions"
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
