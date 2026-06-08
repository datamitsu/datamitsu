package dockerfile

import (
	"path"

	"github.com/datamitsu/datamitsu/internal/config"
)

// Store subtrees are expressed relative to the store root (env.GetStorePath()),
// which the generated Dockerfile fixes at "$DATAMITSU_CACHE_DIR/store". We copy
// the hash-less PARENT directory (the per-config-hash child is its only entry),
// so the generator never has to compute the host/arch/libc-dependent config hash
// — the hash is computed correctly inside each builder stage for its target
// platform. This is what keeps `docker buildx --platform` cross-arch builds
// correct (Strategy A in the design doc).

// uvPythonSubtree is the shared managed-CPython directory. A uv app's venv
// interpreter symlinks into here (UV_PYTHON_INSTALL_DIR), so it must be copied
// into the final image alongside the app, or the symlink dangles.
const uvPythonSubtree = ".uv/python"

// binaryAppSubtree is the store subtree for a downloaded-binary app ({store}/.bin/<app>).
func binaryAppSubtree(app string) string {
	return path.Join(".bin", app)
}

// runtimeSubtree is the store subtree for a managed runtime ({store}/.runtimes/<name>).
func runtimeSubtree(name string) string {
	return path.Join(".runtimes", name)
}

// appEnvSubtree is the store subtree for a runtime-managed app
// ({store}/.apps/<kind>/<app>).
func appEnvSubtree(kind config.RuntimeKind, app string) string {
	return path.Join(".apps", string(kind), app)
}

// runtimeCopiedToFinal reports whether a runtime's own store subtree must be
// present in the final image. Go builds a self-contained binary, so the Go SDK
// is build-only; every other kind needs its interpreter/runtime at run time.
func runtimeCopiedToFinal(kind config.RuntimeKind) bool {
	return kind != config.RuntimeKindGo
}
