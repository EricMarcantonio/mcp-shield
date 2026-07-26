package manifest

import (
	"encoding/json"
	"math/rand"
	"testing"

	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

// shufflePrimitiveArrays deterministically permutes every all-primitive
// array in a decoded JSON value. Object-containing arrays keep their order
// (the canonicalizer deliberately preserves it — anyOf/oneOf semantics).
func shufflePrimitiveArrays(v any, rng *rand.Rand) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, child := range val {
			out[k] = shufflePrimitiveArrays(child, rng)
		}
		return out
	case []any:
		out := make([]any, len(val))
		allPrimitive := true
		for i, elem := range val {
			out[i] = shufflePrimitiveArrays(elem, rng)
			switch elem.(type) {
			case string, float64, bool, nil:
			default:
				allPrimitive = false
			}
		}
		if allPrimitive {
			rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		}
		return out
	default:
		return val
	}
}

// FuzzCanonicalizeValueStable asserts the two properties the entire trust
// model depends on: canonical output is a fixed point (P1, idempotence) and
// valid JSON (P3), and reordering a primitive-only array in the input never
// changes the canonical output (P2) — a manifest hash must not depend on
// incidental JSON array order.
func FuzzCanonicalizeValueStable(f *testing.F) {
	f.Add([]byte(`{"b":1,"a":[3,2,1],"c":{"y":true,"x":null}}`), int64(1))
	f.Add([]byte(`[["a","c","b"],{"k":[1,2]}]`), int64(2))
	f.Add([]byte(`{"anyOf":[{"type":"string"},{"type":"number"}]}`), int64(3))
	f.Fuzz(func(t *testing.T, raw []byte, seed int64) {
		c1, err := CanonicalizeValue(raw)
		if err != nil || c1 == "" {
			t.Skip()
		}
		// P1: idempotence — canonical form is a fixed point.
		c2, err := CanonicalizeValue([]byte(c1))
		if err != nil {
			t.Fatalf("canonical output failed to re-canonicalize: %v\n%s", err, c1)
		}
		if c1 != c2 {
			t.Fatalf("not idempotent:\nfirst:  %s\nsecond: %s", c1, c2)
		}
		// P3: canonical output is valid JSON.
		if !json.Valid([]byte(c1)) {
			t.Fatalf("canonical output is not valid JSON: %s", c1)
		}
		// P2: permuting primitive-only arrays never changes the canonical form.
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Skip()
		}
		sb, err := json.Marshal(shufflePrimitiveArrays(v, rand.New(rand.NewSource(seed))))
		if err != nil {
			t.Skip()
		}
		c3, err := CanonicalizeValue(sb)
		if err != nil {
			t.Fatalf("shuffled form failed to canonicalize: %v", err)
		}
		if c1 != c3 {
			t.Fatalf("primitive-array order changed canonical form:\norig:     %s\nshuffled: %s", c1, c3)
		}
	})
}

// FuzzManifestHashOrderInvariance asserts the property the README's trust
// model is built on: Hash(Canonicalize(m)) must not depend on the order the
// upstream server advertised its tools in. If it did, an operator could
// approve one ordering and have the gateway silently treat a
// differently-ordered (but identical) capability set as a brand new,
// unapproved manifest — or worse, two different capability sets could
// collide onto the same approved hash.
//
// The name1 == name2 case used to be skipped, because Build sorted tools
// with sort.Slice keyed only on Name — neither stable nor a total order, so
// two tools sharing a name made the hash depend on advertised order. Phase 5
// resolved that by having Build reject duplicate identities outright (see
// manifest.validateUniqueIdentities for why fail-closed beats normalizing),
// so the target now asserts the property unconditionally: for any pair of
// orderings, Build either rejects both or produces one hash. The seed corpus
// entry testdata/fuzz/FuzzManifestHashOrderInvariance/
// duplicate_tool_name_hash_nondeterminism, which reproduced the original bug,
// is kept as a permanent regression guard.
func FuzzManifestHashOrderInvariance(f *testing.F) {
	f.Add("calendar_read", "calendar_create", "reads events", []byte(`{"type":"object"}`))
	f.Add("a", "a", "duplicate names", []byte(`{"x":1}`))
	f.Fuzz(func(t *testing.T, name1, name2, desc string, schema []byte) {
		if len(schema) > 0 && !json.Valid(schema) {
			t.Skip()
		}
		t1 := mcp.Tool{Name: name1, Description: desc, InputSchema: schema}
		t2 := mcp.Tool{Name: name2, InputSchema: json.RawMessage(`{"a":1,"b":2}`)}

		forward, forwardErr := Build([]mcp.Tool{t1, t2}, nil, nil)
		reverse, reverseErr := Build([]mcp.Tool{t2, t1}, nil, nil)

		// Admissibility must not depend on advertised order either: a
		// capability set an operator can approve in one ordering must not
		// become inadmissible in another.
		if (forwardErr == nil) != (reverseErr == nil) {
			t.Fatalf("Build accepted one ordering and rejected the other: forward=%v reverse=%v", forwardErr, reverseErr)
		}
		if forwardErr != nil {
			return
		}

		ca, err := Canonicalize(forward)
		if err != nil {
			t.Skip()
		}
		cb, err := Canonicalize(reverse)
		if err != nil {
			t.Fatalf("second ordering failed where first succeeded: %v", err)
		}
		if Hash(ca) != Hash(cb) {
			t.Fatalf("hash depends on advertised tool order:\na=%s\nb=%s", ca, cb)
		}
	})
}

// FuzzFromCanonicalJSONRoundTrip asserts that a manifest reloaded from its
// own stored canonical bytes (as the approval workflow does to rebuild the
// approved baseline for diffing) hashes identically to the original — an
// approved baseline must mean the same thing before and after a database
// round-trip.
func FuzzFromCanonicalJSONRoundTrip(f *testing.F) {
	f.Add("tool_a", "reads things", []byte(`{"type":"object","properties":{"id":{"type":"string"}}}`))
	f.Fuzz(func(t *testing.T, name, desc string, schema []byte) {
		if len(schema) > 0 && !json.Valid(schema) {
			t.Skip()
		}
		m, err := Build([]mcp.Tool{{Name: name, Description: desc, InputSchema: schema}}, nil, nil)
		if err != nil {
			t.Fatalf("single-tool manifest must always build: %v", err)
		}
		c1, err := Canonicalize(m)
		if err != nil {
			t.Skip()
		}
		restored, err := FromCanonicalJSON(c1)
		if err != nil {
			t.Fatalf("stored canonical form failed to decode: %v\n%s", err, c1)
		}
		c2, err := Canonicalize(restored)
		if err != nil {
			t.Fatalf("restored manifest failed to canonicalize: %v", err)
		}
		if Hash(c1) != Hash(c2) {
			t.Fatalf("baseline drifts through storage round-trip:\nstored: %s\nreloaded: %s", c1, c2)
		}
	})
}
