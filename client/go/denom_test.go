package orderbook

import (
	"context"
	"errors"
	"testing"
)

func TestInvalidDenomDoesNotPanic(t *testing.T) {
	client := (&Client{}).WithSigner(privSigner{priv: testPriv()})
	for _, denom := range []string{"!", "", "1bad", "HasSpace "} {
		func() {
			defer func() {
				if recover() != nil {
					t.Fatalf("panic on %q", denom)
				}
			}()
			if _, err := client.Deposit(context.Background(), denom, 1); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("deposit %q: %v", denom, err)
			}
			if _, err := client.Withdraw(context.Background(), denom, 1); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("withdraw %q: %v", denom, err)
			}
		}()
	}
}
