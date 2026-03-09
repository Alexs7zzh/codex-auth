package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"codex-auth/internal/app"
	"codex-auth/internal/quota"
	"codex-auth/internal/store"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type errMsg struct {
	err error
}

type mode int

const (
	modeNormal mode = iota
	modeEdit
	modeDeleteConfirm
)

type Model struct {
	controller appController
	accounts   []store.Account
	cursor     int
	markedKey  string
	mode       mode
	input      textinput.Model
	errorText  string
	warning    string
	quitting   bool
}

type appController interface {
	RefreshAll(context.Context, []store.Account) map[string]quota.Snapshot
	CommitAlias([]store.Account, store.Account, string) (store.Account, error)
	DeleteAccount([]store.Account, store.Account) (app.DeleteResult, error)
	SwitchAccount([]store.Account, store.Account) error
}

func NewModel(controller appController, accounts []store.Account, warning string) Model {
	input := textinput.New()
	input.Prompt = ""
	input.CharLimit = 128
	input.Width = 48

	return Model{
		controller: controller,
		accounts:   accounts,
		input:      input,
		warning:    warning,
	}
}

func Run(ctx context.Context, model Model) error {
	_ = ctx
	program := tea.NewProgram(model)
	_, err := program.Run()
	return err
}

func (m Model) Init() tea.Cmd {
	accounts := append([]store.Account(nil), m.accounts...)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return refreshDoneMsg(m.controller.RefreshAll(ctx, accounts))
	}
}

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.KeyMsg:
		return m.updateKey(message)
	case refreshDoneMsg:
		for i := range m.accounts {
			if snapshot, ok := message[m.accounts[i].Key]; ok {
				m.accounts[i].Quota = snapshot
			}
		}
	case tea.WindowSizeMsg:
	case errMsg:
		m.errorText = message.err.Error()
	}
	return m, nil
}

type refreshDoneMsg map[string]quota.Snapshot

func (m Model) updateKey(message tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeEdit:
		return m.updateEdit(message)
	case modeDeleteConfirm:
		return m.updateDeleteConfirm(message)
	}

	switch message.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.accounts)-1 {
			m.cursor++
		}
	case " ":
		m.markedKey = m.selected().Key
		m.errorText = ""
	case "e", "i":
		selected := m.selected()
		m.mode = modeEdit
		m.input.SetValue(selected.DisplayName)
		m.input.CursorEnd()
		m.input.Focus()
		m.errorText = ""
	case "d":
		if !m.selected().Saved {
			m.errorText = "unsaved current account cannot be deleted"
			return m, nil
		}
		m.mode = modeDeleteConfirm
		m.errorText = ""
	case "enter":
		selected := m.targetAccount()
		if selected.Current {
			if !selected.Saved {
				updated, err := m.controller.CommitAlias(m.accounts, selected, selected.DefaultLabel)
				if err != nil {
					m.errorText = err.Error()
					return m, nil
				}
				m.replaceAccount(updated)
			}
			m.quitting = true
			return m, tea.Quit
		}
		if err := m.controller.SwitchAccount(m.accounts, selected); err != nil {
			m.errorText = err.Error()
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit
	case "esc", "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	}

	return m, nil
}

func (m Model) updateEdit(message tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch message.String() {
	case "esc":
		m.mode = modeNormal
		m.input.Blur()
		m.errorText = ""
		return m, nil
	case "enter":
		selected := m.selected()
		updated, err := m.controller.CommitAlias(m.accounts, selected, m.input.Value())
		if err != nil {
			m.errorText = err.Error()
			return m, nil
		}
		m.replaceAccount(updated)
		m.mode = modeNormal
		m.input.Blur()
		m.errorText = ""
		return m, nil
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(message)
		return m, cmd
	}
}

func (m Model) updateDeleteConfirm(message tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch message.String() {
	case "esc":
		m.mode = modeNormal
		m.errorText = ""
		return m, nil
	case "enter":
		selected := m.selected()
		result, err := m.controller.DeleteAccount(m.accounts, selected)
		if err != nil {
			m.errorText = err.Error()
			return m, nil
		}
		m.accounts = deleteByKey(m.accounts, result.RemovedKey)
		if result.Replacement != nil {
			m.accounts = append(m.accounts, *result.Replacement)
		}
		m.cursor = clampCursor(m.accounts, m.cursor)
		m.mode = modeNormal
		m.errorText = ""
		return m, nil
	}
	return m, nil
}

func (m Model) View() string {
	if len(m.accounts) == 0 {
		return "no codex accounts found"
	}

	var lines []string
	lines = append(lines, lipgloss.NewStyle().Bold(true).Render("codex-auth"))
	lines = append(lines, "up/down,j/k move  space mark  enter confirm  e/i edit  d delete  esc/q close")
	if m.warning != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render(m.warning))
	}
	if m.errorText != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(m.errorText))
	}
	lines = append(lines, "")

	for index, account := range m.accounts {
		lines = append(lines, m.renderAccount(index, account)...)
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderAccount(index int, account store.Account) []string {
	pointer := " "
	if index == m.cursor {
		pointer = ">"
	}

	saveIcon := "[ ]"
	if account.Saved {
		saveIcon = "[*]"
	}

	markers := make([]string, 0, 2)
	if account.Current {
		markers = append(markers, "current")
	}
	if m.markedKey == account.Key && !account.Current {
		markers = append(markers, "target")
	}

	label := account.DisplayName
	if m.mode == modeEdit && index == m.cursor {
		label = m.input.View()
	}
	header := fmt.Sprintf("%s %s %s", pointer, saveIcon, label)
	if len(markers) > 0 {
		header += "  " + strings.Join(markers, ", ")
	}
	if account.Meta.PlanType != "" {
		header += "  " + strings.ToUpper(account.Meta.PlanType)
	}

	primary := quotaLine("5h", account.Quota.Primary.UsedPercent, account.Quota.Primary.ResetsAt, account.Quota.Loading, account.Quota.Error)
	secondary := quotaLine("7d", account.Quota.Secondary.UsedPercent, account.Quota.Secondary.ResetsAt, account.Quota.Loading, account.Quota.Error)
	if account.Quota.Primary.Label != "" {
		primary = quotaLine(account.Quota.Primary.Label, account.Quota.Primary.UsedPercent, account.Quota.Primary.ResetsAt, account.Quota.Loading, account.Quota.Error)
	}
	if account.Quota.Secondary.Label != "" {
		secondary = quotaLine(account.Quota.Secondary.Label, account.Quota.Secondary.UsedPercent, account.Quota.Secondary.ResetsAt, account.Quota.Loading, account.Quota.Error)
	}
	if m.mode == modeDeleteConfirm && index == m.cursor {
		secondary = "  press Enter to delete, Esc to cancel"
	}

	return []string{header, primary, secondary}
}

func quotaLine(label string, percent float64, reset time.Time, loading bool, errText string) string {
	if loading {
		return fmt.Sprintf("  %s  [..........] checking quota", label)
	}
	bar := renderBar(percent)
	if errText != "" {
		return fmt.Sprintf("  %s  %s unavailable", label, bar)
	}
	resetText := "unknown"
	if !reset.IsZero() {
		resetText = reset.Local().Format("Jan _2 15:04")
	}
	return fmt.Sprintf("  %s  %s %3.0f%%  resets %s", label, bar, percent, resetText)
}

func renderBar(percent float64) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	filled := int((percent / 100) * 10)
	if filled > 10 {
		filled = 10
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", 10-filled) + "]"
}

func (m *Model) selected() store.Account {
	return m.accounts[m.cursor]
}

func (m *Model) targetAccount() store.Account {
	if m.markedKey == "" {
		return m.selected()
	}
	for _, account := range m.accounts {
		if account.Key == m.markedKey {
			return account
		}
	}
	return m.selected()
}

func (m *Model) replaceAccount(updated store.Account) {
	for i := range m.accounts {
		if m.accounts[i].Key == updated.Key {
			m.accounts[i] = updated
			return
		}
	}
}

func deleteByKey(accounts []store.Account, key string) []store.Account {
	next := make([]store.Account, 0, len(accounts))
	for _, account := range accounts {
		if account.Key == key {
			continue
		}
		next = append(next, account)
	}
	return next
}

func clampCursor(accounts []store.Account, current int) int {
	if len(accounts) == 0 {
		return 0
	}
	if current >= len(accounts) {
		return len(accounts) - 1
	}
	return current
}
