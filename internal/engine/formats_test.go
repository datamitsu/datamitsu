package engine

import (
	"strings"
	"testing"
)

func TestTOMLStringifyOrder(t *testing.T) {
	e, err := New("")
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}

	// Test case from the issue: primitives should come before tables
	script := `
		const data = {};
		TOML.stringify({
			...data,
			extend: {
				useDefault: true,
			},
			title: "Custom Gitleaks configuration",
		})
	`

	result, err := e.vm.RunString(script)
	if err != nil {
		t.Fatalf("Failed to run script: %v", err)
	}

	output := result.String()
	lines := strings.Split(output, "\n")

	// Find the positions of 'title' and '[extend]'
	titleIdx := -1
	extendIdx := -1

	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "title") {
			titleIdx = i
		}
		if strings.TrimSpace(line) == "[extend]" {
			extendIdx = i
		}
	}

	// Verify both are present
	if titleIdx == -1 {
		t.Error("'title' field not found in output")
	}
	if extendIdx == -1 {
		t.Error("[extend] section not found in output")
	}

	// Verify title comes before [extend]
	if titleIdx >= extendIdx {
		t.Errorf("Expected 'title' (line %d) to come before '[extend]' (line %d)\nOutput:\n%s", titleIdx, extendIdx, output)
	}

	t.Logf("TOML output:\n%s", output)
}

func TestTOMLStringifyMultiplePrimitives(t *testing.T) {
	e, err := New("")
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}

	script := `
		TOML.stringify({
			name: "test",
			version: "1.0",
			nested: {
				key: "value"
			},
			author: "someone"
		})
	`

	result, err := e.vm.RunString(script)
	if err != nil {
		t.Fatalf("Failed to run script: %v", err)
	}

	output := result.String()
	lines := strings.Split(output, "\n")

	// Find positions
	nameIdx := -1
	versionIdx := -1
	authorIdx := -1
	nestedIdx := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "name") {
			nameIdx = i
		}
		if strings.HasPrefix(trimmed, "version") {
			versionIdx = i
		}
		if strings.HasPrefix(trimmed, "author") {
			authorIdx = i
		}
		if trimmed == "[nested]" {
			nestedIdx = i
		}
	}

	// All primitives should come before the table
	if nameIdx > nestedIdx || versionIdx > nestedIdx || authorIdx > nestedIdx {
		t.Errorf("Expected all primitives to come before [nested] table\nOutput:\n%s", output)
	}

	t.Logf("TOML output:\n%s", output)
}
