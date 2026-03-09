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

type mode int

const (
	modeNormal mode = iota
	modeEdit
	modeDeleteConfirm
)

var (
	styleWarning = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	styleError   = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	styleInfo    = lipgloss.NewStyle().Faint(true)
	styleName    = lipgloss.NewStyle().Bold(true)
	styleDim     = lipgloss.NewStyle().Faint(true)
	styleCurrent = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	styleTarget  = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	styleSaved   = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	styleLive    = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	styleDelete  = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	styleCursor  = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	styleKey     = lipgloss.NewStyle().Bold(true)
	styleKeyBlue = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	styleKeyRed  = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	styleBarOn   = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	styleBarOff  = lipgloss.NewStyle().Faint(true)
	styleBarWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Faint(true)
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type appController interface {
	RefreshAll(context.Context, []store.Account) map[string]quota.Snapshot
	CommitAlias([]store.Account, store.Account, string) (store.Account, error)
	DeleteAccount([]store.Account, store.Account) (app.DeleteResult, error)
	SwitchAccount([]store.Account, store.Account) error
}

type Model struct {
	controller appController
	accounts   []store.Account
	cursor     int
	markedKey  string
	mode       mode
	input      textinput.Model
	errorText  string
	warning    string
	spinner    int
}

type refreshDoneMsg map[string]quota.Snapshot
type spinnerTickMsg struct{}

func NewModel(controller appController, accounts []store.Account, warning string) Model {
	input := textinput.New()
	input.Prompt = ""
	input.CharLimit = 128
	input.Width = 36
	input.TextStyle = styleName
	input.Placeholder = "account name"

	bootAccounts := append([]store.Account(nil), accounts...)
	for i := range bootAccounts {
		bootAccounts[i].Quota.Loading = true
	}

	return Model{
		controller: controller,
		accounts:   bootAccounts,
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
	return tea.Batch(
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return refreshDoneMsg(m.controller.RefreshAll(ctx, accounts))
		},
		spinnerTickCmd(),
	)
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
	case spinnerTickMsg:
		if m.anyLoading() {
			m.spinner = (m.spinner + 1) % len(spinnerFrames)
			return m, spinnerTickCmd()
		}
	case tea.WindowSizeMsg:
	}
	return m, nil
}

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
	case "e":
		selected := m.selected()
		m.mode = modeEdit
		m.input.SetValue(selected.DisplayName)
		m.input.CursorEnd()
		m.input.Focus()
		m.errorText = ""
	case "d":
		if !m.selected().Saved {
			m.errorText = "Only saved accounts can be deleted."
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
			return m, tea.Quit
		}
		if err := m.controller.SwitchAccount(m.accounts, selected); err != nil {
			m.errorText = err.Error()
			return m, nil
		}
		return m, tea.Quit
	case "esc", "q", "ctrl+c":
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
		return "No Codex accounts found."
	}

	lines := make([]string, 0, len(m.accounts)*3+6)
	if m.warning != "" {
		lines = append(lines, styleWarning.Render("Warning: "+m.warning))
	}
	lines = append(lines, renderKeyLegend()...)
	if m.errorText != "" {
		lines = append(lines, styleError.Render(m.errorText))
	}
	lines = append(lines, "")

	for index, account := range m.accounts {
		lines = append(lines, m.renderAccount(index, account)...)
	}

	return strings.Join(lines, "\n")
}

func renderKeyLegend() []string {
	return []string{
		renderKeyLegendLine(
			keyLegendItem{style: styleKey, key: "↑/↓", desc: "move"},
			keyLegendItem{style: styleKeyBlue, key: "Space", desc: "switch"},
			keyLegendItem{style: styleKey, key: "Enter", desc: "confirm"},
		),
		renderKeyLegendLine(
			keyLegendItem{style: styleKey, key: "E", desc: "rename"},
			keyLegendItem{style: styleKeyRed, key: "D", desc: "delete"},
			keyLegendItem{style: styleKey, key: "Esc", desc: "close"},
		),
	}
}

type keyLegendItem struct {
	style lipgloss.Style
	key   string
	desc  string
}

func renderKeyLegendLine(items ...keyLegendItem) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, renderKeyLegendCell(item))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

func renderKeyLegendCell(item keyLegendItem) string {
	keyColumn := lipgloss.NewStyle().Width(7).Render(item.style.Render(item.key))
	descColumn := lipgloss.NewStyle().Width(14).Render(styleDim.Render(item.desc))
	cell := lipgloss.JoinHorizontal(lipgloss.Top, keyColumn, " ", descColumn)
	return lipgloss.NewStyle().Width(24).Render(cell)
}

func (m Model) renderAccount(index int, account store.Account) []string {
	cursor := " "
	if index == m.cursor {
		cursor = styleCursor.Render("›")
	}

	primaryLine := renderQuotaLine(account.Quota.Primary, account.Quota, "5h")
	secondaryLine := renderQuotaLine(account.Quota.Secondary, account.Quota, "7d")
	rowWidth := maxInt(lipgloss.Width(primaryLine), lipgloss.Width(secondaryLine))

	statusBits := []string{}
	if !account.Saved {
		statusBits = append(statusBits, styleLive.Render("live only"))
	}
	if plan := strings.TrimSpace(account.Meta.PlanType); plan != "" {
		statusBits = append(statusBits, styleDim.Render(strings.ToUpper(plan)))
	}

	label := styleName.Render(account.DisplayName)
	if m.mode == modeEdit && index == m.cursor {
		label = m.input.View()
	}
	leftParts := []string{fmt.Sprintf("%s %s", cursor, label)}
	if len(statusBits) > 0 {
		leftParts = append(leftParts, strings.Join(statusBits, "  "))
	}
	header := strings.Join(leftParts, "  ")

	trailing := ""
	if account.Quota.Loading {
		trailing = styleInfo.Render(spinnerFrames[m.spinner] + " loading")
	}
	if account.Current {
		trailing = joinTrailing(trailing, styleCurrent.Render("● current"))
	} else if m.markedKey == account.Key {
		trailing = joinTrailing(trailing, styleTarget.Render("○ switch"))
	}
	if trailing != "" {
		header = alignHeader(header, trailing, rowWidth)
	}

	if m.mode == modeDeleteConfirm && index == m.cursor {
		return []string{
			header,
			styleDelete.Render("  Delete this saved account?"),
			styleDim.Render("  Enter confirms deletion. Esc cancels."),
		}
	}

	return []string{
		header,
		primaryLine,
		secondaryLine,
	}
}

func renderQuotaLine(window quota.Window, snapshot quota.Snapshot, fallbackLabel string) string {
	const quotaBarWidth = 14
	label := fallbackLabel
	if window.Label != "" {
		label = window.Label
	}

	if !snapshot.HasData {
		if snapshot.Loading {
			return "  " + styleDim.Render(fmt.Sprintf("%-3s %s  checking quota", label, renderSkeletonBar(quotaBarWidth)))
		}
		return "  " + styleDim.Render(fmt.Sprintf("%-3s %s  quota unavailable", label, renderSkeletonBar(quotaBarWidth)))
	}

	remaining := remainingPercent(window.UsedPercent)
	bar := renderBar(remaining, quotaBarWidth)
	percent := styleDim.Render(fmt.Sprintf("%3.0f%% left", remaining))
	resetText := "reset unknown"
	if !window.ResetsAt.IsZero() {
		resetText = "resets " + window.ResetsAt.Local().Format("Jan _2 15:04")
	}
	return fmt.Sprintf("  %-3s %s  %s  %s", label, bar, percent, styleDim.Render(resetText))
}

func remainingPercent(usedPercent float64) float64 {
	remaining := 100 - usedPercent
	if remaining < 0 {
		return 0
	}
	if remaining > 100 {
		return 100
	}
	return remaining
}

func renderBar(percent float64, width int) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	filled := int((percent / 100) * float64(width))
	if filled > width {
		filled = width
	}
	return styleBarOn.Render(strings.Repeat("█", filled)) + styleBarOff.Render(strings.Repeat("░", width-filled))
}

func renderSkeletonBar(width int) string {
	return styleBarWarn.Render(strings.Repeat("·", width))
}

func alignHeader(left, right string, width int) string {
	if right == "" {
		return left
	}
	if width <= 0 {
		return left + "  " + right
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		gap = 2
	}
	return left + strings.Repeat(" ", gap) + right
}

func joinTrailing(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, "  ")
}

func maxInt(values ...int) int {
	maximum := 0
	for _, value := range values {
		if value > maximum {
			maximum = value
		}
	}
	return maximum
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

func (m Model) anyLoading() bool {
	for _, account := range m.accounts {
		if account.Quota.Loading {
			return true
		}
	}
	return false
}

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}
