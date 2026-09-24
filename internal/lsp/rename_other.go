//go:build !windows

package lsp

func transientRenameError(error) bool { return false }
