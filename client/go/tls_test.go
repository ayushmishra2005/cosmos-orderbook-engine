package orderbook

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"
)

func TestGRPCCredentials(t *testing.T) {
	target, creds, err := grpcCredentials("127.0.0.1:9090")
	if err != nil || target != "127.0.0.1:9090" || creds.Info().SecurityProtocol != "insecure" {
		t.Fatalf("bare %s %s %v", target, creds.Info().SecurityProtocol, err)
	}
	target, creds, err = grpcCredentials("http://127.0.0.1:9090")
	if err != nil || creds.Info().SecurityProtocol != "insecure" {
		t.Fatalf("http %s %v", creds.Info().SecurityProtocol, err)
	}
	_, creds, err = grpcCredentials("tcp://127.0.0.1:9090")
	if err != nil || creds.Info().SecurityProtocol != "insecure" {
		t.Fatal(err)
	}
	target, creds, err = grpcCredentials("https://node.example:443")
	if err != nil || target != "node.example:443" {
		t.Fatal(target, err)
	}
	if creds.Info().SecurityProtocol != "tls" || creds.Info().ServerName != "node.example" {
		t.Fatalf("%+v", creds.Info())
	}
	if _, _, err := grpcCredentials("ftp://127.0.0.1:9090"); err == nil {
		t.Fatal("accepted unsupported scheme")
	}
}

func TestHTTPSDoesNotSkipCertificateVerification(t *testing.T) {
	_, creds, err := grpcCredentials("https://node.example:443")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	server := tls.NewListener(ln, &tls.Config{
		Certificates: []tls.Certificate{selfSigned(t)},
		MinVersion:   tls.VersionTLS12,
	})
	go func() {
		conn, err := server.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		_, _ = conn.Read(make([]byte, 1))
	}()
	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, err = creds.ClientHandshake(ctx, "node.example:443", raw)
	if err == nil {
		t.Fatal("https credentials accepted an untrusted certificate")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		DNSNames:     []string{"node.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
