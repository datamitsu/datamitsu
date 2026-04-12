package sponsor

import (
	"math/rand"
	"strings"
	"testing"
)

func TestRotatingMessagesHaveTemplate(t *testing.T) {
	for i, msg := range rotatingMessages {
		if msg == "" {
			t.Errorf("message %d is empty", i)
		}
		if !strings.Contains(msg, "%s") {
			t.Errorf("message %d does not contain %%s placeholder: %s", i, msg)
		}
	}
}

func TestSponsorURLConstant(t *testing.T) {
	if sponsorURL != "https://datamitsu.com/sponsor" {
		t.Errorf("sponsorURL = %q, want %q", sponsorURL, "https://datamitsu.com/sponsor")
	}
}

func TestSelectRandomMessageContainsURL(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	msg := selectRandomMessage(rnd)
	if msg == "" {
		t.Error("selectRandomMessage returned empty string")
	}
	if !strings.Contains(msg, sponsorURL) {
		t.Errorf("selectRandomMessage result does not contain sponsor URL: %s", msg)
	}
	if strings.Contains(msg, "%s") {
		t.Errorf("selectRandomMessage result still contains %%s placeholder: %s", msg)
	}
}

func TestSelectRandomMessageVariety(t *testing.T) {
	rnd := rand.New(rand.NewSource(99))
	seen := make(map[string]bool)
	for i := 0; i < 200; i++ {
		msg := selectRandomMessage(rnd)
		seen[msg] = true
	}
	if len(seen) < 5 {
		t.Errorf("expected at least 5 different messages in 200 selections, got %d", len(seen))
	}
}

func TestAllMessagesSelectable(t *testing.T) {
	seen := make(map[string]bool)
	for seed := int64(0); seed < 1000; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		msg := selectRandomMessage(rnd)
		seen[msg] = true
		if len(seen) == len(rotatingMessages) {
			return
		}
	}
	missing := 0
	for i, tmpl := range rotatingMessages {
		formatted := strings.ReplaceAll(tmpl, "%s", sponsorURL)
		if !seen[formatted] {
			missing++
			t.Logf("message %d never selected", i)
		}
	}
	if missing > 0 {
		t.Errorf("%d messages were never selected across 1000 seeds", missing)
	}
}
