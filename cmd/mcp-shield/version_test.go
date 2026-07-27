package main

import "testing"

// A stamped version always wins: release archives and the Docker image carry
// -ldflags, and that value is authoritative over anything in build info.
func TestResolveVersionPrefersTheStampedValue(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	version = "1.2.3"
	if got := resolveVersion(); got != "1.2.3" {
		t.Fatalf("resolveVersion() = %q, want the stamped %q", got, "1.2.3")
	}
}

// Under `go test` the main module's build-info version is "(devel)", which
// carries no release information, so the unstamped default stands rather than
// the binary reporting a meaningless string.
func TestResolveVersionFallsBackWhenNothingIsStamped(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	version = "dev"
	if got := resolveVersion(); got != "dev" {
		t.Fatalf("resolveVersion() = %q, want %q for an unstamped test build", got, "dev")
	}
}
