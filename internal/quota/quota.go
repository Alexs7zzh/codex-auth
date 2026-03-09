package quota

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	HasData   bool      `json:"has_data,omitempty"`
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

func Empty(reason string) Snapshot {
	snapshot := Loading()
	snapshot.Loading = false
	snapshot.Stale = true
	snapshot.Error = reason
	return snapshot
}

func ErrorSnapshot(err error) Snapshot {
	if err == nil {
		return Empty("")
	}
	return Empty(err.Error())
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
		HasData:   true,
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

type fileInfo struct {
	path    string
	modTime time.Time
}

type sessionLine struct {
	Timestamp string `json:"timestamp"`
	Payload   struct {
		Type       string          `json:"type"`
		RateLimits json.RawMessage `json:"rate_limits"`
	} `json:"payload"`
}

type sessionRateLimits struct {
	Primary   liveWindow `json:"primary"`
	Secondary liveWindow `json:"secondary"`
	PlanType  string     `json:"plan_type"`
}

func LoadRecentLocalSnapshot(home string) (Snapshot, bool) {
	candidates := newestSessionFiles(home, 40)
	for _, candidate := range candidates {
		snapshot, ok := readSnapshotFromSessionFile(candidate.path)
		if ok {
			snapshot.Source = "local-session"
			snapshot.Stale = true
			snapshot.HasData = true
			return snapshot, true
		}
	}
	return Snapshot{}, false
}

func newestSessionFiles(home string, limit int) []fileInfo {
	roots := []string{
		filepath.Join(home, "sessions"),
		filepath.Join(home, "archived_sessions"),
	}
	files := make([]fileInfo, 0, limit)
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || filepath.Ext(path) != ".jsonl" {
				return nil
			}
			info, statErr := entry.Info()
			if statErr != nil {
				return nil
			}
			files = append(files, fileInfo{path: path, modTime: info.ModTime()})
			return nil
		})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})
	if len(files) > limit {
		files = files[:limit]
	}
	return files
}

func readSnapshotFromSessionFile(path string) (Snapshot, bool) {
	file, err := os.Open(path)
	if err != nil {
		return Snapshot{}, false
	}
	defer file.Close()

	var best Snapshot
	var bestTime time.Time
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 512*1024)

	for scanner.Scan() {
		var line sessionLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}
		if line.Payload.Type != "token_count" || len(line.Payload.RateLimits) == 0 {
			continue
		}

		var payload sessionRateLimits
		if err := json.Unmarshal(line.Payload.RateLimits, &payload); err != nil {
			continue
		}
		if payload.Primary.WindowMinutes == 0 && payload.Secondary.WindowMinutes == 0 {
			continue
		}

		timestamp, _ := time.Parse(time.RFC3339Nano, line.Timestamp)
		if timestamp.Before(bestTime) {
			continue
		}
		bestTime = timestamp
		best = Snapshot{
			Primary:   normalizeWindow("5h", payload.Primary),
			Secondary: normalizeWindow("7d", payload.Secondary),
			PlanType:  strings.ToLower(payload.PlanType),
			CheckedAt: timestamp.UTC(),
			HasData:   true,
		}
	}

	if bestTime.IsZero() {
		return Snapshot{}, false
	}
	return best, true
}
