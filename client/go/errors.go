package orderbook

import "errors"

var (
	ErrRPCUnavailable     = errors.New("orderbook: rpc unavailable")
	ErrGRPCUnavailable    = errors.New("orderbook: grpc unavailable")
	ErrTxRejected         = errors.New("orderbook: transaction rejected")
	ErrAdmissionRejected  = errors.New("orderbook: sequencer admission rejected")
	ErrInvalidArgument    = errors.New("orderbook: invalid argument")
	ErrStreamDisconnected = errors.New("orderbook: stream disconnected")
	ErrSlowConsumer       = errors.New("orderbook: slow consumer")
	ErrNotFound           = errors.New("orderbook: not found")
	ErrSignerRequired     = errors.New("orderbook: signer required")
)
