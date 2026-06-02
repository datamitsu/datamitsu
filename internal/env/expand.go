package env

import "strings"

// ExpandPlaceholders expands datamitsu-managed path placeholders in a value.
//
//	${STORE}   -> GetStorePath() (shared store, cleaned by `datamitsu store clear`)
//	${APP_DIR} -> appDir (the app's install dir)
func ExpandPlaceholders(value, appDir string) string {
	value = strings.ReplaceAll(value, "${STORE}", GetStorePath())
	value = strings.ReplaceAll(value, "${APP_DIR}", appDir)
	return value
}
