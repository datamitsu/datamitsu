package managedconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/digest"
)

func TestAIProfileMarkerFailureRestoresProfile(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	path := filepath.Join(".codex", "agents", "dm-review.toml")
	marker := filepath.FromSlash(sidecar(path))
	if err := root.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{path: "old profile", marker: "old marker"} {
		if err := root.WriteFile(name, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	failure := errors.New("cannot publish marker")
	err = publishAIWithRename(root, path, aiArtifact{path: path, data: []byte("new profile"), files: nil}, true, func(src, dst string) error {
		if dst == marker {
			return failure
		}
		return root.Rename(src, dst)
	})
	if !errors.Is(err, failure) {
		t.Fatalf("publish error = %v", err)
	}
	for name, want := range map[string]string{path: "old profile", marker: "old marker"} {
		data, err := root.ReadFile(name)
		if err != nil || string(data) != want {
			t.Fatalf("%s after rollback = %q, %v", name, data, err)
		}
	}
}

func TestAIProtectsTrackedCaseAliases(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q")
	const path = ".agents/skills/dm-demo"
	const tracked = ".Agents/skills/dm-demo/SKILL.md"
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(tracked)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, tracked), []byte("tracked"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-f", "--", tracked)
	for _, move := range [][2]string{{".Agents", "intermediate"}, {"intermediate", ".agents"}} {
		if err := os.Rename(filepath.Join(dir, move[0]), filepath.Join(dir, move[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, path, aiDirectoryMarker), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	desired := map[string]aiArtifact{path: {path: path, files: map[string]aiFile{"SKILL.md": {data: []byte("replacement"), mode: 0o644}}, data: nil}}
	if _, err := applyAI(context.Background(), dir, desired, nil, nil); err == nil {
		t.Fatal("replacement accepted a tracked descendant through a case alias")
	}
	if changed, err := applyAI(context.Background(), dir, nil, nil, nil); err != nil || changed {
		t.Fatalf("prune changed tracked tree: changed=%v, err=%v", changed, err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, path, "SKILL.md")); err != nil || string(got) != "tracked" {
		t.Fatalf("tracked content = %q, %v", got, err)
	}
	if !trackedExactOrAncestor(map[string]bool{".Agents/Skills": true}, path) {
		t.Fatal("tracked ancestor with different capitalization was missed")
	}
	if trackedExactOrAncestor(map[string]bool{".Agents/Skill": true}, path) {
		t.Fatal("unrelated component prefix counted as an ancestor")
	}
}

func TestAIRejectsCaseAliasesBeforePublication(t *testing.T) {
	const target = ".codex/agents/dm-review.toml"
	for _, name := range []string{"DM-review.toml", "dm-review.TOML", "DM-review.TOML", ".DATAMITSU-OWNED-DM-review.TOML.marker"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command("git", "init", "-q")
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git init: %v: %s", err, out)
			}
			parent := filepath.Join(dir, ".codex", "agents")
			if err := os.MkdirAll(parent, 0o755); err != nil {
				t.Fatal(err)
			}
			existing := filepath.Join(parent, name)
			if err := os.WriteFile(existing, []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}
			desired := map[string]aiArtifact{target: {path: target, data: []byte("replacement"), files: nil}}
			changed, err := applyAI(t.Context(), dir, desired, func() (bool, error) {
				t.Error("publication started despite a case alias")
				return false, nil
			}, nil)
			if changed || err == nil || !strings.Contains(err.Error(), "case alias") && !strings.Contains(err.Error(), "case-aliased") {
				t.Fatalf("applyAI = %v, %v; want case alias error", changed, err)
			}
			if data, err := os.ReadFile(existing); err != nil || string(data) != "keep" {
				t.Fatalf("existing file = %q, %v", data, err)
			}
		})
	}
}

func TestAIRejectsCaseAliasedParentBeforePublication(t *testing.T) {
	const target = ".codex/agents/dm-review.toml"
	dir := newAIGitFixture(t)
	if err := os.MkdirAll(filepath.Join(dir, ".Codex", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	called := false
	changed, err := applyAI(t.Context(), dir, map[string]aiArtifact{target: {path: target, data: []byte("new"), files: nil}}, func() (bool, error) {
		called = true
		return false, nil
	}, nil)
	if changed || err == nil || called || !strings.Contains(err.Error(), "case-aliased parent") {
		t.Fatalf("applyAI = %v, %v, called=%v; want parent alias refusal", changed, err, called)
	}
}

func TestAIRejectsCaseAliasedDirectoryMarker(t *testing.T) {
	const target = ".agents/skills/dm-demo"
	dir := newAIGitFixture(t)
	marker := filepath.Join(dir, target, ".DATAMITSU-OWNED")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	called := false
	changed, err := applyAI(t.Context(), dir, map[string]aiArtifact{target: {path: target, files: map[string]aiFile{"SKILL.md": {data: []byte("new"), mode: 0o644}}, data: nil}}, func() (bool, error) {
		called = true
		return false, nil
	}, nil)
	if changed || err == nil || called || !strings.Contains(err.Error(), "existing case alias") {
		t.Fatalf("applyAI = %v, %v, called=%v; want marker alias refusal", changed, err, called)
	}
}

func TestAIPruneRequiresExactDirectoryMarker(t *testing.T) {
	const path = ".agents/skills/dm-demo"
	for _, marker := range []string{".DATAMITSU-OWNED", ".datamitsu-Owned"} {
		t.Run(marker, func(t *testing.T) {
			dir := newAIGitFixture(t)
			if err := os.MkdirAll(filepath.Join(dir, path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, path, marker), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			changed, err := applyAI(t.Context(), dir, nil, nil, nil)
			if changed || err != nil {
				t.Fatalf("applyAI = %v, %v; want preserve unowned tree", changed, err)
			}
			if _, err := os.Stat(filepath.Join(dir, path, marker)); err != nil {
				t.Fatalf("marker removed: %v", err)
			}
		})
	}
}

func TestAIPruneRevalidatesEditedProfile(t *testing.T) {
	const path = ".codex/agents/dm-review.toml"
	dir := newAIGitFixture(t)
	profile := filepath.Join(dir, path)
	marker := filepath.Join(dir, filepath.FromSlash(sidecar(path)))
	if err := os.MkdirAll(filepath.Dir(profile), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("generated")
	if err := os.WriteFile(profile, original, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(digest.XXH3Of(original).String()), 0o644); err != nil {
		t.Fatal(err)
	}
	var diagnostics []string
	changed, err := applyAI(t.Context(), dir, nil, func() (bool, error) {
		return false, os.WriteFile(profile, []byte("user edit"), 0o644)
	}, func(format string, args ...any) { diagnostics = append(diagnostics, fmt.Sprintf(format, args...)) })
	if changed || err != nil {
		t.Fatalf("applyAI = %v, %v; want preserve changed profile", changed, err)
	}
	if data, err := os.ReadFile(profile); err != nil || string(data) != "user edit" {
		t.Fatalf("profile = %q, %v", data, err)
	}
	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0], "changed after ownership check") {
		t.Fatalf("diagnostics = %v; want stale ownership diagnosis", diagnostics)
	}
}

func newAIGitFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return dir
}
