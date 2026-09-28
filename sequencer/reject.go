package sequencer

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	batchtypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/batch/types"
	exchangetypes "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types"
)

func permanentCommand(err error) bool {
	return errors.Is(err, exchangetypes.ErrNotFound) ||
		errors.Is(err, exchangetypes.ErrWrongOwner) ||
		errors.Is(err, exchangetypes.ErrExpired)
}

func rejectionError(log string) error {
	if idx, rest, ok := parseCommandLog(log); ok {
		return &batchtypes.CommandRejection{Index: idx, Err: commandSentinel(rest)}
	}
	if strings.Contains(log, batchtypes.ErrStaleRevision.Error()) {
		return fmt.Errorf("%w: %s", batchtypes.ErrStaleRevision, log)
	}
	if strings.Contains(log, batchtypes.ErrPreviousCommitment.Error()) {
		return fmt.Errorf("%w: %s", batchtypes.ErrPreviousCommitment, log)
	}
	return fmt.Errorf("%w: %s", ErrBatchRejected, log)
}

func parseCommandLog(msg string) (int, string, bool) {
	const prefix = "batch: command "
	i := strings.Index(msg, prefix)
	if i < 0 {
		return 0, "", false
	}
	rest := msg[i+len(prefix):]
	j := 0
	for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
		j++
	}
	const mark = " rejected: "
	if j == 0 || !strings.HasPrefix(rest[j:], mark) {
		return 0, "", false
	}
	n, err := strconv.Atoi(rest[:j])
	if err != nil {
		return 0, "", false
	}
	return n, rest[j+len(mark):], true
}

func commandSentinel(msg string) error {
	switch {
	case strings.Contains(msg, exchangetypes.ErrNotFound.Error()):
		return exchangetypes.ErrNotFound
	case strings.Contains(msg, exchangetypes.ErrWrongOwner.Error()):
		return exchangetypes.ErrWrongOwner
	case strings.Contains(msg, exchangetypes.ErrExpired.Error()):
		return exchangetypes.ErrExpired
	case strings.Contains(msg, exchangetypes.ErrInsufficientBalance.Error()):
		return exchangetypes.ErrInsufficientBalance
	default:
		return errors.New(msg)
	}
}
