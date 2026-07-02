// SPDX-FileCopyrightText: 2026 The bink Authors
// SPDX-License-Identifier: Apache-2.0

package podman

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.podman.io/podman/v6/pkg/bindings"
)

// newTestClient creates a Client connected to a fake Podman API server.
// The handler is called for all requests except /_ping (which is handled automatically).
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_ping") {
			w.Header().Set("Libpod-API-Version", "5.5.2")
			w.WriteHeader(http.StatusOK)
			return
		}
		handler(w, r)
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	srv := &http.Server{Handler: mux}
	go srv.Serve(listener)
	t.Cleanup(func() { srv.Close() })

	uri := fmt.Sprintf("tcp://%s", listener.Addr().String())
	conn, err := bindings.NewConnection(context.Background(), uri)
	if err != nil {
		t.Fatalf("NewConnection: %v", err)
	}

	return &Client{
		conn:       conn,
		socketPath: uri,
		connected:  true,
	}
}

func TestCallerContextIgnored(t *testing.T) {
	// Simulate a slow Podman API that takes 2 seconds to respond.
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"Names":["test"]}`)
		case <-r.Context().Done():
			return
		}
	})

	client := newTestClient(t, slow)

	// The caller sets a 100ms timeout — this should cancel the request quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := client.ContainerExists(ctx, "test")
	elapsed := time.Since(start)

	// The Podman bindings retry failed requests up to 3 times with backoff
	// (100ms + 200ms + 300ms = 600ms), so the total time is ~700ms.
	// The key assertion is that the call finishes well before the server's
	// 2-second delay, proving the caller's context was propagated.
	if elapsed >= 1500*time.Millisecond {
		t.Fatalf("caller context timeout not respected: expected cancellation well before 2s server delay, call took %v", elapsed)
	}

	if err == nil {
		t.Fatal("expected context deadline exceeded error, got nil")
	}
}
