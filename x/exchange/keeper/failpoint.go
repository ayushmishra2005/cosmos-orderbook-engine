package keeper

import "errors"

// Stages abort a command after that step and before the cache is written.
// They exist for tests. Production leaves the stage empty.
const (
	FailAfterReserve       = "after-reserve"
	FailAfterMatch         = "after-match"
	FailDuringSettle       = "during-settle"
	FailBeforeBook         = "before-book"
	FailDuringOrderUpdate  = "during-order-update"
	FailDuringTrade        = "during-trade"
	FailBeforeNonce        = "before-nonce"
	FailBeforeRevision     = "before-revision"
	FailBeforeCommit       = "before-commit"
	FailAfterBankDeposit   = "after-bank-deposit"
	FailBeforeBankWithdraw = "before-bank-withdraw"
)

// ErrInjected is returned when a test stage aborts a command.
// It is not a consensus error and production never sets a stage.
var ErrInjected = errors.New("exchange: injected failure")

// testHook is shared by keeper copies so a test can arm one stage.
// The message server stores the keeper by value; the pointer is the hook.
type testHook struct {
	stage string
}

func (k Keeper) fail(stage string) error {
	if k.hook != nil && k.hook.stage != "" && k.hook.stage == stage {
		return ErrInjected
	}
	return nil
}

// SetFailStageForTest arms one stage. An empty stage disarms the hook.
// The cache is discarded when the stage returns ErrInjected.
func (k Keeper) SetFailStageForTest(stage string) {
	if k.hook == nil {
		return
	}
	k.hook.stage = stage
}
