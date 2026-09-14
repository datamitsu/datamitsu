package cmd

import "testing"

func TestConfigCacheKeepsRuntimeEnvSymbolic(t *testing.T) {
	isolateCacheTree(t)
	path := writeStandaloneConfig(t, `return {
  apps: {
   proxy: {shell: {name: "echo"}, dependsOn: ["upstream"], runtimeEnv: {UPSTREAM: "${APP_BIN:upstream}", DIR: "${APP_DIR}", STORE: "${STORE}"}},
   upstream: {binary: {}}
  }
 };`)
	for _, warm := range []bool{false, true} {
		cfg, _, vm := loadCached(t, path)
		if servedFromCache(vm) != warm {
			t.Fatalf("cache hit = %v, want %v", servedFromCache(vm), warm)
		}
		for key, want := range map[string]string{"UPSTREAM": "${APP_BIN:upstream}", "DIR": "${APP_DIR}", "STORE": "${STORE}"} {
			if got := cfg.Apps["proxy"].RuntimeEnv[key]; got != want {
				t.Fatalf("%s = %q, want %q", key, got, want)
			}
		}
	}
}

func TestConfigAppBinaryLiteralsInData(t *testing.T) {
	isolateCacheTree(t)
	path := writeStandaloneConfig(t, `return {
  apps: {note: {
   shell: {name: "echo", args: ["${APP_BIN:example}"]},
   description: "Use ${APP_BIN:example} in runtimeEnv",
   env: {"${APP_BIN:env-key}": "literal key"},
   runtimeEnv: {"${APP_BIN:runtime-key}": "literal key"}
  }},
  sharedStorage: {docs: "Use ${APP_BIN:example} in runtimeEnv"}
 };`)
	for _, warm := range []bool{false, true} {
		cfg, _, vm := loadCached(t, path)
		if servedFromCache(vm) != warm {
			t.Fatalf("cache hit = %v, want %v", servedFromCache(vm), warm)
		}
		if got := cfg.SharedStorage["docs"]; got != "Use ${APP_BIN:example} in runtimeEnv" {
			t.Fatalf("docs = %q", got)
		}
		if got := cfg.Apps["note"].RuntimeEnv["${APP_BIN:runtime-key}"]; got != "literal key" {
			t.Fatalf("literal env key = %q", got)
		}
	}
}
