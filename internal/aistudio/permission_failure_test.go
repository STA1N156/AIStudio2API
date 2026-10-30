package aistudio

import (
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func permissionTestLeases(t *testing.T) (*AccountPool, func(string) *AccountLease) {
	t.Helper()
	accounts := make([]*Account, 0, 2)
	for _, id := range []string{"a", "b"} {
		accounts = append(accounts, &Account{
			ID: id, ConfigPath: filepath.Join(t.TempDir(), "account.json"),
			Config: DefaultAccountConfig(id + "@example.com"), State: AccountReady,
		})
	}
	pool := NewAccountPool(accounts, 4)
	checkedAt := time.Now().Add(-time.Hour)
	return pool, func(id string) *AccountLease {
		checkedAt = checkedAt.Add(time.Second)
		return &AccountLease{pool: pool, account: pool.byID[id], checkedAt: checkedAt}
	}
}

func permissionTestError() error {
	return errors.Join(errors.New("upstream"), &RPCError{
		Method: "GenerateContent", StatusCode: http.StatusForbidden, Code: 7,
		Message: "The caller does not have permission",
	})
}

func TestPermissionDeniedConsecutiveFailures(t *testing.T) {
	for _, test := range []struct {
		name  string
		steps []string
	}{
		{"third_failure", []string{"403", "403", "403"}},
		{"success_resets", []string{"403", "403", "success", "403", "403", "403"}},
		{"other_error_resets", []string{"403", "403", "other", "403", "403", "403"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool, next := permissionTestLeases(t)
			original := pool.byID["a"].Config
			for i, step := range test.steps {
				lease := next("a")
				var err error
				switch step {
				case "success":
					lease.ResetPermissionDeniedStreak()
				case "other":
					err = lease.MarkRequestFailure(errors.New("network error"))
				default:
					err = lease.MarkRequestFailure(permissionTestError())
				}
				if err != nil {
					t.Fatal(err)
				}
				if wantEnabled := i < len(test.steps)-1; lease.account.Config.Enabled != wantEnabled {
					t.Fatalf("step %d: enabled=%v, want %v", i, lease.account.Config.Enabled, wantEnabled)
				}
			}
			saved, err := readAccountConfig(pool.byID["a"].ConfigPath)
			original.Enabled = false
			if err != nil || saved != original {
				t.Fatalf("saved config=%+v, error=%v", saved, err)
			}
		})
	}
}

func TestPermissionDeniedDeduplicationAndIsolation(t *testing.T) {
	pool, next := permissionTestLeases(t)
	first := next("a")
	for _, lease := range []*AccountLease{first, first, first, next("b"), next("a")} {
		if err := lease.MarkRequestFailure(permissionTestError()); err != nil {
			t.Fatal(err)
		}
	}
	if !pool.byID["a"].Config.Enabled || pool.byID["a"].permissionFailures != 2 || pool.byID["b"].permissionFailures != 1 {
		t.Fatal("duplicate failures or another account affected the streak")
	}
	if err := next("a").MarkRequestFailure(permissionTestError()); err != nil {
		t.Fatal(err)
	}
	if pool.byID["a"].Config.Enabled || !pool.byID["b"].Config.Enabled {
		t.Fatal("third failure must disable only its own account")
	}
}

func TestPermissionDeniedConcurrentAndStaleResults(t *testing.T) {
	pool, next := permissionTestLeases(t)
	stale := next("a")
	leases := []*AccountLease{next("a"), next("a"), next("a")}
	if err := leases[0].MarkRequestFailure(permissionTestError()); err != nil {
		t.Fatal(err)
	}
	stale.ResetPermissionDeniedStreak()
	if pool.byID["a"].permissionFailures != 1 {
		t.Fatal("an older success cleared a newer failure")
	}
	var wg sync.WaitGroup
	for _, lease := range leases[1:] {
		wg.Go(func() {
			if err := lease.MarkRequestFailure(permissionTestError()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if pool.byID["a"].Config.Enabled {
		t.Fatal("three concurrent failures did not disable the account")
	}
	old := next("b")
	next("b").ResetPermissionDeniedStreak()
	if err := old.MarkRequestFailure(permissionTestError()); err != nil {
		t.Fatal(err)
	}
	if pool.byID["b"].permissionFailures != 0 {
		t.Fatal("an old failure restarted the streak after a newer success")
	}
}

func TestPermissionDeniedOtherErrorsUnchanged(t *testing.T) {
	for _, test := range []struct {
		status int
		code   int64
		state  AccountState
	}{
		{403, 3, AccountDisabled},
		{401, 16, AccountAuthRequired},
	} {
		pool, next := permissionTestLeases(t)
		err := next("a").MarkRequestFailure(&RPCError{StatusCode: test.status, Code: test.code, Message: "other error"})
		if err != nil || pool.byID["a"].State != test.state {
			t.Fatalf("HTTP %d: state=%s, error=%v", test.status, pool.byID["a"].State, err)
		}
	}
}
