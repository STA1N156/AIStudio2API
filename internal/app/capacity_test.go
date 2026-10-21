package app

import "testing"

func TestGoAndBrowserRespectStartupConcurrency(t *testing.T) {
	for _, backend := range []string{"", "camoufox"} {
		manager := &accountWorkerManager{camoufox: backend}
		manager.setLimits(20, 100, 5)
		if manager.startup.size() != 5 || manager.activeLimit() != 100 || manager.warmLimit() != 20 {
			t.Fatalf("incorrect capacity for %q", backend)
		}
	}
}
