package database

import (
	"context"
	"sync"
	"testing"
)

// A first-time connection to a server is a get-then-create sequence, so two
// requests arriving together both find no row and both try to insert. Before
// this was an upsert, the loser failed with "UNIQUE constraint failed:
// servers.name" and that surfaced to the client as a gate-check error on a
// request that was entirely valid.
//
// This is not a theoretical race. Any client that pipelines requests hits it
// on its first connection to a new server, which includes anything driving the
// gateway through `mcp-shield connect`.
func TestConcurrentCreateServerYieldsOneRowAndNoError(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	const goroutines = 16
	var wg sync.WaitGroup
	ids := make([]int64, goroutines)
	errs := make([]error, goroutines)
	start := make(chan struct{})

	for i := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // widen the window: everyone races from the same instant
			srv, err := store.CreateServer(ctx, "calendar", "")
			errs[i] = err
			if err == nil {
				ids[i] = srv.ID
			}
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d: CreateServer returned %v, want nil", i, err)
		}
	}
	// Every caller must agree on the same row, not merely avoid erroring:
	// a caller that got a different id would go on to attach manifests to a
	// duplicate server.
	for i, id := range ids {
		if id != ids[0] {
			t.Errorf("goroutine %d got server id %d, want %d — callers disagree on the row", i, id, ids[0])
		}
	}

	servers, err := store.ListServers(ctx)
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("got %d servers named calendar, want exactly 1", len(servers))
	}
}

// CreateServer for a name that already exists returns the existing row rather
// than erroring, and must not rewrite the endpoint it was registered with —
// otherwise a concurrent first-touch could mutate an existing registration.
func TestCreateServerIsIdempotentAndDoesNotRewriteEndpoint(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	first, err := store.CreateServer(ctx, "calendar", "original-endpoint")
	if err != nil {
		t.Fatalf("first CreateServer: %v", err)
	}

	second, err := store.CreateServer(ctx, "calendar", "different-endpoint")
	if err != nil {
		t.Fatalf("second CreateServer: %v", err)
	}

	if second.ID != first.ID {
		t.Errorf("second CreateServer returned id %d, want the existing %d", second.ID, first.ID)
	}
	if second.Endpoint != "original-endpoint" {
		t.Errorf("endpoint = %q, want it left as %q", second.Endpoint, "original-endpoint")
	}
}
