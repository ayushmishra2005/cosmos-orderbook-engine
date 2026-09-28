package sequencer

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ayushmishra2005/cosmos-orderbook-engine/pkg/domain"
)

func TestCommandSizeLimit(t *testing.T) {
	cfg := testConfig(t)
	cmd := signPlace(t, newKey(), cfg, 1, domain.SideSell)
	encoded, err := encodeSigned(cmd)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxCommandBytes = len(encoded)
	svc := openTest(t, cfg, nil)
	defer svc.Close()
	if _, err := svc.Admit(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	cfg2 := testConfig(t)
	cfg2.MaxCommandBytes = len(encoded) - 1
	svc2 := openTest(t, cfg2, nil)
	defer svc2.Close()
	if _, err := svc2.Admit(context.Background(), cmd); !errors.Is(err, ErrTooLarge) {
		t.Fatal(err)
	}
	if len(svc2.Pending()) != 0 {
		t.Fatal("oversized command was admitted")
	}
}

func TestManyInvalidSignatures(t *testing.T) {
	cfg := testConfig(t)
	svc := openTest(t, cfg, nil)
	defer svc.Close()
	for i := 0; i < 64; i++ {
		cmd := signPlace(t, newKey(), cfg, 1, domain.SideBuy)
		cmd.Signature[0] ^= 0xff
		if _, err := svc.Admit(context.Background(), cmd); !errors.Is(err, ErrSignature) {
			t.Fatal(err)
		}
	}
	if len(svc.Pending()) != 0 || svc.NextPosition() != 1 {
		t.Fatalf("pending %d next %d", len(svc.Pending()), svc.NextPosition())
	}
}

func TestHTTPBodyLimit(t *testing.T) {
	cfg := testConfig(t)
	svc := openTest(t, cfg, nil)
	defer svc.Close()
	ts := httptest.NewServer(svc.Handler())
	defer ts.Close()

	exact := append([]byte("{"), bytes.Repeat([]byte(" "), maxRequestBody-1)...)
	res, err := http.Post(ts.URL+"/v1/commands", "application/json", bytes.NewReader(exact))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("near limit %d", res.StatusCode)
	}

	over := bytes.Repeat([]byte("a"), maxRequestBody+1)
	res, err = http.Post(ts.URL+"/v1/commands", "application/json", bytes.NewReader(over))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("over limit %d", res.StatusCode)
	}
	if len(svc.Pending()) != 0 {
		t.Fatal("rejected body was admitted")
	}

	cmd := signPlace(t, newKey(), cfg, 1, domain.SideSell)
	body := commandJSON(t, cmd)
	if len(body) >= maxRequestBody {
		t.Fatal("fixture is not a normal request")
	}
	res, err = http.Post(ts.URL+"/v1/commands", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("valid %d", res.StatusCode)
	}
}
