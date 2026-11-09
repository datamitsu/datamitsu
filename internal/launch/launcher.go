// Package launch preserves runtime arguments and environment in native executable bindings.
package launch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/ldflags"
)

const (
	launcherName = "datamitsu-app-launcher"
	configName   = "command.json"
)

type command struct {
	Executable string            `json:"executable"`
	Args       []string          `json:"args,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Identity   string            `json:"identity"`
}

type executableSource struct {
	identity string
	path     string
	file     *os.File
	info     os.FileInfo
}

// Path returns the content-addressed launcher path without creating it.
func Path(executable string, args []string, envVars map[string]string) (string, error) {
	path, _, _, err := commandLocation(executable, args, envVars)
	return path, err
}

func commandLocation(executable string, args []string, envVars map[string]string) (string, []byte, *executableSource, error) {
	if !filepath.IsAbs(executable) {
		return "", nil, nil, fmt.Errorf("app launcher executable must be absolute: %q", executable)
	}
	source, err := buildIdentity()
	if err != nil {
		return "", nil, nil, err
	}
	data, err := json.Marshal(command{Executable: executable, Args: args, Env: envVars, Identity: source.identity})
	if err != nil {
		return "", nil, nil, fmt.Errorf("encode app launcher command: %w", err)
	}
	storeRoot, err := filepath.Abs(env.GetStorePath())
	if err != nil {
		return "", nil, nil, fmt.Errorf("resolve app launcher store path: %w", err)
	}
	return filepath.Join(storeRoot, ".app-launchers", commandKey(data), launcherExecutableName()), data, source, nil
}

func launcherExecutableName() string {
	if runtime.GOOS == "windows" {
		return launcherName + ".exe"
	}
	return launcherName
}

func commandKey(data []byte) string {
	return digest.XXH3Multi([]byte(ldflags.Version), data).Hex()
}

// The open descriptor pins the hashed content for this process, even after a rebuild.
var buildIdentity = sync.OnceValues(func() (*executableSource, error) {
	path, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate datamitsu executable for app launcher: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open datamitsu executable for app launcher: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect datamitsu executable for app launcher: %w", err)
	}
	identity, err := digest.XXH3Reader(file)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("hash datamitsu executable for app launcher: %w", err)
	}
	return &executableSource{identity: identity.Hex(), path: path, file: file, info: info}, nil
})

// Ensure stages the immutable command record before exposing a copy of this executable.
func Ensure(executable string, args []string, envVars map[string]string) (string, error) {
	path, data, source, err := commandLocation(executable, args, envVars)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create app launcher directory: %w", err)
	}
	configPath := filepath.Join(dir, configName)
	if existing, err := os.ReadFile(configPath); err == nil {
		if string(existing) != string(data) {
			return "", errors.New("app launcher command record does not match its content-addressed path")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read app launcher command: %w", err)
	} else {
		tmp, err := os.CreateTemp(dir, ".command-*")
		if err != nil {
			return "", fmt.Errorf("stage app launcher command: %w", err)
		}
		tmpPath := tmp.Name()
		defer func() { _ = os.Remove(tmpPath) }()
		if _, err := tmp.Write(data); err != nil {
			_ = tmp.Close()
			return "", fmt.Errorf("write app launcher command: %w", err)
		}
		if err := tmp.Chmod(0o640); err != nil {
			_ = tmp.Close()
			return "", fmt.Errorf("set shared launcher command permissions: %w", err)
		}
		if err := tmp.Close(); err != nil {
			return "", fmt.Errorf("close staged app launcher command: %w", err)
		}
		if err := os.Rename(tmpPath, configPath); err != nil {
			if existing, readErr := os.ReadFile(configPath); readErr != nil || string(existing) != string(data) {
				return "", fmt.Errorf("install app launcher command: %w", err)
			}
		}
	}
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect app launcher %q: %w", path, err)
	}
	if err := publishExecutable(source, path); err != nil {
		return "", err
	}
	return path, nil
}

func publishExecutable(source *executableSource, target string) error {
	stage, err := os.CreateTemp(filepath.Dir(target), ".launcher-link-*")
	if err != nil {
		return fmt.Errorf("stage launcher executable link: %w", err)
	}
	stagePath := stage.Name()
	defer func() { _ = os.Remove(stagePath) }()
	if err := stage.Close(); err != nil {
		return fmt.Errorf("close staged launcher link: %w", err)
	}
	if err := os.Remove(stagePath); err != nil {
		return fmt.Errorf("reserve staged launcher link: %w", err)
	}
	if err := os.Link(source.path, stagePath); err == nil {
		linked, statErr := os.Lstat(stagePath)
		if statErr == nil && linked.Mode().IsRegular() && os.SameFile(source.info, linked) {
			if err := os.Rename(stagePath, target); err != nil {
				if _, statErr := os.Stat(target); statErr == nil {
					return nil
				}
				return fmt.Errorf("install launcher executable link: %w", err)
			}
			return nil
		}
	}
	return copyExecutable(source, target)
}

func copyExecutable(source *executableSource, target string) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".launcher-*")
	if err != nil {
		return fmt.Errorf("create staged launcher executable in %q: %w", filepath.Dir(target), err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := io.Copy(tmp, io.NewSectionReader(source.file, 0, source.info.Size())); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("copy captured executable to staged launcher: %w", err)
	}
	if err := tmp.Chmod(source.info.Mode()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set staged launcher executable permissions: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close staged launcher executable: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		if _, statErr := os.Stat(target); statErr == nil {
			return nil
		}
		return fmt.Errorf("install launcher executable %q: %w", target, err)
	}
	return nil
}

// Dispatch replaces this launcher with its recorded command; ordinary CLI invocations are untouched.
func Dispatch() (int, bool) {
	return DispatchWith(os.Args, os.Environ(), os.Args[0], execAppCommand)
}

// DispatchWith allows launcher identity and argument handling to be exercised without replacing the test process.
func DispatchWith(args []string, environ []string, executable string, run func(string, []string, []string) int) (int, bool) {
	base := filepath.Base(executable)
	if base != launcherName && base != launcherName+".exe" {
		return 0, false
	}
	if len(args) == 0 || filepath.Clean(executable) != executable {
		return 126, true
	}
	root := filepath.Dir(executable)
	storeRoot, err := filepath.Abs(env.GetStorePath())
	launcherRoot, absErr := filepath.Abs(filepath.Dir(root))
	key := filepath.Base(root)
	if err != nil || absErr != nil || filepath.Clean(root) != root || launcherRoot != filepath.Join(storeRoot, ".app-launchers") || !validLauncherKey(key) {
		return 126, true
	}
	records, err := os.OpenRoot(launcherRoot)
	if err != nil {
		return 126, true
	}
	data, readErr := records.ReadFile(filepath.Join(key, configName))
	closeErr := records.Close()
	if readErr != nil || closeErr != nil {
		return 126, true
	}
	var record command
	if err := json.Unmarshal(data, &record); err != nil || !filepath.IsAbs(record.Executable) || !validLauncherKey(record.Identity) {
		return 126, true
	}
	canonical, err := json.Marshal(record)
	if err != nil || key != commandKey(canonical) {
		return 126, true
	}
	argv := make([]string, 1, 1+len(record.Args)+len(args)-1)
	argv[0] = record.Executable
	argv = append(argv, record.Args...)
	argv = append(argv, args[1:]...)
	return run(record.Executable, argv, mergeEnvironment(environ, record.Env)), true
}

func validLauncherKey(key string) bool {
	if len(key) != 32 {
		return false
	}
	for _, char := range key {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func mergeEnvironment(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	values := make(map[string]string, len(base)+len(overrides))
	normalize := func(key string) string {
		if runtime.GOOS == "windows" {
			return strings.ToUpper(key)
		}
		return key
	}
	for _, entry := range base {
		if key, value, ok := strings.Cut(entry, "="); ok && key != "" {
			values[normalize(key)] = value
		}
	}
	for key, value := range overrides {
		key = normalize(key)
		if key == "PATH" && values[key] != "" && values[key] != value && !strings.HasPrefix(values[key], value+string(os.PathListSeparator)) {
			value += string(os.PathListSeparator) + values[key]
		}
		values[key] = value
	}
	keys := slices.Sorted(maps.Keys(values))
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}
