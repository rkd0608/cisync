package api

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"log/slog"
	"net/http/httptest"
)

func bytesReader(raw []byte) *bytes.Reader { return bytes.NewReader(raw) }

func recordResponse() *httptest.ResponseRecorder { return httptest.NewRecorder() }

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(discardSink{}, nil)) }

type discardSink struct{}

func (discardSink) Write(p []byte) (int, error) { return len(p), nil }

var cachedRSAKey = mustRSA()

func mustRSA() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
}

func testKeyRSA() *rsa.PrivateKey { return cachedRSAKey }
