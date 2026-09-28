package orderbook

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

// SequencerClient posts owner-signed commands to an off-chain sequencer.
// HTTP 202 is provisional admission. It is not exchange execution.
type SequencerClient struct {
	base string
	http *http.Client
}

// NewSequencerClient uses baseURL as the sequencer origin, without a path.
func NewSequencerClient(baseURL string) (*SequencerClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("%w: sequencer url", ErrInvalidArgument)
	}
	return &SequencerClient{
		base: baseURL,
		http: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// Submit posts one signed command.
// A successful result has Provisional set. That does not mean the order executed.
func (s *SequencerClient) Submit(ctx context.Context, cmd canonical.Command) (Admission, error) {
	body, err := marshalCommand(cmd)
	if err != nil {
		return Admission{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/v1/commands", bytes.NewReader(body))
	if err != nil {
		return Admission{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return Admission{}, fmt.Errorf("%w: %v", ErrAdmissionRejected, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Admission{}, fmt.Errorf("%w: %v", ErrAdmissionRejected, err)
	}
	var parsed admitBody
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Admission{}, fmt.Errorf("%w: %s", ErrAdmissionRejected, strings.TrimSpace(string(raw)))
	}
	if resp.StatusCode != http.StatusAccepted || !parsed.Accepted {
		msg := parsed.Error
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return Admission{}, fmt.Errorf("%w: %s", ErrAdmissionRejected, msg)
	}
	if !parsed.Provisional {
		return Admission{}, fmt.Errorf("%w: admission was not marked provisional", ErrAdmissionRejected)
	}
	return Admission{
		Accepted:          true,
		SequencerPosition: parsed.SequencerPosition,
		Status:            parsed.Status,
		Provisional:       true,
	}, nil
}

// Health reads the sequencer health report.
func (s *SequencerClient) Health(ctx context.Context) (SequencerHealth, error) {
	var out SequencerHealth
	if err := s.get(ctx, "/health", &out); err != nil {
		return SequencerHealth{}, err
	}
	return out, nil
}

// Pending returns commands that are not yet finalized.
func (s *SequencerClient) Pending(ctx context.Context) ([]Admission, error) {
	var body struct {
		Commands []struct {
			Position    uint64 `json:"sequencer_position"`
			Status      string `json:"status"`
			Provisional bool   `json:"provisional"`
		} `json:"commands"`
	}
	if err := s.get(ctx, "/v1/pending", &body); err != nil {
		return nil, err
	}
	out := make([]Admission, 0, len(body.Commands))
	for _, cmd := range body.Commands {
		out = append(out, Admission{
			Accepted: true, SequencerPosition: cmd.Position, Status: cmd.Status, Provisional: cmd.Provisional,
		})
	}
	return out, nil
}

func (s *SequencerClient) get(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAdmissionRejected, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAdmissionRejected, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s", ErrAdmissionRejected, strings.TrimSpace(string(raw)))
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("%w: %v", ErrAdmissionRejected, err)
	}
	return nil
}

type admitBody struct {
	Accepted          bool   `json:"accepted"`
	SequencerPosition uint64 `json:"sequencer_position"`
	Status            string `json:"status"`
	Provisional       bool   `json:"provisional"`
	Error             string `json:"error"`
}

func marshalCommand(cmd canonical.Command) ([]byte, error) {
	if len(cmd.Owner) == 0 || len(cmd.PubKey) == 0 || len(cmd.Signature) == 0 {
		return nil, fmt.Errorf("%w: signed command", ErrInvalidArgument)
	}
	if !utf8.ValidString(cmd.ChainID) || !utf8.Valid(cmd.ExchangeInstanceID) {
		return nil, fmt.Errorf("%w: chain id and exchange instance id must be UTF-8 text", ErrInvalidArgument)
	}
	req := commandJSON{
		ProtocolVersion:    cmd.ProtocolVersion,
		ChainID:            cmd.ChainID,
		ExchangeInstanceID: string(cmd.ExchangeInstanceID),
		Owner:              sdk.AccAddress(cmd.Owner).String(),
		CommandNonce:       cmd.Nonce,
		PubKey:             hex.EncodeToString(cmd.PubKey),
		Signature:          hex.EncodeToString(cmd.Signature),
	}
	switch cmd.Type {
	case canonical.CommandTypePlace:
		if cmd.Place == nil {
			return nil, fmt.Errorf("%w: place command", ErrInvalidArgument)
		}
		if !utf8.Valid(cmd.Place.ClientOrderID) {
			return nil, fmt.Errorf("%w: client order id must be UTF-8 text", ErrInvalidArgument)
		}
		req.CommandType = "place"
		req.Place = &placeJSON{
			MarketID: uint64(cmd.Place.MarketID), Side: sideName(cmd.Place.Side),
			OrderType: typeName(cmd.Place.Type), TimeInForce: tifName(cmd.Place.TimeInForce),
			QuantityLots: uint64(cmd.Place.Quantity), PriceTicks: uint64(cmd.Place.Price),
			ExpiryHeight: cmd.Place.ExpiryHeight, ClientOrderID: string(cmd.Place.ClientOrderID),
		}
	case canonical.CommandTypeCancel:
		if cmd.Cancel == nil {
			return nil, fmt.Errorf("%w: cancel command", ErrInvalidArgument)
		}
		req.CommandType = "cancel"
		req.Cancel = &cancelJSON{OrderID: hex.EncodeToString(cmd.Cancel.OrderID[:])}
	default:
		return nil, fmt.Errorf("%w: command type", ErrInvalidArgument)
	}
	return json.Marshal(req)
}

type commandJSON struct {
	ProtocolVersion    uint32      `json:"protocol_version"`
	ChainID            string      `json:"chain_id"`
	ExchangeInstanceID string      `json:"exchange_instance_id"`
	Owner              string      `json:"owner"`
	CommandNonce       uint64      `json:"command_nonce"`
	CommandType        string      `json:"command_type"`
	Place              *placeJSON  `json:"place,omitempty"`
	Cancel             *cancelJSON `json:"cancel,omitempty"`
	PubKey             string      `json:"pub_key"`
	Signature          string      `json:"signature"`
}

type placeJSON struct {
	MarketID      uint64 `json:"market_id"`
	Side          string `json:"side"`
	OrderType     string `json:"order_type"`
	TimeInForce   string `json:"time_in_force"`
	QuantityLots  uint64 `json:"quantity_lots"`
	PriceTicks    uint64 `json:"price_ticks"`
	ExpiryHeight  uint64 `json:"expiry_height"`
	ClientOrderID string `json:"client_order_id"`
}

type cancelJSON struct {
	OrderID string `json:"order_id"`
}

func sideName(s domain.Side) string {
	switch s {
	case domain.SideBuy:
		return "buy"
	case domain.SideSell:
		return "sell"
	default:
		return ""
	}
}

func typeName(t domain.OrderType) string {
	switch t {
	case domain.OrderTypeLimit:
		return "limit"
	case domain.OrderTypeMarket:
		return "market"
	default:
		return ""
	}
}

func tifName(t domain.TimeInForce) string {
	switch t {
	case domain.TimeInForceGTC:
		return "gtc"
	case domain.TimeInForceIOC:
		return "ioc"
	case domain.TimeInForceFOK:
		return "fok"
	case domain.TimeInForceGTD:
		return "gtd"
	default:
		return ""
	}
}
