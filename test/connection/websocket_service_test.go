package connection_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aiconnectionmanager/internal/app"
	"aiconnectionmanager/internal/client"
	connectionpkg "aiconnectionmanager/internal/connection"
	serverpkg "aiconnectionmanager/internal/server"
	"github.com/gorilla/websocket"
)

func TestWebSocketHandlerSendsGreetingAndKeepsConnectionOpen(t *testing.T) {
	const interval = 10 * time.Millisecond

	server := httptest.NewTLSServer(connectionpkg.NewWebSocketHandler(interval, connectionpkg.NewConnectionRegistry("test")))
	defer server.Close()

	dialer := websocket.Dialer{
		TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
	}
	connectionURL := "wss" + strings.TrimPrefix(server.URL, "https") + connectionpkg.WebSocketPath
	connection, _, err := dialer.Dial(connectionURL, nil)
	if err != nil {
		t.Fatalf("dial secure websocket: %v", err)
	}
	defer connection.Close()

	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	for messageNumber := 0; messageNumber < 2; messageNumber++ {
		messageType, message, err := connection.ReadMessage()
		if err != nil {
			t.Fatalf("read greeting %d: %v", messageNumber+1, err)
		}
		if messageType != websocket.TextMessage {
			t.Fatalf("message type = %d, want text message", messageType)
		}
		if string(message) != connectionpkg.GreetingMessage {
			t.Fatalf("message = %q, want %q", message, connectionpkg.GreetingMessage)
		}
	}
}

func TestWebSocketHandlerRejectsUnknownPath(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://localhost/unknown", nil)
	response := httptest.NewRecorder()

	connectionpkg.NewWebSocketHandler(time.Second, connectionpkg.NewConnectionRegistry("test")).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestWebSocketHandlerRegistersAndRemovesConnections(t *testing.T) {
	registry := connectionpkg.NewConnectionRegistry("test")
	server := httptest.NewTLSServer(connectionpkg.NewWebSocketHandler(5*time.Millisecond, registry))
	defer server.Close()

	dialer := websocket.Dialer{
		TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig,
	}
	headers := http.Header{}
	headers.Set(connectionpkg.ClientIDHeader, "terminal-1")
	connectionURL := "wss" + strings.TrimPrefix(server.URL, "https") + connectionpkg.WebSocketPath
	connection, _, err := dialer.Dial(connectionURL, headers)
	if err != nil {
		t.Fatalf("dial secure websocket: %v", err)
	}

	waitForRegistrySize(t, registry, 1)
	connections := registry.Snapshot()
	if len(connections) != 1 || connections[0].ClientID != "terminal-1" || connections[0].Endpoint == "" || connections[0].Port == "" || connections[0].WebSocket == nil {
		t.Fatalf("registered connection = %+v", connections)
	}

	if err := connection.Close(); err != nil {
		t.Fatalf("close client connection: %v", err)
	}
	waitForRegistrySize(t, registry, 0)
}

func TestRunClientPrintsGreetingsUntilContextIsCanceled(t *testing.T) {
	server := httptest.NewTLSServer(connectionpkg.NewWebSocketHandler(5*time.Millisecond, connectionpkg.NewConnectionRegistry("test")))
	defer server.Close()

	certificatePath := writeCertificateFile(t, server.Certificate())
	connectionURL := "wss" + strings.TrimPrefix(server.URL, "https") + connectionpkg.WebSocketPath
	clientContext, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var output bytes.Buffer

	if err := client.Run(clientContext, connectionURL, certificatePath, &output); err != nil {
		t.Fatalf("run client: %v", err)
	}
	if !strings.Contains(output.String(), connectionpkg.GreetingMessage+"\n") {
		t.Fatalf("client output = %q, want at least one greeting", output.String())
	}
}

func TestRunClientValidatesSecureURLAndCA(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "missing URL", url: "", want: "websocket URL is required"},
		{name: "insecure scheme", url: "ws://localhost/ws", want: "websocket URL must use wss scheme"},
		{name: "missing host", url: "wss:///ws", want: "websocket URL must include a host"},
		{name: "malformed URL", url: "wss://[::1", want: "parse websocket URL"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := client.Run(context.Background(), test.url, "unused.pem", &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestLoadRootCAsRejectsMissingAndInvalidCertificates(t *testing.T) {
	if _, err := client.LoadRootCAs(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("loadRootCAs should reject a missing certificate")
	}

	invalidPath := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(invalidPath, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write invalid certificate: %v", err)
	}
	if _, err := client.LoadRootCAs(invalidPath); err == nil {
		t.Fatal("loadRootCAs should reject invalid PEM")
	}
}

func TestEnsureCertificatePairCreatesTrustedLocalCertificate(t *testing.T) {
	certificatePath := filepath.Join(t.TempDir(), "tls", "server.crt")
	keyPath := filepath.Join(filepath.Dir(certificatePath), "server.key")

	if err := connectionpkg.EnsureCertificatePair(certificatePath, keyPath); err != nil {
		t.Fatalf("create certificate pair: %v", err)
	}

	certificate, err := tls.LoadX509KeyPair(certificatePath, keyPath)
	if err != nil {
		t.Fatalf("load generated certificate pair: %v", err)
	}
	parsedCertificate, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		t.Fatalf("parse generated certificate: %v", err)
	}
	if !parsedCertificate.IsCA {
		t.Fatal("generated certificate must be a local CA so the client can trust it")
	}
	if len(parsedCertificate.IPAddresses) == 0 || len(parsedCertificate.DNSNames) == 0 {
		t.Fatalf("generated certificate must include loopback IP and localhost SANs: %+v", parsedCertificate)
	}

	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat generated private key: %v", err)
	}
	if keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("private key permissions = %o, want 600", keyInfo.Mode().Perm())
	}
	if err := connectionpkg.EnsureCertificatePair(certificatePath, keyPath); err != nil {
		t.Fatalf("reuse existing certificate pair: %v", err)
	}
}

func TestEnsureCertificatePairRequiresCertificateAndKeyTogether(t *testing.T) {
	tempDirectory := t.TempDir()
	certificatePath := filepath.Join(tempDirectory, "server.crt")
	keyPath := filepath.Join(tempDirectory, "server.key")
	if err := os.WriteFile(certificatePath, []byte("certificate"), 0o644); err != nil {
		t.Fatalf("write certificate placeholder: %v", err)
	}

	if err := connectionpkg.EnsureCertificatePair(certificatePath, keyPath); err == nil {
		t.Fatal("ensureCertificatePair should reject an incomplete pair")
	}
}

func TestEnsureCertificatePairSupportsFreshPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := connectionpkg.EnsureCertificatePair(path+".crt", path+".key"); err != nil {
		t.Fatalf("create certificate pair: %v", err)
	}
}

func TestRunServerStartsAndStops(t *testing.T) {
	tempDirectory := t.TempDir()
	serverContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 1)
	logger := log.New(notificationWriter{notifications: started}, "", 0)
	result := make(chan error, 1)

	go func() {
		result <- serverpkg.Run(serverContext, serverpkg.Config{
			Address:         "127.0.0.1:0",
			CertificatePath: filepath.Join(tempDirectory, "server.crt"),
			KeyPath:         filepath.Join(tempDirectory, "server.key"),
			Interval:        time.Hour,
			ReportInterval:  time.Hour,
		}, logger)
	}()

	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("server did not start")
	}

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("stop server: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
}

func TestRunServerValidatesConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		config serverpkg.Config
		want   string
	}{
		{name: "missing address", config: serverpkg.Config{Interval: time.Second}, want: "server address is required"},
		{name: "invalid interval", config: serverpkg.Config{Address: "127.0.0.1:0"}, want: "message interval must be positive"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := serverpkg.Run(context.Background(), test.config, log.Default())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestRunAndCommandUsage(t *testing.T) {
	var output bytes.Buffer
	if exitCode := app.Run(nil, &output, &output); exitCode != 2 {
		t.Fatalf("empty command exit code = %d, want 2", exitCode)
	}
	if exitCode := app.Run([]string{"help"}, &output, &output); exitCode != 0 {
		t.Fatalf("help exit code = %d, want 0", exitCode)
	}
	if exitCode := app.Run([]string{"unknown"}, &output, &output); exitCode != 1 {
		t.Fatalf("unknown command exit code = %d, want 1", exitCode)
	}
	if exitCode := app.Run([]string{"server", "-addr", "", "-interval", "1s"}, &output, &output); exitCode != 1 {
		t.Fatal("server command should reject an empty address")
	}
	if exitCode := app.Run([]string{"client", "-url", "ws://localhost/ws"}, &output, &output); exitCode != 1 {
		t.Fatal("client command should reject an insecure URL")
	}
}

func writeCertificateFile(t *testing.T, certificate *x509.Certificate) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.crt")
	contents := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	return path
}

func waitForRegistrySize(t *testing.T, registry *connectionpkg.ConnectionRegistry, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(registry.Snapshot()) == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("registry size = %d, want %d", len(registry.Snapshot()), want)
}

type notificationWriter struct {
	notifications chan<- struct{}
}

func (writer notificationWriter) Write(contents []byte) (int, error) {
	select {
	case writer.notifications <- struct{}{}:
	default:
	}
	return len(contents), nil
}
