package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"codex-auth/internal/authfile"
)

type Window struct {
	UsedPercent   float64   `json:"used_percent"`
	WindowMinutes int       `json:"window_minutes"`
	ResetsAt      time.Time `json:"resets_at,omitempty"`
	Label         string    `json:"label,omitempty"`
}

type Snapshot struct {
	Primary   Window    `json:"primary"`
	Secondary Window    `json:"secondary"`
	PlanType  string    `json:"plan_type,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
	Source    string    `json:"source,omitempty"`
	Stale     bool      `json:"stale,omitempty"`
	Error     string    `json:"error,omitempty"`
	Loading   bool      `json:"loading,omitempty"`
}

type Provider interface {
	Fetch(context.Context, authfile.File) (Snapshot, error)
}

type LiveProvider struct {
	client *http.Client
}

type liveResponse struct {
	Primary   liveWindow `json:"primary"`
	Secondary liveWindow `json:"secondary"`
	PlanType  string     `json:"plan_type"`
	RateLimit struct {
		Primary   liveWindow `json:"primary"`
		Secondary liveWindow `json:"secondary"`
		PlanType  string     `json:"plan_type"`
	} `json:"rate_limits"`
}

type liveWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"`
}

func NewLiveProvider() Provider {
	return &LiveProvider{
		client: &http.Client{Timeout: 6 * time.Second},
	}
}

func Loading() Snapshot {
	return Snapshot{
		Primary: Window{Label: "5h"},
		Secondary: Window{
			Label: "7d",
		},
		Loading: true,
	}
}

func ErrorSnapshot(err error) Snapshot {
	snapshot := Loading()
	snapshot.Loading = false
	snapshot.Stale = true
	if err != nil {
		snapshot.Error = err.Error()
	}
	return snapshot
}

func (p *LiveProvider) Fetch(ctx context.Context, file authfile.File) (Snapshot, error) {
	if file.Tokens.AccessToken == "" || file.Tokens.AccountID == "" {
		return Snapshot{}, fmt.Errorf("missing access token or account id")
	}

	urls := []string{
		"https://chatgpt.com/api/codex/usage",
		"https://chatgpt.com/backend-api/codex/usage",
	}

	var lastErr error
	for _, endpoint := range urls {
		snapshot, err := p.fetchURL(ctx, file, endpoint)
		if err == nil {
			return snapshot, nil
		}
		lastErr = err
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("usage endpoint unavailable")
	}
	return Snapshot{}, lastErr
}

func (p *LiveProvider) fetchURL(ctx context.Context, file authfile.File, endpoint string) (Snapshot, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Snapshot{}, err
	}

	request.Header.Set("Authorization", "Bearer "+file.Tokens.AccessToken)
	request.Header.Set("ChatGPT-Account-Id", file.Tokens.AccountID)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "codex-auth/0.1")

	response, err := p.client.Do(request)
	if err != nil {
		return Snapshot{}, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Snapshot{}, err
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusForbidden {
			return Snapshot{}, fmt.Errorf("upstream denied quota request")
		}
		return Snapshot{}, fmt.Errorf("usage request failed: %s", response.Status)
	}

	var payload liveResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return Snapshot{}, fmt.Errorf("decode usage response: %w", err)
	}

	primary := payload.Primary
	secondary := payload.Secondary
	planType := payload.PlanType
	if primary.WindowMinutes == 0 && payload.RateLimit.Primary.WindowMinutes > 0 {
		primary = payload.RateLimit.Primary
		secondary = payload.RateLimit.Secondary
		planType = firstNonEmpty(planType, payload.RateLimit.PlanType)
	}
	if primary.WindowMinutes == 0 && secondary.WindowMinutes == 0 {
		return Snapshot{}, fmt.Errorf("usage response missing windows")
	}

	return Snapshot{
		Primary:   normalizeWindow("5h", primary),
		Secondary: normalizeWindow("7d", secondary),
		PlanType:  strings.ToLower(planType),
		CheckedAt: time.Now().UTC(),
		Source:    "live",
	}, nil
}

func normalizeWindow(label string, window liveWindow) Window {
	return Window{
		Label:         label,
		UsedPercent:   window.UsedPercent,
		WindowMinutes: window.WindowMinutes,
		ResetsAt:      time.Unix(window.ResetsAt, 0).UTC(),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
