package store

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
)

func osReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

// parseEd25519PEM extracts an Ed25519 private key from a PKCS#8 PEM block
// ("-----BEGIN PRIVATE KEY-----" as produced by openssl genpkey).
func parseEd25519PEM(pemBytes []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	if block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("unsupported PEM type %q", block.Type)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse pkcs8: %w", err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is %T, want ed25519.PrivateKey", parsed)
	}
	return key, nil
}

func checkpointMessage(seq int64, entryHash string) []byte {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d|%s", seq, entryHash)))
	return h[:]
}

// SignCheckpoint returns the Ed25519 signature over the checkpoint message.
func (sg *Signer) SignCheckpoint(seq int64, entryHash string) []byte {
	return ed25519.Sign(sg.priv, checkpointMessage(seq, entryHash))
}

// VerifyCheckpoint validates an Ed25519 checkpoint signature.
func (sg *Signer) VerifyCheckpoint(seq int64, entryHash string, sig []byte) bool {
	return ed25519.Verify(sg.priv.Public().(ed25519.PublicKey), checkpointMessage(seq, entryHash), sig)
}
