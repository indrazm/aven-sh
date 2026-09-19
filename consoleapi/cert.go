// Package consoleapi serves the authenticated HTTPS API that the
// console.aven.sh web app talks to. The listener binds 127.0.0.1 only and
// presents a leaf certificate for daemon.<suffix> signed by the aven CA, so
// the user's browser — which already trusts the CA — connects without
// warnings. Cloudflare (which serves the static console) never sees any
// user traffic.
package consoleapi

import (
	"crypto"
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

// EnsureLeafCert issues (or reuses) a TLS leaf certificate for the console
// listener, signed by the aven root CA stored by Caddy. The leaf is valid
// for host (daemon.<suffix>), localhost and the loopback addresses, valid
// for ~27 months, and regenerated when within 30 days of expiry.
func EnsureLeafCert(caCertFile, caKeyFile, outDir, host string) (certFile, keyFile string, err error) {
	certFile = filepath.Join(outDir, "console-cert.pem")
	keyFile = filepath.Join(outDir, "console-key.pem")

	if reuse(certFile, keyFile) {
		return certFile, keyFile, nil
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return "", "", err
	}

	caPEM, err := os.ReadFile(caCertFile)
	if err != nil {
		return "", "", fmt.Errorf("read CA cert: %w", err)
	}
	caBlock, _ := pem.Decode(caPEM)
	if caBlock == nil {
		return "", "", fmt.Errorf("CA cert: invalid PEM")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return "", "", fmt.Errorf("parse CA cert: %w", err)
	}
	caKeyPEM, err := os.ReadFile(caKeyFile)
	if err != nil {
		return "", "", fmt.Errorf("read CA key: %w", err)
	}
	caKeyBlock, _ := pem.Decode(caKeyPEM)
	if caKeyBlock == nil {
		return "", "", fmt.Errorf("CA key: invalid PEM")
	}
	caKeyAny, err := parsePrivateKey(caKeyBlock.Bytes)
	if err != nil {
		return "", "", fmt.Errorf("parse CA key: %w", err)
	}
	caSigner, ok := caKeyAny.(crypto.Signer)
	if !ok {
		return "", "", fmt.Errorf("CA key is not a signer")
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{Organization: []string{"aven"}, CommonName: "aven console"},
		DNSNames:     []string{host, "localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(0, 0, 825),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, caCert, &leafKey.PublicKey, caSigner)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return "", "", err
	}
	if err := writePEM(certFile, "CERTIFICATE", der, 0o644); err != nil {
		return "", "", err
	}
	if err := writePEM(keyFile, "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return "", "", err
	}
	return certFile, keyFile, nil
}

// parsePrivateKey handles the encodings Caddy/smallstep may store: PKCS#8,
// SEC1 EC and PKCS#1 RSA.
func parsePrivateKey(der []byte) (crypto.Signer, error) {
	if key, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		signer, ok := key.(crypto.Signer)
		if !ok {
			return nil, fmt.Errorf("key is not a signer")
		}
		return signer, nil
	}
	if key, err := x509.ParseECPrivateKey(der); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("unsupported key encoding")
}

func reuse(certFile, keyFile string) bool {
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return false
	}
	if _, err := os.Stat(keyFile); err != nil {
		return false
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	return time.Until(cert.NotAfter) > 30*24*time.Hour
}

func writePEM(file, blockType string, der []byte, mode os.FileMode) error {
	return os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), mode)
}
