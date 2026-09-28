package orderbook

import (
	"context"
	"fmt"
	"time"

	"github.com/gorilla/websocket"

	cmttypes "github.com/cometbft/cometbft/types"
)

const liveQuery = "tm.event='NewBlock'"

type liveConn interface {
	Recv(ctx context.Context) ([]byte, error)
	Close() error
}

type wsConn struct {
	conn    *websocket.Conn
	pending []byte
}

func (c *Client) dialComet(ctx context.Context) (liveConn, error) {
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.DialContext(ctx, c.wsURL, nil)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(8 << 20)
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "subscribe",
		"params":  map[string]string{"query": liveQuery},
	}
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := conn.WriteJSON(payload); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	events, err := decodeMessage(msg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: subscribe: %v", ErrStreamDisconnected, err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	out := &wsConn{conn: conn}
	if len(events) > 0 {
		out.pending = append([]byte(nil), msg...)
	}
	return out, nil
}

func (w *wsConn) Recv(ctx context.Context) ([]byte, error) {
	if len(w.pending) > 0 {
		msg := w.pending
		w.pending = nil
		return msg, nil
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = w.conn.Close()
		case <-done:
		}
	}()
	defer close(done)
	_, msg, err := w.conn.ReadMessage()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrStreamDisconnected, err)
	}
	return msg, nil
}

func (w *wsConn) Close() error {
	return w.conn.Close()
}

func (c *Client) latestHeight(ctx context.Context) (int64, error) {
	if c.rpc == nil {
		return 0, ErrRPCUnavailable
	}
	st, err := c.rpc.Status(ctx)
	if err != nil {
		return 0, err
	}
	return st.SyncInfo.LatestBlockHeight, nil
}

func (c *Client) blockEvents(ctx context.Context, height int64) ([]Event, error) {
	if c.rpc == nil {
		return nil, ErrRPCUnavailable
	}
	h := height
	results, err := c.rpc.BlockResults(ctx, &h)
	if err != nil {
		return nil, err
	}
	block, err := c.rpc.Block(ctx, &h)
	if err != nil {
		return nil, err
	}
	out := eventsFromABCI(height, "", results.FinalizeBlockEvents)
	var txs []cmttypes.Tx
	if block != nil && block.Block != nil {
		txs = block.Block.Txs
	}
	for i, result := range results.TxsResults {
		if result == nil {
			continue
		}
		hash := ""
		if i < len(txs) {
			hash = fmt.Sprintf("%X", txs[i].Hash())
		}
		out = append(out, eventsFromABCI(height, hash, result.Events)...)
	}
	return out, nil
}
