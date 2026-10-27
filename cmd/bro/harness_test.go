package main

import (
	"net/http/httptest"
	"testing"

	"github.com/eusean-tg/bro/internal/client"
	"github.com/eusean-tg/bro/internal/server"
	"github.com/eusean-tg/bro/internal/store"
)

// clientFor serves s in-process and returns a client talking to it, so the
// run* functions exercise the full client → HTTP → server → store path
// without a daemon or a socket. Tests keep s for setup and assertions.
func clientFor(t *testing.T, s *store.Store) *client.Client {
	t.Helper()

	ts := httptest.NewServer(server.New(s, "test", func() {}, func() bool { return true }))
	t.Cleanup(ts.Close)

	return &client.Client{BaseURL: ts.URL, HTTP: ts.Client(), Version: "test"}
}
