package aistudio

import (
	"errors"
	"net/http"
	"path/filepath"
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

func TestCallerPermission403NeverDisables(t *testing.T) {
	pool, next := permissionTestLeases(t)
	original := pool.byID["a"].Config
	for i := 0; i < 20; i++ {
		message := "The caller does not have permission"
		if i%2 == 0 {
			message = "  " + message + ".  "
		}
		err := next("a").MarkRequestFailure(&RPCError{
			Method: "GenerateContent", StatusCode: 403, Code: int64(i%2) * 7, Message: message,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if pool.byID["a"].Config != original || pool.byID["a"].State != AccountReady {
		t.Fatal("caller permission errors changed the account")
	}
	if len(pool.byID["a"].runtime.ModelAccess) != 0 || len(pool.byID["a"].runtime.Cooldowns) != 0 {
		t.Fatal("caller permission errors must not mark a model unavailable or cooling")
	}
	pool.byID["b"].Config.Enabled = false
	if err := next("b").MarkRequestFailure(permissionTestError()); err != nil {
		t.Fatal(err)
	}
	if pool.byID["b"].Config.Enabled {
		t.Fatal("an already disabled account was re-enabled")
	}
}

func TestPermissionDeniedOtherErrorsUnchanged(t *testing.T) {
	for _, test := range []struct {
		status int
		code   int64
		state  AccountState
	}{
		{403, 3, AccountDisabled},
		{403, 7, AccountDisabled},
		{401, 16, AccountAuthRequired},
	} {
		pool, next := permissionTestLeases(t)
		err := next("a").MarkRequestFailure(&RPCError{StatusCode: test.status, Code: test.code, Message: "other error"})
		if err != nil || pool.byID["a"].State != test.state {
			t.Fatalf("HTTP %d: state=%s, error=%v", test.status, pool.byID["a"].State, err)
		}
	}
}

func TestOther403DisablesEvenForFreeModelEligibilityMessage(t *testing.T) {
	pool, next := permissionTestLeases(t)
	err := next("a").MarkRequestFailure(&RPCError{
		Method: "GenerateContent", StatusCode: 403, Code: 7,
		Message: "You are not eligible to use this model.",
	})
	if err != nil || pool.byID["a"].Config.Enabled {
		t.Fatalf("other 403 must disable: %v", err)
	}
}
