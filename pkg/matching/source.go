package matching

import "github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"

// OrderSource is a forward-only cursor over resting orders in book order.
// Peek returns the order at the cursor. Next advances one order and must
// move forward; the matcher reads each maker at most once.
// The matcher does not close the source.
type OrderSource interface {
	Peek() (domain.Order, bool, error)
	Next() error
	Close() error
}
