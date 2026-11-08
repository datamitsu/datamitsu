package main

import (
	"encoding/json"
	"flag"
	"io"
	"os"
)

type record struct {
	Args  []string          `json:"args"`
	Cwd   string            `json:"cwd"`
	Stdin string            `json:"stdin"`
	Env   map[string]string `json:"env"`
}

func main() {
	path := flag.String("record", "", "write process record")
	exitCode := flag.Int("exit", 0, "exit status")
	flag.Parse()
	stdin, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(90)
	}
	if *path != "" {
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(91)
		}
 env := make(map[string]string)
		selected := map[string]bool{"GOCACHE":true,"GOMODCACHE":true,"GOPATH":true,"GOTOOLCHAIN":true,"HOME":true,"PATH":true}
		for _, item := range os.Environ() {
			key, value, ok := splitEnv(item)
			if ok && selected[key] { env[key] = value }
		}
		data, err := json.Marshal(record{Args: os.Args[1:], Cwd: cwd, Stdin: string(stdin), Env: env})
		if err != nil || os.WriteFile(*path, data, 0o600) != nil {
			os.Exit(92)
		}
	}
	_, _ = os.Stdout.WriteString("probe stdout\n")
	_, _ = os.Stderr.WriteString("probe stderr\n")
	os.Exit(*exitCode)
}

func splitEnv(value string) (string, string, bool) {
	for i := range value {
		if value[i] == '=' {
			return value[:i], value[i+1:], true
		}
	}
	return "", "", false
}
