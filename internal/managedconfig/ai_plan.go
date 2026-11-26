package managedconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/goccy/go-yaml"
	"github.com/pelletier/go-toml/v2"
)

type aiArtifact struct {
	path  string
	files map[string]aiFile
	data  []byte
}

type aiFile struct {
	data []byte
	mode fs.FileMode
}

var aiNamePattern = regexp.MustCompile(`^dm-[a-z0-9]+(-[a-z0-9]+)*$`)

func validAIName(name string) bool { return len(name) <= 64 && aiNamePattern.MatchString(name) }

var aiRoots = map[string]string{
	".agents/skills":   "",
	".claude/skills":   "",
	".agents/agents":   "",
	".claude/agents":   ".md",
	".codex/agents":    ".toml",
	".omp/agents":      ".md",
	".opencode/agents": ".md",
}

func planAI(cfg *config.AIConfig, bundleRoot func(string) (string, error)) (map[string]aiArtifact, error) {
	if cfg == nil {
		return map[string]aiArtifact{}, nil
	}
	for name, skill := range cfg.Skills {
		if !validAIName(name) {
			return nil, fmt.Errorf("invalid AI skill name %q: expected dm- prefix and lowercase kebab case", name)
		}
		for target := range skill.Targets {
			if !knownTarget(target, "agents", "claude") {
				return nil, fmt.Errorf("unknown AI skill target %q", target)
			}
		}
	}
	for name, agent := range cfg.Agents {
		if !validAIName(name) {
			return nil, fmt.Errorf("invalid AI agent name %q: expected dm- prefix and lowercase kebab case", name)
		}
		for target, override := range agent.Targets {
			if !knownTarget(target, "antigravity", "claude", "codex", "omp", "opencode") {
				return nil, fmt.Errorf("unknown AI agent target %q", target)
			}
			if override.Model != nil && !knownTarget(target, "claude", "codex", "opencode") {
				return nil, fmt.Errorf("model is not supported for AI agent target %q", target)
			}
		}
	}
	artifacts := map[string]aiArtifact{}
	if bundleRoot == nil && len(cfg.Skills) != 0 {
		return nil, errors.New("AI skills require a bundle resolver")
	}
	reserved := map[string]string{}
	reserve := func(path, owner string) error {
		path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
		if path == "." || strings.HasPrefix(path, "../") || strings.HasPrefix(path, "/") {
			return fmt.Errorf("invalid AI artifact path %q", path)
		}
		folded := strings.ToLower(path)
		for prior, priorOwner := range reserved {
			priorFolded := strings.ToLower(prior)
			if folded == priorFolded || strings.HasPrefix(folded, priorFolded+"/") || strings.HasPrefix(priorFolded, folded+"/") {
				return fmt.Errorf("AI artifact path collision between %q and %q", priorOwner, owner)
			}
		}
		reserved[path] = owner
		return nil
	}
	for _, name := range sortedAIKeys(cfg.Skills) {
		skill := cfg.Skills[name]
		if !aiEnabled(skill.Enabled) {
			continue
		}
		active := false
		for _, target := range skill.Targets {
			active = active || aiEnabled(target.Enabled)
		}
		if !active {
			continue
		}
		if skill.Source.Bundle == "" || skill.Source.Path == "" {
			return nil, fmt.Errorf("AI skill %q requires source bundle and path", name)
		}
		root, err := bundleRoot(skill.Source.Bundle)
		if err != nil {
			return nil, fmt.Errorf("AI skill %q bundle: %w", name, err)
		}
		files, err := readAISkill(root, skill.Source.Path)
		if err != nil {
			return nil, fmt.Errorf("AI skill %q: %w", name, err)
		}
		if _, ok := files["SKILL.md"]; !ok {
			return nil, fmt.Errorf("AI skill %q has no SKILL.md", name)
		}
		if err := verifyAISkillFrontmatter(files["SKILL.md"].data, name); err != nil {
			return nil, fmt.Errorf("AI skill %q: %w", name, err)
		}
		for _, target := range sortedAIKeys(skill.Targets) {
			override := skill.Targets[target]
			if !knownTarget(target, "agents", "claude") {
				return nil, fmt.Errorf("unknown AI skill target %q", target)
			}
			if !aiEnabled(override.Enabled) {
				continue
			}
			dir := filepath.ToSlash(filepath.Join(map[string]string{"agents": ".agents/skills", "claude": ".claude/skills"}[target], name))
			if err := reserve(dir, "skill "+name+" target "+target); err != nil {
				return nil, err
			}
			copied := cloneAIFiles(files)
			if override.Description != nil || override.DisableModelInvocation != nil {
				content, err := renderAISkillFrontmatter(copied["SKILL.md"].data, override)
				if err != nil {
					return nil, fmt.Errorf("AI skill %q target %s: %w", name, target, err)
				}
				copied["SKILL.md"] = aiFile{data: content, mode: copied["SKILL.md"].mode}
			}
			artifacts[dir] = aiArtifact{path: dir, files: copied, data: nil}
		}
	}
	for _, name := range sortedAIKeys(cfg.Agents) {
		agent := cfg.Agents[name]
		if len(name) > 64 || !aiNamePattern.MatchString(name) {
			return nil, fmt.Errorf("invalid AI agent name %q: expected dm- prefix and lowercase kebab case", name)
		}
		if !aiEnabled(agent.Enabled) {
			continue
		}
		for _, target := range sortedAIKeys(agent.Targets) {
			if !knownTarget(target, "antigravity", "claude", "codex", "omp", "opencode") {
				return nil, fmt.Errorf("unknown AI agent target %q", target)
			}
			override := agent.Targets[target]
			if !aiEnabled(override.Enabled) {
				continue
			}
			base, ok := agentTargetPath(target, name)
			if !ok {
				return nil, fmt.Errorf("unsupported AI agent target %q", target)
			}
			if target == "antigravity" {
				base = filepath.ToSlash(filepath.Dir(base))
			}
			if err := reserve(base, "agent "+name+" target "+target); err != nil {
				return nil, err
			}
			data, err := renderAIAgent(agent, name, target, override)
			if err != nil {
				return nil, fmt.Errorf("AI agent %q target %s: %w", name, target, err)
			}
			if target == "antigravity" {
				artifacts[base] = aiArtifact{path: base, data: nil, files: map[string]aiFile{"agent.md": {data: data, mode: 0o644}}}
			} else {
				artifacts[base] = aiArtifact{path: base, data: data, files: nil}
			}
		}
	}
	return artifacts, nil
}

func aiEnabled(enabled *bool) bool { return enabled == nil || *enabled }
func knownTarget(target string, choices ...string) bool {
	return slices.Contains(choices, target)
}

func sortedAIKeys[T any](items map[string]T) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func agentTargetPath(target, name string) (string, bool) {
	switch target {
	case "antigravity":
		return filepath.ToSlash(filepath.Join(".agents", "agents", name, "agent.md")), true
	case "claude":
		return filepath.ToSlash(filepath.Join(".claude", "agents", name+".md")), true
	case "codex":
		return filepath.ToSlash(filepath.Join(".codex", "agents", name+".toml")), true
	case "omp":
		return filepath.ToSlash(filepath.Join(".omp", "agents", name+".md")), true
	case "opencode":
		return filepath.ToSlash(filepath.Join(".opencode", "agents", name+".md")), true
	default:
		return "", false
	}
}

func readAISkill(root, rel string) (map[string]aiFile, error) {
	if err := validateAISkillPath(rel); err != nil {
		return nil, err
	}
	rootDir, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open skill bundle root: %w", err)
	}
	defer func() { _ = rootDir.Close() }()

	source := rel
	sourceInfo, err := rootDir.Stat(source)
	if err != nil {
		return nil, fmt.Errorf("stat skill source: %w", err)
	}
	if !sourceInfo.IsDir() {
		return nil, errors.New("skill source is not a directory")
	}
	files := map[string]aiFile{}
	resourcePaths := map[string]string{}
	reserveResource := func(name string) error {
		folded := strings.ToLower(filepath.ToSlash(name))
		for prior := range resourcePaths {
			if folded == prior || strings.HasPrefix(folded, prior+"/") || strings.HasPrefix(prior, folded+"/") {
				return fmt.Errorf("case-folded skill resource path collision: %q and %q", resourcePaths[prior], name)
			}
		}
		resourcePaths[folded] = name
		return nil
	}
	err = fs.WalkDir(rootDir.FS(), source, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk skill resource: %w", walkErr)
		}
		relPath, err := filepath.Rel(source, path)
		if err != nil {
			return fmt.Errorf("relative skill resource path: %w", err)
		}
		if relPath == "." {
			return nil
		}
		if err := reserveResource(filepath.ToSlash(relPath)); err != nil {
			return err
		}
		info, err := rootDir.Lstat(path)
		if err != nil {
			return fmt.Errorf("stat skill resource: %w", err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symbolic link is not allowed: %s", relPath)
		}
		if strings.EqualFold(filepath.Base(relPath), aiDirectoryMarker) {
			return fmt.Errorf("ownership marker is not allowed: %s", relPath)
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular file is not allowed: %s", relPath)
		}
		data, err := rootDir.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read skill resource: %w", err)
		}
		files[filepath.ToSlash(relPath)] = aiFile{data: data, mode: info.Mode().Perm()}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk skill source: %w", err)
	}
	if len(files) == 0 {
		return nil, errors.New("skill source is empty")
	}
	return files, nil
}

func validateAISkillPath(rel string) error {
	if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, `\`) || filepath.VolumeName(rel) != "" || len(rel) >= 2 && ((rel[0] >= 'A' && rel[0] <= 'Z') || (rel[0] >= 'a' && rel[0] <= 'z')) && rel[1] == ':' {
		return errors.New("source path must be a portable relative path")
	}
	for component := range strings.SplitSeq(rel, "/") {
		if component == "" || component == "." || component == ".." {
			return errors.New("source path escapes bundle")
		}
	}
	return nil
}

func verifyAISkillFrontmatter(data []byte, name string) error {
	body, _, ok := aiFrontmatter(data)
	if !ok {
		return errors.New("SKILL.md must have YAML frontmatter with a closing delimiter")
	}
	var fields map[string]any
	if err := yaml.Unmarshal(body, &fields); err != nil {
		return fmt.Errorf("invalid YAML frontmatter: %w", err)
	}
	if fields["name"] != name {
		return fmt.Errorf("frontmatter name must be %q", name)
	}
	if _, ok := fields["description"].(string); !ok {
		return errors.New("frontmatter description must be a string")
	}
	return nil
}

func aiFrontmatter(data []byte) ([]byte, []byte, bool) {
	s := string(data)
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return nil, nil, false
	}
	start := strings.Index(s, "\n") + 1
	for pos := start; pos < len(s); {
		end := strings.IndexByte(s[pos:], '\n')
		lineEnd := len(s)
		if end >= 0 {
			lineEnd = pos + end
		}
		line := strings.TrimSuffix(s[pos:lineEnd], "\r")
		if line == "---" {
			tailStart := lineEnd
			if tailStart < len(s) {
				tailStart++
			}
			return data[start:pos], data[tailStart:], true
		}
		if end < 0 {
			break
		}
		pos = lineEnd + 1
	}
	return nil, nil, false
}

func renderAISkillFrontmatter(data []byte, override config.AISkillTarget) ([]byte, error) {
	body, tail, ok := aiFrontmatter(data)
	if !ok {
		return nil, errors.New("SKILL.md must have YAML frontmatter with a closing delimiter")
	}
	var fields map[string]any
	if err := yaml.Unmarshal(body, &fields); err != nil {
		return nil, fmt.Errorf("parse skill frontmatter: %w", err)
	}
	if override.Description != nil {
		fields["description"] = *override.Description
	}
	if override.DisableModelInvocation != nil {
		fields["disable-model-invocation"] = *override.DisableModelInvocation
	}
	encoded, err := yaml.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encode skill frontmatter: %w", err)
	}
	out := append([]byte("---\n"), encoded...)
	out = append(out, []byte("---\n")...)
	return append(out, tail...), nil
}

func cloneAIFiles(files map[string]aiFile) map[string]aiFile {
	cloned := make(map[string]aiFile, len(files))
	for k, v := range files {
		v.data = append([]byte(nil), v.data...)
		cloned[k] = v
	}
	return cloned
}

func renderAIAgent(agent config.AIAgent, name, target string, override config.AIAgentTarget) ([]byte, error) {
	description, instructions := agent.Description, agent.Instructions
	if override.Description != nil {
		description = *override.Description
	}
	if override.Instructions != nil {
		instructions = *override.Instructions
	}
	if target == "codex" {
		type codexAgent struct {
			Name                  string  `toml:"name"`
			Description           string  `toml:"description"`
			DeveloperInstructions string  `toml:"developer_instructions"`
			Model                 *string `toml:"model,omitempty"`
		}
		var model *string
		if override.Model != nil {
			model = override.Model
		}
		data, err := toml.Marshal(codexAgent{Name: name, Description: description, DeveloperInstructions: instructions, Model: model})
		if err != nil {
			return nil, fmt.Errorf("encode Codex profile: %w", err)
		}
		return data, nil
	}
	frontmatter := map[string]any{"description": description}
	if target == "opencode" {
		frontmatter["mode"] = "subagent"
	} else {
		frontmatter["name"] = name
	}
	if override.Model != nil {
		frontmatter["model"] = *override.Model
	}
	header, err := yaml.Marshal(frontmatter)
	if err != nil {
		return nil, fmt.Errorf("encode agent frontmatter: %w", err)
	}
	out := append([]byte("---\n"), header...)
	out = append(out, []byte("---\n")...)
	out = append(out, instructions...)
	if !strings.HasSuffix(instructions, "\n") {
		out = append(out, '\n')
	}
	return out, nil
}
