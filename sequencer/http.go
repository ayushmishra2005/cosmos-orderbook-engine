package sequencer

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/canonical"
	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

const maxRequestBody = 256 << 10

type commandRequest struct {
	ProtocolVersion    uint32         `json:"protocol_version"`
	ChainID            string         `json:"chain_id"`
	ExchangeInstanceID string         `json:"exchange_instance_id"`
	Owner              string         `json:"owner"`
	CommandNonce       uint64         `json:"command_nonce"`
	CommandType        string         `json:"command_type"`
	Place              *placeRequest  `json:"place"`
	Cancel             *cancelRequest `json:"cancel"`
	PubKey             string         `json:"pub_key"`
	Signature          string         `json:"signature"`
}

type placeRequest struct {
	MarketID      uint64 `json:"market_id"`
	Side          string `json:"side"`
	OrderType     string `json:"order_type"`
	TimeInForce   string `json:"time_in_force"`
	QuantityLots  uint64 `json:"quantity_lots"`
	PriceTicks    uint64 `json:"price_ticks"`
	ExpiryHeight  uint64 `json:"expiry_height"`
	ClientOrderID string `json:"client_order_id"`
}

type cancelRequest struct {
	OrderID string `json:"order_id"`
}

type admitResponse struct {
	Accepted          bool   `json:"accepted"`
	SequencerPosition uint64 `json:"sequencer_position,omitempty"`
	Status            string `json:"status,omitempty"`
	Provisional       bool   `json:"provisional"`
	Error             string `json:"error,omitempty"`
}

type healthResponse struct {
	Status              string `json:"status"`
	Pending             int    `json:"pending"`
	Inflight            int    `json:"inflight"`
	LatestObservedBatch uint64 `json:"latest_observed_batch"`
	Chain               string `json:"chain"`
}

type pendingResponse struct {
	Commands   []Receipt `json:"commands"`
	NextOffset int       `json:"next_offset,omitempty"`
}

// Handler is the admission HTTP API.
// POST /v1/commands accepts one signed command and reports provisional admission.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.Handle("GET /metrics", promhttp.HandlerFor(s.met.reg, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
	}))
	mux.HandleFunc("POST /v1/commands", s.handleCommand)
	mux.HandleFunc("GET /v1/pending", s.handlePending)
	return mux
}

func (s *Service) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.health())
}

func (s *Service) health() healthResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := "ok"
	chain := "unknown"
	if s.chainKnown {
		if s.chainConnected {
			chain = "connected"
		} else {
			chain = "unavailable"
			status = "degraded"
		}
	}
	return healthResponse{
		Status:              status,
		Pending:             len(s.pending),
		Inflight:            len(s.inflight),
		LatestObservedBatch: s.latestBatch,
		Chain:               chain,
	}
}

func (s *Service) handlePending(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	page, next := s.PendingPage(limit, offset)
	writeJSON(w, http.StatusOK, pendingResponse{Commands: page, NextOffset: next})
}

func (s *Service) handleCommand(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.rejectHTTP(reasonForRead(err))
		writeAdmit(w, http.StatusBadRequest, Receipt{}, ErrMalformed)
		return
	}
	cmd, err := decodeCommandJSON(body)
	if err != nil {
		s.rejectHTTP(reasonLabel(err))
		writeAdmit(w, statusFor(err), Receipt{}, err)
		return
	}
	rec, err := s.Admit(r.Context(), cmd)
	if err != nil {
		writeAdmit(w, statusFor(err), Receipt{}, err)
		return
	}
	writeAdmit(w, http.StatusAccepted, rec, nil)
}

func (s *Service) rejectHTTP(reason string) {
	s.met.countReceived("unknown")
	s.met.countRejected(reason)
}

func reasonForRead(err error) string {
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		return reasonTooLarge
	}
	return reasonMalformed
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, ErrChainUnavailable), errors.Is(err, ErrSequenceExhausted):
		return http.StatusServiceUnavailable
	case errors.Is(err, ErrDuplicate):
		return http.StatusConflict
	case errors.Is(err, ErrQueueFull), errors.Is(err, ErrOwnerQueue):
		return http.StatusTooManyRequests
	default:
		return http.StatusBadRequest
	}
}

func writeAdmit(w http.ResponseWriter, code int, rec Receipt, err error) {
	resp := admitResponse{
		Accepted:          err == nil,
		SequencerPosition: rec.Position,
		Status:            string(rec.Status),
		Provisional:       err == nil && rec.Provisional,
	}
	if err != nil {
		resp.Error = err.Error()
		resp.Provisional = false
	}
	writeJSON(w, code, resp)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeCommandJSON(body []byte) (canonical.Command, error) {
	if len(bytesTrimSpace(body)) == 0 || !utf8.Valid(body) {
		return canonical.Command{}, ErrMalformed
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	var req commandRequest
	if err := dec.Decode(&req); err != nil {
		return canonical.Command{}, ErrMalformed
	}
	if dec.More() {
		return canonical.Command{}, ErrMalformed
	}
	owner, err := sdk.AccAddressFromBech32(req.Owner)
	if err != nil {
		return canonical.Command{}, ErrOwner
	}
	pub, err := hex.DecodeString(req.PubKey)
	if err != nil {
		return canonical.Command{}, ErrMalformed
	}
	sig, err := hex.DecodeString(req.Signature)
	if err != nil {
		return canonical.Command{}, ErrMalformed
	}
	cmd := canonical.Command{
		ProtocolVersion:    req.ProtocolVersion,
		ChainID:            req.ChainID,
		ExchangeInstanceID: []byte(req.ExchangeInstanceID),
		Owner:              owner,
		Nonce:              req.CommandNonce,
		PubKey:             pub,
		Signature:          sig,
	}
	switch strings.ToLower(req.CommandType) {
	case "place":
		if req.Place == nil || req.Cancel != nil {
			return canonical.Command{}, ErrMalformed
		}
		side, err := parseSide(req.Place.Side)
		if err != nil {
			return canonical.Command{}, err
		}
		orderType, err := parseOrderType(req.Place.OrderType)
		if err != nil {
			return canonical.Command{}, err
		}
		tif, err := parseTIF(req.Place.TimeInForce)
		if err != nil {
			return canonical.Command{}, err
		}
		cmd.Type = canonical.CommandTypePlace
		cmd.Place = &canonical.Place{
			MarketID:      domain.MarketID(req.Place.MarketID),
			Side:          side,
			Type:          orderType,
			TimeInForce:   tif,
			Quantity:      domain.Quantity(req.Place.QuantityLots),
			Price:         domain.Price(req.Place.PriceTicks),
			ExpiryHeight:  req.Place.ExpiryHeight,
			ClientOrderID: []byte(req.Place.ClientOrderID),
		}
	case "cancel":
		if req.Cancel == nil || req.Place != nil {
			return canonical.Command{}, ErrMalformed
		}
		raw, err := hex.DecodeString(req.Cancel.OrderID)
		if err != nil || len(raw) != len(domain.OrderID{}) {
			return canonical.Command{}, ErrMalformed
		}
		var id domain.OrderID
		copy(id[:], raw)
		cmd.Type = canonical.CommandTypeCancel
		cmd.Cancel = &canonical.Cancel{OrderID: id}
	default:
		return canonical.Command{}, ErrUnsupportedCommand
	}
	return cmd, nil
}

func parseSide(s string) (domain.Side, error) {
	switch strings.ToLower(s) {
	case "buy":
		return domain.SideBuy, nil
	case "sell":
		return domain.SideSell, nil
	default:
		return 0, ErrMalformed
	}
}

func parseOrderType(s string) (domain.OrderType, error) {
	switch strings.ToLower(s) {
	case "limit":
		return domain.OrderTypeLimit, nil
	case "market":
		return domain.OrderTypeMarket, nil
	default:
		return 0, ErrMalformed
	}
}

func parseTIF(s string) (domain.TimeInForce, error) {
	switch strings.ToLower(s) {
	case "gtc":
		return domain.TimeInForceGTC, nil
	case "ioc":
		return domain.TimeInForceIOC, nil
	case "fok":
		return domain.TimeInForceFOK, nil
	case "gtd":
		return domain.TimeInForceGTD, nil
	default:
		return 0, ErrMalformed
	}
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
