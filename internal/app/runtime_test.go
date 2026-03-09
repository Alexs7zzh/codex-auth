package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex-auth/internal/authfile"
	"codex-auth/internal/quota"
	"codex-auth/internal/store"
)

type stubProvider struct {
	snapshot quota.Snapshot
	err      error
}

func (s stubProvider) Fetch(context.Context, authfile.File) (quota.Snapshot, error) {
	return s.snapshot, s.err
}

func TestLoadCreatesUnmanagedCurrentRow(t *testing.T) {
	home := t.TempDir()
	writeAuthFixture(t, home, "auth.json", authFixture{
		email:     "current@example.com",
		accountID: "acct-current",
	})
	writeAuthFixture(t, home, filepath.Join("accounts", "saved.json"), authFixture{
		email:     "saved@example.com",
		accountID: "acct-saved",
	})

	runtime, accounts, _, err := Load(home, stubProvider{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if runtime == nil {
		t.Fatal("expected runtime")
	}
	if len(accounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(accounts))
	}

	var currentFound bool
	for _, account := range accounts {
		if account.Current {
			currentFound = true
			if account.Saved {
				t.Fatalf("expected current account to be unmanaged")
			}
			if account.DisplayName != "current@example.com" {
				t.Fatalf("unexpected default label %q", account.DisplayName)
			}
		}
	}
	if !currentFound {
		t.Fatal("expected unmanaged current account")
	}
}

func TestCommitAliasSavesUnmanagedCurrent(t *testing.T) {
	home := t.TempDir()
	writeAuthFixture(t, home, "auth.json", authFixture{
		email:     "current@example.com",
		accountID: "acct-current",
	})

	runtime, accounts, _, err := Load(home, stubProvider{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	current := currentAccount(t, accounts)
	updated, err := runtime.CommitAlias(accounts, current, "work")
	if err != nil {
		t.Fatalf("CommitAlias() error = %v", err)
	}
	if !updated.Saved || updated.Alias != "work" {
		t.Fatalf("expected saved alias work, got saved=%v alias=%q", updated.Saved, updated.Alias)
	}

	if _, err := os.Stat(filepath.Join(home, "accounts", "work.json")); err != nil {
		t.Fatalf("expected saved account file: %v", err)
	}
	currentName, err := os.ReadFile(filepath.Join(home, "current"))
	if err != nil {
		t.Fatalf("read current: %v", err)
	}
	if strings.TrimSpace(string(currentName)) != "work" {
		t.Fatalf("expected current alias work, got %q", string(currentName))
	}
}

func TestDeleteCurrentSavedAccountLeavesUnsavedCurrent(t *testing.T) {
	home := t.TempDir()
	writeAuthFixture(t, home, "auth.json", authFixture{
		email:     "current@example.com",
		accountID: "acct-current",
	})
	writeAuthFixture(t, home, filepath.Join("accounts", "work.json"), authFixture{
		email:     "current@example.com",
		accountID: "acct-current",
	})
	if err := os.WriteFile(filepath.Join(home, "current"), []byte("work"), 0o644); err != nil {
		t.Fatalf("write current: %v", err)
	}

	runtime, accounts, _, err := Load(home, stubProvider{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	current := currentAccount(t, accounts)
	result, err := runtime.DeleteAccount(accounts, current)
	if err != nil {
		t.Fatalf("DeleteAccount() error = %v", err)
	}
	if result.Replacement == nil {
		t.Fatal("expected unsaved replacement account")
	}
	if result.Replacement.Saved {
		t.Fatal("expected replacement to be unsaved")
	}
	if _, err := os.Stat(filepath.Join(home, "accounts", "work.json")); !os.IsNotExist(err) {
		t.Fatalf("expected account file to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "current")); !os.IsNotExist(err) {
		t.Fatalf("expected current alias file to be removed, stat err=%v", err)
	}
}

func TestSwitchAccountUpdatesAuthAndCurrent(t *testing.T) {
	home := t.TempDir()
	writeAuthFixture(t, home, "auth.json", authFixture{
		email:     "first@example.com",
		accountID: "acct-first",
	})
	writeAuthFixture(t, home, filepath.Join("accounts", "first.json"), authFixture{
		email:     "first@example.com",
		accountID: "acct-first",
	})
	secondRaw := writeAuthFixture(t, home, filepath.Join("accounts", "second.json"), authFixture{
		email:     "second@example.com",
		accountID: "acct-second",
	})
	if err := os.WriteFile(filepath.Join(home, "current"), []byte("first"), 0o644); err != nil {
		t.Fatalf("write current: %v", err)
	}

	runtime, accounts, _, err := Load(home, stubProvider{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	var targetFound bool
	for _, account := range accounts {
		if account.Alias == "second" {
			targetFound = true
			if err := runtime.SwitchAccount(accounts, account); err != nil {
				t.Fatalf("SwitchAccount() error = %v", err)
			}
		}
	}
	if !targetFound {
		t.Fatal("expected second account")
	}

	activeRaw, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	if string(activeRaw) != string(secondRaw) {
		t.Fatal("expected auth.json to match second account")
	}
	currentName, err := os.ReadFile(filepath.Join(home, "current"))
	if err != nil {
		t.Fatalf("read current: %v", err)
	}
	if strings.TrimSpace(string(currentName)) != "second" {
		t.Fatalf("expected current alias second, got %q", string(currentName))
	}
}

type authFixture struct {
	email     string
	accountID string
}

func writeAuthFixture(t *testing.T, home, relativePath string, fixture authFixture) []byte {
	t.Helper()

	payload := map[string]any{
		"email": fixture.email,
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_plan_type":                 "plus",
			"chatgpt_subscription_active_until": "2026-04-08T08:12:34+00:00",
			"chatgpt_account_id":                fixture.accountID,
			"user_id":                           "user-" + fixture.accountID,
		},
	}
	claims, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	token := "header." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
	data, err := json.MarshalIndent(map[string]any{
		"OPENAI_API_KEY": nil,
		"auth_mode":      "chatgpt",
		"last_refresh":   "2026-03-08T08:15:28.218654Z",
		"tokens": map[string]any{
			"access_token":  "access-" + fixture.accountID,
			"account_id":    fixture.accountID,
			"id_token":      token,
			"refresh_token": "refresh-" + fixture.accountID,
		},
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal auth file: %v", err)
	}

	path := filepath.Join(home, relativePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return data
}

func currentAccount(t *testing.T, accounts []store.Account) store.Account {
	t.Helper()
	for _, account := range accounts {
		if account.Current {
			return account
		}
	}
	t.Fatal("missing current account")
	return store.Account{}
}
