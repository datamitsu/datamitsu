package dockerfile

import (
	"testing"

	"github.com/datamitsu/datamitsu/internal/config"
)

func TestStoreSubtrees(t *testing.T) {
	if got := binaryAppSubtree("shellcheck"); got != ".bin/shellcheck" {
		t.Errorf("binaryAppSubtree = %q", got)
	}
	if got := runtimeSubtree("node"); got != ".runtimes/node" {
		t.Errorf("runtimeSubtree = %q", got)
	}
	if got := appEnvSubtree(config.RuntimeKindNode, "prettier"); got != ".apps/node/prettier" {
		t.Errorf("appEnvSubtree(node) = %q", got)
	}
	if got := appEnvSubtree(config.RuntimeKindUV, "ruff"); got != ".apps/uv/ruff" {
		t.Errorf("appEnvSubtree(uv) = %q", got)
	}
	if uvPythonSubtree != ".uv/python" {
		t.Errorf("uvPythonSubtree = %q", uvPythonSubtree)
	}
}

func TestRuntimeCopiedToFinal(t *testing.T) {
	cases := map[config.RuntimeKind]bool{
		config.RuntimeKindNode: true,
		config.RuntimeKindUV:   true,
		config.RuntimeKindJVM:  true,
		config.RuntimeKindGo:   false, // SDK is build-only; the binary is self-contained
	}
	for kind, want := range cases {
		if got := runtimeCopiedToFinal(kind); got != want {
			t.Errorf("runtimeCopiedToFinal(%s) = %v, want %v", kind, got, want)
		}
	}
}
