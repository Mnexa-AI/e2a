package agent

import (
	"testing"

	"github.com/tokencanopy/e2a/internal/usage"
)

func TestSetPollRateLimitReusesLimiter(t *testing.T) {
	api := NewAPI(nil, nil, nil, nil, usage.NewNoopUsageTracker(), "", "", "", "", false)
	original := api.pollLimit

	api.SetPollRateLimit(600)

	if api.pollLimit != original {
		t.Fatal("SetPollRateLimit replaced the limiter; the original cleanup goroutine would be abandoned")
	}
	_, _, limit, _, _ := api.PollLimitAllow("user_test")
	if limit != 600 {
		t.Fatalf("poll limit = %d, want 600", limit)
	}
}
