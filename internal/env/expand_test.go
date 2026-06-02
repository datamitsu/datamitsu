package env

import "testing"

func TestExpandPlaceholders(t *testing.T) {
	store := GetStorePath()
	appDir := "/tmp/app-dir"

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"store token", "${STORE}/.playwright/browsers", store + "/.playwright/browsers"},
		{"app dir token", "${APP_DIR}/bin", appDir + "/bin"},
		{"both tokens", "${STORE}:${APP_DIR}", store + ":" + appDir},
		{"repeated tokens", "${STORE}-${STORE}", store + "-" + store},
		{"no placeholder", "/plain/path", "/plain/path"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExpandPlaceholders(tt.value, appDir)
			if got != tt.want {
				t.Errorf("ExpandPlaceholders(%q, %q) = %q, want %q", tt.value, appDir, got, tt.want)
			}
		})
	}
}
