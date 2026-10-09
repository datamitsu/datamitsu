package env

import (
	"errors"
	"os"
	"regexp"
	"strings"
)

var credentialName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Credential reads an explicitly named third-party token without permitting datamitsu settings.
func Credential(name string) (string, error) {
	if !credentialName.MatchString(name) || strings.HasPrefix(name, "DATAMITSU_") {
		return "", errors.New("invalid third-party credential environment name")
	}
	return os.Getenv(name), nil
}
