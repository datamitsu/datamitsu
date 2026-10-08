package env

import "testing"

func TestCredentialReferences(t *testing.T) {
	t.Setenv("MY_FORGE_TOKEN", "value")
	got, err := Credential("MY_FORGE_TOKEN")
	if err != nil || got != "value" {
		t.Fatalf("credential=%q err=%v", got, err)
	}
	for _, name := range []string{"DATAMITSU_SECRET", "BAD NAME", ""} {
		if _, err := Credential(name); err == nil {
			t.Fatal("invalid credential name accepted")
		}
	}
}
