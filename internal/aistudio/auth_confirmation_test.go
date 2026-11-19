package aistudio

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Mag1cFall/AIStudio2API/internal/camoufoxnative"
)

func TestConfirmAuthenticationFailureRequiresConfirmation(t *testing.T) {
	for _, test := range []struct {
		name      string
		checkErr  error
		noCheck   bool
		cancelled bool
		want      AccountState
	}{
		{name: "valid session", want: AccountReady},
		{name: "network failure", checkErr: errors.New("connection reset"), want: AccountReady},
		{name: "timeout", checkErr: context.DeadlineExceeded, want: AccountReady},
		{name: "no checker", noCheck: true, want: AccountReady},
		{name: "confirmed login redirect", checkErr: camoufoxnative.ErrAuthenticationRequired, want: AccountAuthRequired},
		{name: "cancelled check", checkErr: camoufoxnative.ErrAuthenticationRequired, cancelled: true, want: AccountReady},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool, next := permissionTestLeases(t)
			account := pool.byID["a"]
			original := account.Config
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if !test.noCheck {
				pool.SetSessionCheck(func(context.Context, *Account) error {
					if test.cancelled {
						cancel()
					}
					return test.checkErr
				})
			}
			cause := &RPCError{StatusCode: http.StatusUnauthorized, Code: 16, Message: "authentication required"}
			if err := next("a").ConfirmAuthenticationFailure(ctx, cause); err != nil {
				t.Fatal(err)
			}
			if account.State != test.want || account.Config != original {
				t.Fatalf("state=%s, want=%s; config changed=%v", account.State, test.want, account.Config != original)
			}
			if test.want == AccountReady && (account.stateMessage != "" || !account.authCheckedAt.IsZero()) {
				t.Fatal("an unconfirmed failure changed authentication state")
			}
		})
	}
}
