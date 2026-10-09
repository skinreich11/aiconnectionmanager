package connection

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func EnsureCertificatePair(certificatePath, keyPath string) error {
	certificateExists, err := pathExists(certificatePath)
	if err != nil {
		return fmt.Errorf("check certificate file: %w", err)
	}
	keyExists, err := pathExists(keyPath)
	if err != nil {
		return fmt.Errorf("check private key file: %w", err)
	}

	switch {
	case certificateExists && keyExists:
		return nil
	case certificateExists || keyExists:
		return fmt.Errorf("certificate and private key must be provided together")
	}

	if err := os.MkdirAll(filepath.Dir(certificatePath), 0o700); err != nil {
		return fmt.Errorf("create certificate directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return fmt.Errorf("create private key directory: %w", err)
	}

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate private key: %w", err)
	}

	serialNumber, err := randomSerialNumber()
	if err != nil {
		return fmt.Errorf("generate certificate serial number: %w", err)
	}

	now := time.Now()
	certificateTemplate := &x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               pkix.Name{CommonName: "AIGateway local WebSocket"},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}

	certificateDER, err := x509.CreateCertificate(rand.Reader, certificateTemplate, certificateTemplate, &privateKey.PublicKey, privateKey)
	if err != nil {
		return fmt.Errorf("create certificate: %w", err)
	}
	privateKeyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("encode private key: %w", err)
	}

	if err := writeNewFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKeyDER}), 0o600); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}
	if err := writeNewFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0o644); err != nil {
		return fmt.Errorf("write certificate: %w", err)
	}

	return nil
}

func randomSerialNumber() (*big.Int, error) {
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, serialNumberLimit)
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, err
	}
}

func writeNewFile(path string, contents []byte, permission os.FileMode) error {
	// #nosec G304 -- certificate and key paths are explicit local configuration.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, permission)
	if err != nil {
		return err
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
