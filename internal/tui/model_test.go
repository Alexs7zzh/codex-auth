package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex-auth/internal/app"
	"codex-auth/internal/quota"
	"codex-auth/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

type fakeController struct {
	refreshResult map[string]quota.Snapshot
	commitResult  store.Account
	deleteResult  app.DeleteResult
	switchKey     string
	lastAlias     string
}

func (f *fakeController) RefreshAll(context.Context, []store.Account) map[string]quota.Snapshot {
	return f.refreshResult
}

func (f *fakeController) CommitAlias(_ []store.Account, _ store.Account, alias string) (store.Account, error) {
	f.lastAlias = alias
	result := f.commitResult
	if result.DisplayName == "" {
		result.DisplayName = alias
	}
	if result.Alias == "" {
		result.Alias = alias
	}
	result.Saved = true
	return result, nil
}

func (f *fakeController) DeleteAccount(_ []store.Account, _ store.Account) (app.DeleteResult, error) {
	return f.deleteResult, nil
}

func (f *fakeController) SwitchAccount(_ []store.Account, account store.Account) error {
	f.switchKey = account.Key
	return nil
}

func TestEditModeCommitsAlias(t *testing.T) {
	controller := &fakeController{
		commitResult: sampleAccount("a", false, true),
	}
	model := NewModel(controller, []store.Account{sampleAccount("a", false, true)}, "")

	next, _ := model.Update(keyRunes("e"))
	model = next.(Model)
	if model.mode != modeEdit {
		t.Fatalf("expected edit mode, got %v", model.mode)
	}

	model.input.SetValue("renamed")
	next, _ = model.Update(keyEnter())
	model = next.(Model)
	if model.mode != modeNormal {
		t.Fatalf("expected normal mode after commit, got %v", model.mode)
	}
	if controller.lastAlias != "renamed" {
		t.Fatalf("expected alias commit, got %q", controller.lastAlias)
	}
}

func TestDeleteConfirmationMode(t *testing.T) {
	account := sampleAccount("a", true, false)
	controller := &fakeController{
		deleteResult: app.DeleteResult{RemovedKey: account.Key},
	}
	model := NewModel(controller, []store.Account{account}, "")

	next, _ := model.Update(keyRunes("d"))
	model = next.(Model)
	if model.mode != modeDeleteConfirm {
		t.Fatalf("expected delete confirm mode, got %v", model.mode)
	}

	next, _ = model.Update(keyEnter())
	model = next.(Model)
	if model.mode != modeNormal {
		t.Fatalf("expected normal mode after delete, got %v", model.mode)
	}
	if len(model.accounts) != 0 {
		t.Fatalf("expected account removed, got %d", len(model.accounts))
	}
}

func TestSpaceMarksTargetAndEnterSwitchesMarkedAccount(t *testing.T) {
	current := sampleAccount("current", true, true)
	target := sampleAccount("target", true, false)
	controller := &fakeController{}
	model := NewModel(controller, []store.Account{current, target}, "")

	next, _ := model.Update(keyDown())
	model = next.(Model)
	next, _ = model.Update(keySpace())
	model = next.(Model)
	if model.markedKey != target.Key {
		t.Fatalf("expected marked key %q, got %q", target.Key, model.markedKey)
	}

	next, _ = model.Update(keyUp())
	model = next.(Model)
	_, cmd := model.Update(keyEnter())
	if controller.switchKey != target.Key {
		t.Fatalf("expected switch target %q, got %q", target.Key, controller.switchKey)
	}
	if cmd == nil {
		t.Fatal("expected quit command")
	}
}

func TestViewKeepsRowHeightAfterRefresh(t *testing.T) {
	account := sampleAccount("a", true, true)
	controller := &fakeController{
		refreshResult: map[string]quota.Snapshot{
			account.Key: {
				Primary:   quota.Window{Label: "5h", UsedPercent: 40, ResetsAt: time.Now().Add(time.Hour)},
				Secondary: quota.Window{Label: "7d", UsedPercent: 60, ResetsAt: time.Now().Add(7 * time.Hour)},
			},
		},
	}
	model := NewModel(controller, []store.Account{account}, "")

	before := strings.Count(model.View(), "\n")
	next, _ := model.Update(refreshDoneMsg(controller.refreshResult))
	model = next.(Model)
	after := strings.Count(model.View(), "\n")
	if before != after {
		t.Fatalf("expected stable line count, before=%d after=%d", before, after)
	}
}

func TestViewShowsCachedQuotaWhileLiveRefreshIsLoading(t *testing.T) {
	account := sampleAccount("a", true, true)
	account.Quota = quota.Snapshot{
		Primary: quota.Window{
			Label:       "5h",
			UsedPercent: 40,
			ResetsAt:    time.Now().Add(time.Hour),
		},
		Secondary: quota.Window{
			Label:       "7d",
			UsedPercent: 60,
			ResetsAt:    time.Now().Add(7 * time.Hour),
		},
		HasData:   true,
		Loading:   true,
		CheckedAt: time.Now(),
	}

	model := NewModel(&fakeController{}, []store.Account{account}, "")
	view := model.View()

	if !strings.Contains(view, "loading") {
		t.Fatal("expected loading spinner text in header")
	}
	if strings.Contains(view, "checking quota") {
		t.Fatal("expected cached quota lines to stay visible while loading")
	}
	if !strings.Contains(view, "60% left") {
		t.Fatal("expected remaining cached quota percentage to be rendered")
	}
}

func TestQuotaBarWidthPrefers24AndShrinksForNarrowTerminal(t *testing.T) {
	account := sampleAccount("a", true, true)
	account.Quota = quota.Snapshot{
		Primary: quota.Window{
			Label:       "5h",
			UsedPercent: 40,
			ResetsAt:    time.Date(2026, 3, 9, 17, 16, 0, 0, time.Local),
		},
		Secondary: quota.Window{
			Label:       "7d",
			UsedPercent: 95,
			ResetsAt:    time.Date(2026, 3, 10, 11, 43, 0, 0, time.Local),
		},
		HasData: true,
	}

	model := NewModel(&fakeController{}, []store.Account{account}, "")
	if got := model.quotaBarWidth(account); got != 24 {
		t.Fatalf("expected preferred bar width 24, got %d", got)
	}

	model.width = 40
	if got := model.quotaBarWidth(account); got >= 24 {
		t.Fatalf("expected shrunk bar width below 24, got %d", got)
	}
}

func sampleAccount(name string, saved, current bool) store.Account {
	return store.Account{
		Key:          name + "-key",
		Alias:        name,
		DisplayName:  name,
		DefaultLabel: name + "@example.com",
		Saved:        saved,
		Current:      current,
		Quota:        quota.Loading(),
	}
}

func keyRunes(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}

func keyEnter() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyEnter}
}

func keySpace() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeySpace}
}

func keyDown() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyDown}
}

func keyUp() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyUp}
}
