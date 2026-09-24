//go:build windows

package lsp

import (
	"errors"

	"golang.org/x/sys/windows"
)

func transientRenameError(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
