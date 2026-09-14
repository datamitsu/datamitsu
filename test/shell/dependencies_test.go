package shell_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeletedDependencyIsRepaired(t *testing.T) {
	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			f := newFixture(t)
			url := f.srv.URL + "/" + branchV1 + "/" + toolName
			hash := sha256Hex(stubScript(toolName, versionOf[branchV1]))
			js := configJS(stubApp{Name: toolName, URL: url, Hash: hash}, stubApp{Name: "git", URL: url, Hash: hash})
			js = strings.Replace(js, `"`+toolName+`": { binary:`, `"`+toolName+`": { dependsOn: ["git"], runtimeEnv: { UPSTREAM: "${APP_BIN:git}" }, binary:`, 1)
			f.writeFile("datamitsu.config.js", js)
			activation := f.activation(shell)
			depDir := filepath.Join(f.Cache, "store", ".bin", "git")
			if _, err := os.Stat(depDir); !os.IsNotExist(err) {
				t.Fatalf("activation touched dependency store: %v", err)
			}
			assertRan(t, f.runRaw(shell, activation+toolName+"\n"), branchV1)
			entries, err := os.ReadDir(depDir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("dependency entries = %v, error = %v", entries, err)
			}
			dep := filepath.Join(depDir, entries[0].Name())
			if err := os.Remove(dep); err != nil {
				t.Fatal(err)
			}
			assertRan(t, f.runRaw(shell, activation+toolName+"\n"), branchV1)
			if _, err := os.Stat(dep); err != nil {
				t.Fatalf("dependency not repaired: %v", err)
			}
		})
	}
}
