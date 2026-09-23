package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

func ejectFixture() *Config {
	return &Config{
		Tools: MapOfTools{
			"gitleaks": {Name: "gitleaks", Operations: map[OperationType]ToolOperation{
				OpLint: {App: "gitleaks", Args: []string{"--config", "{managedConfig:.gitleaks.toml}", "{target}"}},
			}},
			"yamlfmt": {Name: "yamlfmt", Operations: map[OperationType]ToolOperation{
				OpFix:  {App: "yamlfmt", Args: []string{"-conf={managedConfig:.yamlfmt.yaml}", "{files}"}},
				OpLint: {App: "yamlfmt", Args: []string{"-lint", "{files}"}, Env: map[string]string{"YAMLFMT_CONF": "{managedConfig:.yamlfmt.yaml}"}},
			}},
			"eslint": {Name: "eslint", Operations: map[OperationType]ToolOperation{
				OpLint: {App: "eslint", Args: []string{"-c", "{managedConfig:eslint.config.mjs}", "{files}"}},
			}},
		},
		ManagedConfigs: MapOfManagedConfigs{
			".gitleaks.toml":    {Tools: []string{"gitleaks"}, Scope: ScopeGitRoot, Ejectable: true, Content: "fn"},
			".yamlfmt.yaml":     {Tools: []string{"yamlfmt"}, Scope: ScopeGitRoot, Ejectable: true, Content: "fn"},
			"eslint.config.mjs": {Tools: []string{"eslint"}},
		},
	}
}

func TestValidateEject(t *testing.T) {
	t.Run("a valid declaration passes", func(t *testing.T) {
		cfg := ejectFixture()
		cfg.EjectConfigs = []string{"gitleaks"}
		if err := ValidateEject(cfg); err != nil {
			t.Fatalf("ValidateEject() = %v", err)
		}
	})

	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"unknown tool", func(c *Config) { c.EjectConfigs = []string{"no-such-tool"} }, `"no-such-tool" is not a configured tool`},
		{"tool without an ejectable config", func(c *Config) { c.EjectConfigs = []string{"eslint"} }, `tool "eslint" has no ejectable managed config`},
		{"duplicate", func(c *Config) { c.EjectConfigs = []string{"gitleaks", "gitleaks"} }, "listed more than once"},
		{"empty name", func(c *Config) { c.EjectConfigs = []string{""} }, "must not be empty"},
		{"project scope", func(c *Config) {
			mc := c.ManagedConfigs[".gitleaks.toml"]
			mc.Scope = ScopeProject
			c.ManagedConfigs[".gitleaks.toml"] = mc
		}, `requires scope "git-root"`},
		{"no tools", func(c *Config) {
			mc := c.ManagedConfigs[".gitleaks.toml"]
			mc.Tools = nil
			c.ManagedConfigs[".gitleaks.toml"] = mc
		}, "requires tools"},
		{"linkTarget", func(c *Config) {
			mc := c.ManagedConfigs[".gitleaks.toml"]
			mc.LinkTarget = "AGENTS.md"
			c.ManagedConfigs[".gitleaks.toml"] = mc
		}, "cannot be combined with linkTarget"},
		{"deleteOnly", func(c *Config) {
			mc := c.ManagedConfigs[".gitleaks.toml"]
			mc.DeleteOnly = true
			c.ManagedConfigs[".gitleaks.toml"] = mc
		}, "cannot be combined with deleteOnly"},
		{"key inside .datamitsu", func(c *Config) {
			c.ManagedConfigs[".datamitsu/x.toml"] = ManagedConfig{Tools: []string{"gitleaks"}, Scope: ScopeGitRoot, Ejectable: true}
		}, "clean relative path"},
		{"keys differing only in case", func(c *Config) {
			c.ManagedConfigs[".GITLEAKS.toml"] = ManagedConfig{Tools: []string{"gitleaks"}, Scope: ScopeGitRoot, Ejectable: true}
		}, "differ only in case"},
		{"a key that is a directory of another", func(c *Config) {
			c.ManagedConfigs[".gitleaks.toml/nested.toml"] = ManagedConfig{Tools: []string{"gitleaks"}, Scope: ScopeGitRoot, Ejectable: true}
		}, "is a directory of the other"},
		{"key escaping the root", func(c *Config) {
			c.ManagedConfigs["../x.toml"] = ManagedConfig{Tools: []string{"gitleaks"}, Scope: ScopeGitRoot, Ejectable: true}
		}, "clean relative path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ejectFixture()
			tt.mutate(cfg)
			err := ValidateEject(cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateEject() = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestApplyManagedConfigPlacements(t *testing.T) {
	cfg := ejectFixture()
	cfg.ManagedConfigs["shared.toml"] = ManagedConfig{Tools: []string{"yamlfmt", "gitleaks"}, Scope: ScopeGitRoot, Ejectable: true}
	cfg.EjectConfigs = []string{"gitleaks"}
	ApplyManagedConfigPlacements(cfg)

	want := map[string]ManagedConfigPlacement{
		".gitleaks.toml":    PlacementRepo,
		".yamlfmt.yaml":     PlacementInternal,
		"eslint.config.mjs": PlacementRepo,
		"shared.toml":       PlacementRepo,
	}
	for key, placement := range want {
		if got := cfg.ManagedConfigs[key].Placement; got != placement {
			t.Errorf("%s placement = %q, want %q", key, got, placement)
		}
	}
}

func TestResolveManagedConfigPlaceholders(t *testing.T) {
	cfg := ejectFixture()
	cfg.EjectConfigs = []string{"gitleaks"}
	ApplyManagedConfigPlacements(cfg)
	if err := ResolveManagedConfigPlaceholders(cfg); err != nil {
		t.Fatalf("ResolveManagedConfigPlaceholders() = %v", err)
	}

	gitleaks := cfg.Tools["gitleaks"].Operations[OpLint]
	if !slices.Equal(gitleaks.Args, []string{"--config", "{root}/.gitleaks.toml", "{target}"}) {
		t.Errorf("ejected args = %v", gitleaks.Args)
	}
	if len(gitleaks.ManagedConfigRefs) != 1 || gitleaks.ManagedConfigRefs[0] != (ManagedConfigOpRef{Key: ".gitleaks.toml", Path: "{root}/.gitleaks.toml"}) {
		t.Errorf("ejected refs = %v", gitleaks.ManagedConfigRefs)
	}

	fix := cfg.Tools["yamlfmt"].Operations[OpFix]
	if fix.Args[0] != "-conf={root}/.datamitsu/configs/.yamlfmt.yaml" {
		t.Errorf("internal args = %v", fix.Args)
	}
	lint := cfg.Tools["yamlfmt"].Operations[OpLint]
	if lint.Env["YAMLFMT_CONF"] != "{root}/.datamitsu/configs/.yamlfmt.yaml" {
		t.Errorf("internal env = %v", lint.Env)
	}
	if len(lint.ManagedConfigRefs) != 1 || lint.ManagedConfigRefs[0].Key != ".yamlfmt.yaml" {
		t.Errorf("env refs = %v", lint.ManagedConfigRefs)
	}

	eslint := cfg.Tools["eslint"].Operations[OpLint]
	if eslint.Args[1] != "{cwd}/eslint.config.mjs" {
		t.Errorf("project-scoped args = %v", eslint.Args)
	}
}

func TestResolveManagedConfigPlaceholdersRejects(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want string
	}{
		{"unknown key", "{managedConfig:.missing.toml}", "names no managed config"},
		{"a config of another tool", "{managedConfig:.yamlfmt.yaml}", `add "gitleaks" to its tools`},
		{"malformed", "{managedConfig:.gitleaks.toml", "malformed"},
		{"empty key", "{managedConfig:}", "malformed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ejectFixture()
			op := cfg.Tools["gitleaks"].Operations[OpLint]
			op.Args = []string{"--config", tt.arg}
			cfg.Tools["gitleaks"].Operations[OpLint] = op
			ApplyManagedConfigPlacements(cfg)
			err := ResolveManagedConfigPlaceholders(cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ResolveManagedConfigPlaceholders() = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestRenderManagedConfigFromScratch(t *testing.T) {
	root := t.TempDir()
	baseVM := goja.New()
	projectVM := goja.New()

	fn := func(vm *goja.Runtime, src string) goja.Value {
		v, err := vm.RunString(src)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	base := ManagedConfigLayer{Name: "base", VM: baseVM, Configs: MapOfManagedConfigs{
		".gitleaks.toml": {Content: fn(baseVM, `(ctx) => [ctx.placement, ctx.originalContent ?? "none", ctx.datamitsuDir, ctx.datamitsuDirFromOutput, ctx.outputPath].join("|")`)},
	}}
	project := ManagedConfigLayer{Name: "project", VM: projectVM, Configs: MapOfManagedConfigs{
		".gitleaks.toml": {Content: fn(projectVM, `(ctx) => ctx.existingContent + "+project"`)},
	}}

	got, err := RenderManagedConfigFromScratch([]ManagedConfigLayer{base, project}, ".gitleaks.toml", root, PlacementInternal)
	if err != nil {
		t.Fatalf("RenderManagedConfigFromScratch() = %v", err)
	}
	wantPath := filepath.Join(root, ".datamitsu", "configs", ".gitleaks.toml")
	want := "internal|none|.datamitsu|..|" + wantPath + "+project"
	if got == nil || *got != want {
		t.Fatalf("internal render = %v, want %q", got, want)
	}

	got, err = RenderManagedConfigFromScratch([]ManagedConfigLayer{base}, ".gitleaks.toml", root, PlacementRepo)
	if err != nil {
		t.Fatal(err)
	}
	if want := "repo|none|.datamitsu|.datamitsu|" + filepath.Join(root, ".gitleaks.toml"); got == nil || *got != want {
		t.Fatalf("repo render = %v, want %q", got, want)
	}

	t.Run("a throwing content is an error", func(t *testing.T) {
		vm := goja.New()
		layer := ManagedConfigLayer{Name: "broken", VM: vm, Configs: MapOfManagedConfigs{
			"x.toml": {Content: fn(vm, `() => { throw new Error("boom") }`)},
		}}
		if _, err := RenderManagedConfigFromScratch([]ManagedConfigLayer{layer}, "x.toml", root, PlacementInternal); err == nil || !strings.Contains(err.Error(), "broken") {
			t.Fatalf("err = %v, want one naming the layer", err)
		}
	})

	t.Run("undefined from every layer renders nothing", func(t *testing.T) {
		vm := goja.New()
		layer := ManagedConfigLayer{Name: "undefined-only", VM: vm, Configs: MapOfManagedConfigs{
			"x.toml": {Content: fn(vm, `() => undefined`)},
		}}
		got, err := RenderManagedConfigFromScratch([]ManagedConfigLayer{layer}, "x.toml", root, PlacementInternal)
		if err != nil || got != nil {
			t.Fatalf("got %v, %v; want nil, nil", got, err)
		}
	})
}
