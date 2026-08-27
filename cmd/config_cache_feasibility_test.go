package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/hashutil"

	"github.com/shamaton/msgpack/v2"
)

// Feasibility measurements for a cross-process config-evaluation cache.
//
// The question these answer is whether skipping the goja evaluation is worth it:
// the cache only pays for itself if hashing the inputs plus decoding a stored
// result costs meaningfully less than evaluating the config, and that ratio is
// the whole design decision. Arguing it from first principles is how you end up
// with a cache that is slower than the thing it replaced.
//
// They are gated on CONFIG_CACHE_FEASIBILITY, which must point at a real
// before-config, because the numbers are dominated by the size of the config
// chain and a synthetic fixture would measure nothing.
//
// The variable deliberately carries no DATAMITSU_ prefix: internal/env owns that
// namespace, env.Environ() folds every variable in it into the source-mode farm
// staleness key, and internal/clitest strips them from the golden harness. A
// measurement switch belongs in none of those:
//
//	CONFIG_CACHE_FEASIBILITY=/path/to/datamitsu.config.base.js \
//	  go test ./cmd/ -run XXX -bench 'ConfigCache' -benchtime 20x
func feasibilityConfigPath(tb testing.TB) string {
	tb.Helper()
	path := os.Getenv("CONFIG_CACHE_FEASIBILITY")
	if path == "" {
		tb.Skip("set CONFIG_CACHE_FEASIBILITY to a before-config path to run the config-cache feasibility measurements")
	}
	if _, err := os.Stat(path); err != nil {
		tb.Skipf("CONFIG_CACHE_FEASIBILITY=%s: %v", path, err)
	}
	return path
}

// loadFeasibilityConfig evaluates the chain once and returns the merged config
// with the setup content functions dropped, which is the shape a cache artifact
// could actually hold: ConfigSetup.Content is a live goja value and cannot be
// serialized.
func loadFeasibilityConfig(tb testing.TB) *config.Config {
	tb.Helper()
	path := feasibilityConfigPath(tb)

	cfg, _, _, err := loadConfigWithPaths(context.Background(), []string{path}, true, nil)
	if err != nil {
		tb.Fatalf("loading %s: %v", path, err)
	}
	for name, entry := range cfg.Setup {
		entry.Content = nil
		cfg.Setup[name] = entry
	}
	return cfg
}

// BenchmarkConfigCacheEvaluate is the cost the cache removes: the full chain
// evaluated in goja, exactly as every command paid it before this cache
// existed. The cache is switched off explicitly — measuring "evaluation" with
// it on would silently measure a hit.
func BenchmarkConfigCacheEvaluate(b *testing.B) {
	path := feasibilityConfigPath(b)
	b.Setenv("DATAMITSU_CONFIG_CACHE", "0")
	for b.Loop() {
		if _, _, _, err := loadConfigWithPaths(context.Background(), []string{path}, true, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkConfigCacheHit is the acceptance number for the whole plan: the same
// chain served from the cross-process cache instead of evaluated. Compare it
// against BenchmarkConfigCacheEvaluate — that ratio is what this plan bought.
//
// The chain is passed as a --config path rather than a --before-config one so
// the load has an explicit chain to be namespaced by; with neither a git root
// nor an explicit chain there is nothing to key a namespace on and the loader
// deliberately does not cache.
func BenchmarkConfigCacheHit(b *testing.B) {
	path := feasibilityConfigPath(b)

	if _, _, _, err := loadConfigWithPaths(context.Background(), nil, true, []string{path}); err != nil {
		b.Fatal(err)
	}
	// A nil VM is the hit signal: a load that evaluated returns the last
	// layer's runtime. Failing here beats reporting an evaluation as a hit.
	_, _, vm, err := loadConfigWithPaths(context.Background(), nil, true, []string{path})
	if err != nil {
		b.Fatal(err)
	}
	if vm != nil {
		b.Fatal("the warm-up load evaluated instead of hitting the cache; this would measure evaluation, not a hit")
	}

	for b.Loop() {
		if _, _, _, err := loadConfigWithPaths(context.Background(), nil, true, []string{path}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkConfigCacheKeyXXH3 is the cost the cache would add on the read side:
// reading every byte of the config chain and hashing it into a cache key.
// XXH3 per the repository's hashing policy — this is an internal cache key over
// local files, never compared against a value from the network.
func BenchmarkConfigCacheKeyXXH3(b *testing.B) {
	path := feasibilityConfigPath(b)
	b.Run("read+hash", func(b *testing.B) {
		for b.Loop() {
			data, err := os.ReadFile(path)
			if err != nil {
				b.Fatal(err)
			}
			_ = hashutil.XXH3Hex(data)
		}
	})
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("hash-only", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		for b.Loop() {
			_ = hashutil.XXH3Hex(data)
		}
	})
	b.Run("sha256-only", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		for b.Loop() {
			_ = sha256.Sum256(data)
		}
	})
	b.Run("stat-only", func(b *testing.B) {
		for b.Loop() {
			if _, err := os.Stat(path); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkConfigCacheSerialize measures both halves of the artifact: writing it
// on a miss and — the number that matters — decoding it on a hit.
func BenchmarkConfigCacheSerialize(b *testing.B) {
	cfg := loadFeasibilityConfig(b)

	msgpackBytes, err := msgpack.Marshal(cfg)
	if err != nil {
		b.Fatalf("msgpack.Marshal: %v", err)
	}
	jsonBytes, err := json.Marshal(cfg)
	if err != nil {
		b.Fatalf("json.Marshal: %v", err)
	}
	b.Logf("artifact size: msgpack %d B, json %d B (apps=%d tools=%d setup=%d)",
		len(msgpackBytes), len(jsonBytes), len(cfg.Apps), len(cfg.Tools), len(cfg.Setup))

	b.Run("msgpack-marshal", func(b *testing.B) {
		for b.Loop() {
			if _, err := msgpack.Marshal(cfg); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("msgpack-unmarshal", func(b *testing.B) {
		for b.Loop() {
			var out config.Config
			if err := msgpack.Unmarshal(msgpackBytes, &out); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("json-marshal", func(b *testing.B) {
		for b.Loop() {
			if _, err := json.Marshal(cfg); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("json-unmarshal", func(b *testing.B) {
		for b.Loop() {
			var out config.Config
			if err := json.Unmarshal(jsonBytes, &out); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("read-artifact-from-disk", func(b *testing.B) {
		dir := b.TempDir()
		artifact := dir + "/config.msgpack"
		if err := os.WriteFile(artifact, msgpackBytes, 0o600); err != nil {
			b.Fatal(err)
		}
		for b.Loop() {
			data, err := os.ReadFile(artifact)
			if err != nil {
				b.Fatal(err)
			}
			var out config.Config
			if err := msgpack.Unmarshal(data, &out); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestConfigCacheRoundTripsThroughMsgpack checks that a decoded artifact
// actually reproduces the config, which is the precondition for any of the
// timings above meaning anything. A serializer that silently drops a field would
// make the cache fast and wrong.
func TestConfigCacheRoundTripsThroughMsgpack(t *testing.T) {
	cfg := loadFeasibilityConfig(t)

	encoded, err := msgpack.Marshal(cfg)
	if err != nil {
		t.Fatalf("msgpack.Marshal: %v", err)
	}
	var decoded config.Config
	if err := msgpack.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("msgpack.Unmarshal: %v", err)
	}

	if len(decoded.Apps) != len(cfg.Apps) {
		t.Errorf("apps: decoded %d, want %d", len(decoded.Apps), len(cfg.Apps))
	}
	if len(decoded.Tools) != len(cfg.Tools) {
		t.Errorf("tools: decoded %d, want %d", len(decoded.Tools), len(cfg.Tools))
	}
	if len(decoded.Setup) != len(cfg.Setup) {
		t.Errorf("setup: decoded %d, want %d", len(decoded.Setup), len(cfg.Setup))
	}
	if len(decoded.Runtimes) != len(cfg.Runtimes) {
		t.Errorf("runtimes: decoded %d, want %d", len(decoded.Runtimes), len(cfg.Runtimes))
	}

	// The whole graph, not just the counts: a per-field comparison is what
	// catches a type msgpack cannot represent.
	before, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(&decoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("config did not survive the msgpack round trip (%d B before, %d B after)",
			len(before), len(after))
	}
}
