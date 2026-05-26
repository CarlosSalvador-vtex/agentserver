package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// Sprint 2 PR-5 (improvements.md #8). Background poller that closes the
// feedback loop between Promote → real production landing. Every 5 min the
// loop lists promoted drafts whose PR state is NULL or "open" and pings the
// GitHub Pulls API to refresh the cached state.
//
// Termination contract: any draft whose recorded state is already "merged"
// or "closed" is skipped (terminal). The poller is cheap and idempotent —
// missed ticks during a deploy just defer the next observation.

const (
	promotePollInterval = 5 * time.Minute
	promotePollTimeout  = 10 * time.Second
)

// pullURLRegex extracts the PR number from a GitHub PR URL of the form
// https://github.com/<owner>/<repo>/pull/<n>.
var pullURLRegex = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/pull/(\d+)`)

// StartPromotePoller spawns the background poller. It returns immediately;
// the loop runs until ctx is cancelled. When GITHUB_PROMOTE_TOKEN is unset
// (dev cluster without promote credentials) the loop logs once and exits.
func (s *Server) StartPromotePoller(ctx context.Context) {
	cfg, err := loadPromoteConfig()
	if err != nil {
		log.Printf("playground promote poller: disabled (%v)", err)
		return
	}
	go s.runPromotePoller(ctx, cfg)
}

func (s *Server) runPromotePoller(ctx context.Context, cfg *promoteConfig) {
	t := time.NewTicker(promotePollInterval)
	defer t.Stop()
	// Run once on startup so freshly promoted PRs get a state without
	// waiting a full interval.
	s.pollPromotedDraftsOnce(ctx, cfg)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.pollPromotedDraftsOnce(ctx, cfg)
		}
	}
}

func (s *Server) pollPromotedDraftsOnce(ctx context.Context, cfg *promoteConfig) {
	rows, err := s.DB.ListPromotedDraftsForPolling()
	if err != nil {
		log.Printf("playground promote poller: list: %v", err)
		return
	}
	for _, row := range rows {
		owner, repo, prNum, ok := parsePullURL(row.PromotedPRURL)
		if !ok {
			continue
		}
		state, err := fetchPRState(ctx, cfg.Token, owner, repo, prNum)
		if err != nil {
			log.Printf("playground promote poller: %s/%s#%d: %v", owner, repo, prNum, err)
			continue
		}
		// Only write when the state actually changed; cuts down on
		// updated_at noise and audit-log churn (when #14 lands).
		if row.PromotedPRState.Valid && row.PromotedPRState.String == state {
			continue
		}
		if err := s.DB.UpdatePromotedPRState(row.Kind, row.DraftID, state); err != nil {
			log.Printf("playground promote poller: persist %s %s: %v", row.Kind, row.DraftID, err)
		}
	}
}

func parsePullURL(u string) (owner, repo string, prNum int, ok bool) {
	m := pullURLRegex.FindStringSubmatch(u)
	if len(m) != 4 {
		return "", "", 0, false
	}
	n, err := strconv.Atoi(m[3])
	if err != nil {
		return "", "", 0, false
	}
	return m[1], m[2], n, true
}

// fetchPRState hits GET /repos/{owner}/{repo}/pulls/{n} and derives one of
// {open, merged, closed} from the JSON shape. GitHub reports merged PRs as
// state=closed + merged=true, so we collapse those into "merged".
func fetchPRState(ctx context.Context, token, owner, repo string, prNum int) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, repo, prNum)
	ctx, cancel := context.WithTimeout(ctx, promotePollTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// PR was deleted; treat as closed to stop polling it.
		return "closed", nil
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("github pulls api: %s", resp.Status)
	}
	var body struct {
		State  string `json:"state"`  // "open" | "closed"
		Merged bool   `json:"merged"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode pr: %w", err)
	}
	switch {
	case body.Merged:
		return "merged", nil
	case body.State == "open":
		return "open", nil
	default:
		return "closed", nil
	}
}
