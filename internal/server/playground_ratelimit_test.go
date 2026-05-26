package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/agentserver/agentserver/internal/auth"
)

func TestPlaygroundRateLimiter_DryRunBurst(t *testing.T) {
	rl := newPlaygroundRateLimiter(rate.Every(6*time.Second), 3)
	defer func() {
		// Stop evict goroutine from leaking in short tests — not critical.
	}()

	var hits int
	handler := rl.middleware(6, func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/dry-run", nil)
	req = req.WithContext(auth.ContextWithUserID(req.Context(), "user-1"))

	for i := 0; i < 10; i++ {
		rr := httptest.NewRecorder()
		handler(rr, req)
		if rr.Code == http.StatusTooManyRequests {
			if i < 3 {
				t.Fatalf("request %d: unexpected 429 (burst should allow 3)", i+1)
			}
			if got := rr.Header().Get("Retry-After"); got != "6" {
				t.Fatalf("Retry-After = %q, want 6", got)
			}
			if hits != 3 {
				t.Fatalf("handler hits = %d, want 3 before rate limit", hits)
			}
			return
		}
	}
	t.Fatalf("expected 429 within 10 requests, got %d successful hits", hits)
}

func TestPlaygroundRateLimiter_TestSandboxStricter(t *testing.T) {
	rl := newPlaygroundRateLimiter(rate.Every(20*time.Second), 1)

	var hits int
	handler := rl.middleware(20, func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/test-sandbox", nil)
	req = req.WithContext(auth.ContextWithUserID(req.Context(), "user-2"))

	for i := 0; i < 4; i++ {
		rr := httptest.NewRecorder()
		handler(rr, req)
		if rr.Code == http.StatusTooManyRequests {
			if i < 1 {
				t.Fatalf("request %d: unexpected 429 (burst 1)", i+1)
			}
			if got := rr.Header().Get("Retry-After"); got != "20" {
				t.Fatalf("Retry-After = %q, want 20", got)
			}
			return
		}
	}
	t.Fatal("expected 429 on 2nd+ request with burst 1")
}

func TestPlaygroundRateLimiter_RegeneratesAfterWindow(t *testing.T) {
	rl := newPlaygroundRateLimiter(rate.Every(50*time.Millisecond), 1)

	handler := rl.middleware(1, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = req.WithContext(auth.ContextWithUserID(req.Context(), "user-3"))

	rr1 := httptest.NewRecorder()
	handler(rr1, req)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first request: %d", rr1.Code)
	}

	rr2 := httptest.NewRecorder()
	handler(rr2, req)
	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: %d, want 429", rr2.Code)
	}

	time.Sleep(60 * time.Millisecond)

	rr3 := httptest.NewRecorder()
	handler(rr3, req)
	if rr3.Code != http.StatusOK {
		t.Fatalf("third request after window: %d, want 200", rr3.Code)
	}
}
