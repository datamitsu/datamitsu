package storelock

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreTargetLockAliasAndCanonicalSerialize(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	physical := filepath.Join(root, "physical")
	if err := os.MkdirAll(physical, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(physical, filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	aliasTarget := filepath.Join("alias", "missing", "target")
	resolved, err := ValidateTarget(root, aliasTarget)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		t.Fatal(err)
	}
	canonicalTarget := filepath.Join("physical", "missing", "target")
	first, err := Lock(context.Background(), root, aliasTarget)
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := Lock(ctx, root, canonicalTarget); err == nil {
		t.Fatal("canonical target acquired while alias target lock was held")
	}
}

func TestStoreLeaseProcessHelper(t *testing.T) {
	mode := os.Getenv("DATAMITSU_STORELOCK_HELPER")
	if mode == "" {
		return
	}
	root := os.Getenv("DATAMITSU_STORELOCK_ROOT")
	input := bufio.NewScanner(os.Stdin)
	switch mode {
	case "owner":
		_, release, err := WithStore(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("HELD")
		if !input.Scan() || input.Text() != "RELEASE" {
			t.Fatal("missing release signal")
		}
		release()
		fmt.Println("RELEASED")
	case "entrant":
		fmt.Println("TRYING")
		_, release, err := WithStore(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("ENTERED")
		if !input.Scan() || input.Text() != "RELEASE" {
			t.Fatal("missing release signal")
		}
		release()
		fmt.Println("RELEASED")
	case "clear":
		gateAcquiredHook = func() { fmt.Println("GATE") }
		release, err := Clear(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("CLEARED")
		if !input.Scan() || input.Text() != "RELEASE" {
			t.Fatal("missing release signal")
		}
		release()
		fmt.Println("RELEASED")
	case "cancel-clear":
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			if input.Scan() && input.Text() == "CANCEL" {
				cancel()
			}
		}()
		gateAcquiredHook = func() { fmt.Println("GATE") }
		_, err := Clear(ctx, root)
		if err == nil {
			t.Fatal("cancelled clear unexpectedly acquired lease")
		}
		fmt.Println("CANCELED")
	case "probe-clear":
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		gateAcquiredHook = func() { fmt.Println("GATE") }
		release, err := Clear(ctx, root)
		if err == nil {
			release()
			fmt.Println("ACQUIRED")
			return
		}
		if !strings.Contains(err.Error(), "deadline exceeded") {
			t.Fatal(err)
		}
		fmt.Println("BLOCKED")
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

type leaseProcess struct {
	cmd   *exec.Cmd
	lines chan string
	stdin interface {
		Write(p []byte) (n int, err error)
	}
}

func startLeaseProcess(t *testing.T, mode, root string) *leaseProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestStoreLeaseProcessHelper$")
	cmd.Env = append(os.Environ(), "DATAMITSU_STORELOCK_HELPER="+mode, "DATAMITSU_STORELOCK_ROOT="+root)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 8)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	p := &leaseProcess{cmd: cmd, lines: lines, stdin: stdin}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return p
}

func (p *leaseProcess) line(t *testing.T, want string) {
	t.Helper()
	select {
	case got, ok := <-p.lines:
		if !ok || got != want {
			t.Fatalf("helper line = %q (open %v), want %q", got, ok, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for helper line %q", want)
	}
}

func (p *leaseProcess) send(t *testing.T, line string) {
	t.Helper()
	if _, writeErr := p.stdin.Write([]byte(line + "\n")); writeErr != nil {
		t.Fatal(writeErr)
	}
}

func TestStoreLeaseFairGateAcrossProcesses(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", filepath.Join(base, "cachebase"))
	root := filepath.Join(base, "store")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	owner := startLeaseProcess(t, "owner", root)
	owner.line(t, "HELD")
	clearProcess := startLeaseProcess(t, "clear", root)
	clearProcess.line(t, "GATE")
	entrant := startLeaseProcess(t, "entrant", root)
	entrant.line(t, "TRYING")
	owner.send(t, "RELEASE")
	owner.line(t, "RELEASED")
	clearProcess.line(t, "CLEARED")
	clearProcess.send(t, "RELEASE")
	clearProcess.line(t, "RELEASED")
	entrant.line(t, "ENTERED")
	entrant.send(t, "RELEASE")
	entrant.line(t, "RELEASED")
}

func TestCanceledCrossProcessClearReleasesGateOnly(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", filepath.Join(base, "cachebase"))
	root := filepath.Join(base, "store")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	owner := startLeaseProcess(t, "owner", root)
	owner.line(t, "HELD")
	waiter := startLeaseProcess(t, "cancel-clear", root)
	waiter.line(t, "GATE")
	waiter.send(t, "CANCEL")
	waiter.line(t, "CANCELED")
	probe := startLeaseProcess(t, "probe-clear", root)
	probe.line(t, "GATE")
	probe.line(t, "BLOCKED")
	owner.send(t, "RELEASE")
	owner.line(t, "RELEASED")
}

func TestWithStoreNestedAndClearWaits(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", filepath.Join(base, "base"))
	store := filepath.Join(base, "store")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, release, err := WithStore(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	nested, nestedRelease, err := WithStore(ctx, filepath.Join(base, "unused", "..", "store"))
	if err != nil {
		t.Fatal(err)
	}
	if nested.Value(leaseKey{}) != ctx.Value(leaseKey{}) {
		t.Fatal("nested acquisition did not reuse lease context")
	}
	nestedRelease()

	clearCtx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()
	if _, err := Clear(clearCtx, store); err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("Clear while shared lease held = %v, want deadline exceeded", err)
	}
	release()
	releaseClear, err := Clear(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	releaseClear()
}

func TestWithStoreAllowsNestedAbsentRoot(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", filepath.Join(base, "cachebase"))
	root := filepath.Join(base, "not-created", "store")
	ctx, release, err := WithStore(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := Lock(ctx, root, filepath.Join(".apps", "uv", "hash")); err != nil {
		t.Fatalf("Lock with absent store root: %v", err)
	}
}

func TestWithStoreRejectsInstallerRecursion(t *testing.T) {
	t.Setenv("DATAMITSU_INSTALL_CHILD", "1")
	if _, _, err := WithStore(context.Background(), filepath.Join(t.TempDir(), "store")); err == nil || !strings.Contains(err.Error(), "not allowed inside an installer") {
		t.Fatalf("WithStore recursive acquisition = %v", err)
	}
	if _, err := Clear(context.Background(), filepath.Join(t.TempDir(), "store")); err == nil || !strings.Contains(err.Error(), "not allowed inside an installer") {
		t.Fatalf("Clear recursive acquisition = %v", err)
	}
}

func TestTargetIdentityStableWhenMissingSuffixAppears(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	physical := filepath.Join(root, "physical")
	if err := os.MkdirAll(physical, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(physical, filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	target := filepath.Join("alias", "new", "leaf")
	before, err := TargetIdentity(root, target)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ValidateTarget(root, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		t.Fatal(err)
	}
	after, err := TargetIdentity(root, target)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := TargetIdentity(root, filepath.Join("physical", "new", "leaf"))
	if err != nil {
		t.Fatal(err)
	}
	if before != after || before != canonical {
		t.Fatalf("identity changed across creation/alias: before=%s after=%s canonical=%s", before, after, canonical)
	}
}

func TestValidateTargetRejectsTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	if _, err := ValidateTarget(root, "../outside"); err == nil {
		t.Fatal("traversal target accepted")
	}
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := ValidateTarget(root, filepath.Join("escape", "target")); err == nil {
		t.Fatal("symlink escape accepted")
	}
}

func TestStoreLockCanceledWaiterDoesNotBlockLaterOwner(t *testing.T) {
	base := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", filepath.Join(base, "cache"))
	store := filepath.Join(base, "store")
	ownerRelease, err := Lock(context.Background(), store, ".apps/node/identity")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := Lock(ctx, store, ".apps/node/identity"); err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("canceled lock wait = %v, want context deadline", err)
	}
	ownerRelease()
	release, err := Lock(context.Background(), store, ".apps/node/identity")
	if err != nil {
		t.Fatalf("lock after canceled waiter = %v", err)
	}
	release()
}

func TestValidateTargetRejectsAbsoluteOutsideAndRootTargets(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"..", filepath.Join("..", "outside"), root, "."} {
		if _, err := ValidateTarget(root, target); err == nil {
			t.Errorf("ValidateTarget(%q) unexpectedly succeeded", target)
		}
	}
}
