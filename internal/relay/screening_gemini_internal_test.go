package relay

import (
	"testing"
)

// TestBuildScreenEngine_GeminiTimeout guards the fix for PR #359 review comment #4
// (retry backoff couldn't fit inside the Engine's default 5s per-detector timeout,
// making the retry loop in piguard/gemini.go dead code). buildScreenEngine must
// widen the engine's timeout to geminiDetectorTimeout when Gemini is wired in, and
// leave the default alone otherwise.
func TestBuildScreenEngine_GeminiTimeout(t *testing.T) {
	t.Run("with_gemini_key", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "fake-key-for-wiring-check")
		e := buildScreenEngine()
		if got := e.Timeout(); got != geminiDetectorTimeout {
			t.Errorf("Timeout() = %v, want %v (geminiDetectorTimeout)", got, geminiDetectorTimeout)
		}
	})

	t.Run("without_gemini_key", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "")
		t.Setenv("GOOGLE_API_KEY", "")
		e := buildScreenEngine()
		if got := e.Timeout(); got == geminiDetectorTimeout {
			t.Errorf("Timeout() = %v, want the Engine default (not geminiDetectorTimeout) when Gemini is absent", got)
		}
	})

	t.Run("key_present_but_explicitly_disabled", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "fake-key-for-wiring-check")
		t.Setenv("E2A_GEMINI_DETECTOR_ENABLED", "false")
		e := buildScreenEngine()
		if got := e.Timeout(); got == geminiDetectorTimeout {
			t.Errorf("Timeout() = %v, want the Engine default — E2A_GEMINI_DETECTOR_ENABLED=false must disable Gemini even with a key present", got)
		}
	})
}

// TestGeminiDetectorEnabled_DefaultsTrue guards the flag's default: unset must
// preserve the pre-existing behavior (enabled purely by key presence, no opt-in
// required), so introducing this kill-switch doesn't silently disable Gemini for
// anyone already relying on key-presence-only activation.
func TestGeminiDetectorEnabled_DefaultsTrue(t *testing.T) {
	t.Setenv("E2A_GEMINI_DETECTOR_ENABLED", "")
	if !geminiDetectorEnabled() {
		t.Error("geminiDetectorEnabled() = false, want true when E2A_GEMINI_DETECTOR_ENABLED is unset")
	}
}
