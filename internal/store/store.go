package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"

	"codex-auth/internal/authfile"
	"codex-auth/internal/quota"
)

const lockFileName = "codex-auth.lock"

type Account struct {
	Key          string
	Alias        string
	DisplayName  string
	DefaultLabel string
	FilePath     string
	Raw          []byte
	File         authfile.File
	Meta         authfile.Metadata
	Saved        bool
	Current      bool
	Quota        quota.Snapshot
}

type State struct {
	Order  []string                  `json:"order,omitempty"`
	Quotas map[string]quota.Snapshot `json:"quotas,omitempty"`
}

type Discovery struct {
	Accounts []Account
	State    State
}

type Store struct {
	Home string
}

func ResolveHome(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if env := os.Getenv("CODEX_HOME"); env != "" {
		return env, nil
	}

	currentUser, err := user.Current()
	if err != nil {
		return "", err
	}
	return filepath.Join(currentUser.HomeDir, ".codex"), nil
}

func New(home string) *Store {
	return &Store{Home: home}
}

func (s *Store) Discover() (Discovery, error) {
	state, err := s.loadState()
	if err != nil {
		return Discovery{}, err
	}

	accountsDir := s.accountsDir()
	if err := os.MkdirAll(accountsDir, 0o700); err != nil {
		return Discovery{}, err
	}

	currentRaw, err := os.ReadFile(s.authPath())
	if err != nil {
		return Discovery{}, fmt.Errorf("read auth.json: %w", err)
	}
	currentHash := hashBytes(currentRaw)

	entries, err := os.ReadDir(accountsDir)
	if err != nil {
		return Discovery{}, err
	}

	var accounts []Account
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(accountsDir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return Discovery{}, err
		}
		account, err := parseAccount(strings.TrimSuffix(entry.Name(), ".json"), path, raw, true)
		if err != nil {
			return Discovery{}, fmt.Errorf("parse %s: %w", path, err)
		}
		account.Current = account.Key == currentHash
		if snapshot, ok := state.Quotas[account.Key]; ok {
			account.Quota = snapshot
		} else {
			account.Quota = quota.Loading()
		}
		accounts = append(accounts, account)
	}

	if !hasCurrent(accounts) {
		currentAccount, err := parseAccount("", "", currentRaw, false)
		if err != nil {
			return Discovery{}, fmt.Errorf("parse active auth.json: %w", err)
		}
		currentAccount.Current = true
		if snapshot, ok := state.Quotas[currentAccount.Key]; ok {
			currentAccount.Quota = snapshot
		} else {
			currentAccount.Quota = quota.Loading()
		}
		accounts = append(accounts, currentAccount)
	}

	accounts = applyOrder(accounts, state.Order)
	if len(accounts) == 0 {
		return Discovery{}, fmt.Errorf("no accounts found")
	}

	return Discovery{Accounts: accounts, State: state}, nil
}

func (s *Store) SaveState(state State) error {
	if state.Quotas == nil {
		state.Quotas = map[string]quota.Snapshot{}
	}

	if err := os.MkdirAll(s.stateDir(), 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.statePath(), data, 0o600)
}

func (s *Store) SaveAccount(raw []byte, alias string) (string, error) {
	path, err := s.aliasPath(alias)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(s.accountsDir(), 0o700); err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, raw, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) RenameAccount(oldAlias, newAlias string) (string, error) {
	oldPath, err := s.aliasPath(oldAlias)
	if err != nil {
		return "", err
	}
	newPath, err := s.aliasPath(newAlias)
	if err != nil {
		return "", err
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return "", err
	}
	return newPath, nil
}

func (s *Store) DeleteAccount(alias string) error {
	path, err := s.aliasPath(alias)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Store) Activate(account Account, alias string) error {
	release, err := s.acquireLock()
	if err != nil {
		return err
	}
	defer release()

	if err := writeFileAtomic(s.authPath(), account.Raw, 0o600); err != nil {
		return err
	}
	if alias == "" {
		if err := os.Remove(s.currentPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeFileAtomic(s.currentPath(), []byte(alias), 0o644)
}

func (s *Store) ClearCurrentAlias() error {
	if err := os.Remove(s.currentPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Store) authPath() string {
	return filepath.Join(s.Home, "auth.json")
}

func (s *Store) currentPath() string {
	return filepath.Join(s.Home, "current")
}

func (s *Store) accountsDir() string {
	return filepath.Join(s.Home, "accounts")
}

func (s *Store) stateDir() string {
	return filepath.Join(s.Home, "codex-auth")
}

func (s *Store) statePath() string {
	return filepath.Join(s.stateDir(), "state.json")
}

func (s *Store) lockPath() string {
	return filepath.Join(s.Home, lockFileName)
}

func (s *Store) aliasPath(alias string) (string, error) {
	if err := ValidateAlias(alias); err != nil {
		return "", err
	}
	return filepath.Join(s.accountsDir(), alias+".json"), nil
}

func (s *Store) loadState() (State, error) {
	data, err := os.ReadFile(s.statePath())
	if os.IsNotExist(err) {
		return State{Quotas: map[string]quota.Snapshot{}}, nil
	}
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	if state.Quotas == nil {
		state.Quotas = map[string]quota.Snapshot{}
	}
	return state, nil
}

func (s *Store) acquireLock() (func(), error) {
	if err := os.MkdirAll(s.Home, 0o700); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(s.lockPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}

	return func() {
		_ = file.Close()
		_ = os.Remove(s.lockPath())
	}, nil
}

func parseAccount(alias, path string, raw []byte, saved bool) (Account, error) {
	file, err := authfile.Parse(raw)
	if err != nil {
		return Account{}, err
	}
	meta := authfile.DecodeMetadata(file)
	defaultLabel := authfile.DefaultLabel(meta)
	displayName := defaultLabel
	if alias != "" {
		displayName = alias
	}
	return Account{
		Key:          hashBytes(raw),
		Alias:        alias,
		DisplayName:  displayName,
		DefaultLabel: defaultLabel,
		FilePath:     path,
		Raw:          append([]byte(nil), raw...),
		File:         file,
		Meta:         meta,
		Saved:        saved,
	}, nil
}

func ValidateAlias(alias string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if strings.ContainsAny(alias, `/\`) {
		return fmt.Errorf("name cannot include path separators")
	}
	if alias == "." || alias == ".." {
		return fmt.Errorf("name is reserved")
	}
	return nil
}

func AccountNames(accounts []Account) []string {
	names := make([]string, 0, len(accounts))
	for _, account := range accounts {
		if account.Saved {
			names = append(names, account.Alias)
		}
	}
	return names
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(perm); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func hasCurrent(accounts []Account) bool {
	for _, account := range accounts {
		if account.Current {
			return true
		}
	}
	return false
}

func applyOrder(accounts []Account, order []string) []Account {
	if len(order) == 0 {
		sort.SliceStable(accounts, func(i, j int) bool {
			if accounts[i].Current != accounts[j].Current {
				return accounts[i].Current
			}
			return strings.ToLower(accounts[i].DisplayName) < strings.ToLower(accounts[j].DisplayName)
		})
		return accounts
	}

	index := make(map[string]Account, len(accounts))
	for _, account := range accounts {
		index[account.Key] = account
	}

	var ordered []Account
	seen := map[string]bool{}
	for _, key := range order {
		account, ok := index[key]
		if !ok {
			continue
		}
		ordered = append(ordered, account)
		seen[key] = true
	}

	for _, account := range accounts {
		if !seen[account.Key] {
			ordered = append(ordered, account)
		}
	}
	return ordered
}
