package manifest

import (
	"crypto/sha256"
	"encoding/hex"
)

// Hash returns the hex-encoded SHA256 digest of canonical manifest bytes.
// This becomes the version identity of an upstream MCP server's capability
// set — see Canonicalize for the determinism guarantees this relies on.
func Hash(canonicalJSON []byte) string {
	sum := sha256.Sum256(canonicalJSON)
	return hex.EncodeToString(sum[:])
}
