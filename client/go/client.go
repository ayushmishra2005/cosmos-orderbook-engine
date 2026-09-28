package orderbook

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"cosmossdk.io/log/v2"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/app"
	exchangev1 "github.com/ayushmishra2005/cosmos-orderbook-engine/x/exchange/types/v1"
)

const (
	defaultBuffer       = 32
	defaultGas          = 1_000_000
	defaultReconnect    = 200 * time.Millisecond
	defaultReconnectMax = 5 * time.Second
)

// Config is the read path. It does not hold a private key.
type Config struct {
	GRPCEndpoint       string
	RPCEndpoint        string
	ChainID            string
	ExchangeInstanceID []byte
	GasLimit           uint64
	Fees               string
	SubscriberBuffer   int
	ReconnectInitial   time.Duration
	ReconnectMax       time.Duration
}

// Signer signs transactions and batch commands.
// Implementations should use a Cosmos SDK keyring or an equivalent signer.
// The client does not accept a raw private key in Config.
type Signer interface {
	Address() sdk.AccAddress
	PubKey() cryptotypes.PubKey
	Sign([]byte) ([]byte, error)
}

// Client queries and submits to one chain.
type Client struct {
	cfg      Config
	chainID  string
	instance []byte
	gas      uint64
	fees     string
	buffer   int
	delay    time.Duration
	delayMax time.Duration

	grpcConn *grpc.ClientConn
	rpc      *rpchttp.HTTP
	httpRPC  string
	wsURL    string
	cdc      codec.Codec
	registry codectypes.InterfaceRegistry
	txConfig client.TxConfig

	mu     sync.Mutex
	txMu   sync.Mutex
	signer Signer

	dialLive func(context.Context) (liveConn, error)
	tip      func(context.Context) (int64, error)
	pages    func(context.Context, int64) ([]Event, error)
	decode   func([]byte) ([]Event, error)
}

// NewClient dials gRPC and CometBFT. A signer is optional.
func NewClient(cfg Config) (*Client, error) {
	if cfg.ChainID == "" || cfg.GRPCEndpoint == "" || cfg.RPCEndpoint == "" {
		return nil, fmt.Errorf("%w: chain id, grpc, and rpc are required", ErrInvalidArgument)
	}
	httpRPC, wsURL, err := normalizeRPC(cfg.RPCEndpoint)
	if err != nil {
		return nil, err
	}
	grpcTarget, err := normalizeGRPC(cfg.GRPCEndpoint)
	if err != nil {
		return nil, err
	}
	enc, err := app.MakeEncodingConfig(log.NewNopLogger())
	if err != nil {
		return nil, err
	}
	rpc, err := rpchttp.New(httpRPC, "/websocket")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRPCUnavailable, err)
	}
	if err := rpc.Start(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRPCUnavailable, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, statusErr := rpc.Status(ctx)
	cancel()
	if statusErr != nil {
		_ = rpc.Stop()
		return nil, fmt.Errorf("%w: %v", ErrRPCUnavailable, statusErr)
	}
	conn, err := grpc.NewClient(grpcTarget,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.NewProtoCodec(enc.InterfaceRegistry).GRPCCodec())),
	)
	if err != nil {
		_ = rpc.Stop()
		return nil, fmt.Errorf("%w: %v", ErrGRPCUnavailable, err)
	}
	instance := cfg.ExchangeInstanceID
	if len(instance) == 0 {
		instance = []byte(exchangev1.DefaultInstanceID)
	}
	gas := cfg.GasLimit
	if gas == 0 {
		gas = defaultGas
	}
	buffer := cfg.SubscriberBuffer
	if buffer == 0 {
		buffer = defaultBuffer
	}
	if buffer < 1 {
		_ = conn.Close()
		_ = rpc.Stop()
		return nil, fmt.Errorf("%w: subscriber buffer", ErrInvalidArgument)
	}
	delay := cfg.ReconnectInitial
	if delay == 0 {
		delay = defaultReconnect
	}
	delayMax := cfg.ReconnectMax
	if delayMax == 0 {
		delayMax = defaultReconnectMax
	}
	if delay < 0 || delayMax < 0 {
		_ = conn.Close()
		_ = rpc.Stop()
		return nil, fmt.Errorf("%w: reconnect delay", ErrInvalidArgument)
	}
	if delayMax < delay {
		delayMax = delay
	}
	if cfg.Fees != "" {
		if _, err := sdk.ParseCoinsNormalized(cfg.Fees); err != nil {
			_ = conn.Close()
			_ = rpc.Stop()
			return nil, fmt.Errorf("%w: fees", ErrInvalidArgument)
		}
	}
	c := &Client{
		cfg:      cfg,
		chainID:  cfg.ChainID,
		instance: append([]byte(nil), instance...),
		gas:      gas,
		fees:     cfg.Fees,
		buffer:   buffer,
		delay:    delay,
		delayMax: delayMax,
		grpcConn: conn,
		rpc:      rpc,
		httpRPC:  httpRPC,
		wsURL:    wsURL,
		cdc:      enc.Codec,
		registry: enc.InterfaceRegistry,
		txConfig: enc.TxConfig,
	}
	c.dialLive = c.dialComet
	c.tip = c.latestHeight
	c.pages = c.blockEvents
	c.decode = decodeMessage
	return c, nil
}

// WithSigner sets the signer used by transaction helpers.
// Batch-command helpers take a signer argument and do not read this value.
func (c *Client) WithSigner(s Signer) *Client {
	c.mu.Lock()
	c.signer = s
	c.mu.Unlock()
	return c
}

// Close closes the gRPC and RPC clients. Cancel stream contexts separately.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	var err error
	if c.grpcConn != nil {
		err = c.grpcConn.Close()
	}
	if c.rpc != nil {
		if stopErr := c.rpc.Stop(); err == nil {
			err = stopErr
		}
	}
	return err
}

func (c *Client) currentSigner() (Signer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.signer == nil {
		return nil, ErrSignerRequired
	}
	return c.signer, nil
}

func normalizeRPC(raw string) (httpURL, wsURL string, err error) {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, "tcp://"):
		raw = "http://" + strings.TrimPrefix(raw, "tcp://")
	case strings.HasPrefix(raw, "http://"), strings.HasPrefix(raw, "https://"):
	default:
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("%w: rpc endpoint", ErrInvalidArgument)
	}
	if u.Hostname() == "0.0.0.0" {
		u.Host = net.JoinHostPort("127.0.0.1", u.Port())
	}
	wsScheme := "ws"
	switch u.Scheme {
	case "https":
		wsScheme = "wss"
	case "http":
	default:
		return "", "", fmt.Errorf("%w: rpc scheme", ErrInvalidArgument)
	}
	u.Path = ""
	u.RawQuery = ""
	httpURL = u.String()
	u.Scheme = wsScheme
	u.Path = "/websocket"
	return httpURL, u.String(), nil
}

func normalizeGRPC(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "tcp://")
	raw = strings.TrimPrefix(raw, "http://")
	raw = strings.TrimPrefix(raw, "https://")
	if raw == "" || strings.Contains(raw, "://") {
		return "", fmt.Errorf("%w: grpc endpoint", ErrInvalidArgument)
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return "", fmt.Errorf("%w: grpc endpoint", ErrInvalidArgument)
	}
	if host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}
