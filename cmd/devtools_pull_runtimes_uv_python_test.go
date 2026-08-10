package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func stubUVSupportedPythonVersions(t *testing.T, versions map[string]bool, err error) {
	t.Helper()
	orig := getUVSupportedPythonVersions
	getUVSupportedPythonVersions = func(context.Context, string) (map[string]bool, error) {
		return versions, err
	}
	t.Cleanup(func() { getUVSupportedPythonVersions = orig })
}

func TestReconcilePythonWithUV(t *testing.T) {
	// Regression: endoflife.date reported CPython 3.14.7 while the age gate held
	// the uv pin at 0.12.1, whose embedded table stops at 3.14.6. The generated
	// config pinned 3.14.7 and every managed Python tool failed to install with
	// "No interpreter found for Python 3.14.7".
	t.Run("downgrades a Python the pinned uv cannot install", func(t *testing.T) {
		stubUVSupportedPythonVersions(t, map[string]bool{"3.14.6": true, "3.14.5": true}, nil)

		got, err := reconcilePythonWithUV(context.Background(), "3.14.7", "0.12.1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "3.14.6" {
			t.Errorf("expected '3.14.6', got '%s'", got)
		}
	})

	t.Run("keeps a supported Python untouched", func(t *testing.T) {
		stubUVSupportedPythonVersions(t, map[string]bool{"3.14.7": true, "3.14.6": true}, nil)

		got, err := reconcilePythonWithUV(context.Background(), "3.14.7", "0.12.3")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "3.14.7" {
			t.Errorf("expected '3.14.7', got '%s'", got)
		}
	})

	// A brand-new minor line is not something to paper over: silently pinning
	// 3.14.x when 3.15.0 was requested would change the interpreter users get.
	t.Run("fails when uv does not know the minor line", func(t *testing.T) {
		stubUVSupportedPythonVersions(t, map[string]bool{"3.14.6": true}, nil)

		_, err := reconcilePythonWithUV(context.Background(), "3.15.0", "0.12.1")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "3.15.0") || !strings.Contains(err.Error(), "0.12.1") {
			t.Errorf("error should name both versions, got: %v", err)
		}
	})

	// Failing loud beats writing an unverified pin: without the metadata we
	// cannot tell whether the Python version is installable.
	t.Run("propagates lookup failures", func(t *testing.T) {
		stubUVSupportedPythonVersions(t, nil, errors.New("boom"))

		_, err := reconcilePythonWithUV(context.Background(), "3.14.7", "0.12.1")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "boom") {
			t.Errorf("expected wrapped cause, got: %v", err)
		}
	})
}
