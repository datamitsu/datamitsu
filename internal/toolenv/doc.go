package toolenv

import (
	"strings"
	"unicode/utf8"
)

// DocPath is where the reference page lives, relative to the repository root.
const DocPath = "website/docs/reference/tool-environment.md"

// Markdown renders the reference page for the lists in this package. It emits
// what the formatters would make of it — aligned tables, no wrapping — so the
// `pnpm dm fix` step of task gen:toolenv-doc leaves it unchanged and a test can
// compare it with the committed page byte for byte.
func Markdown() string {
	var b strings.Builder
	b.WriteString(`---
# AUTO-GENERATED — do not edit by hand. Regenerate with ` + "`task gen:toolenv-doc`" + `.
title: Tool Environment
description: What datamitsu removes from and sets in the environment of the tools that fix, lint and check run
---

:::info Auto-generated
This page is generated from ` + "`internal/toolenv`" + ` by ` + "`task gen:toolenv-doc`" + `. Do not edit by hand.
:::

Tools read their environment to decide what to print. Under ` + "`GITHUB_ACTIONS`" + ` several linters switch to GitHub workflow commands, under an AI agent's marker oxlint switches to a one-line format, and ` + "`FORCE_COLOR`" + ` colours output even into a pipe. datamitsu reads a tool's output with its [output parser](./configuration-api.md#output-parser-outputparser), and a parser that meets a format it does not expect reads the run as clean. So ` + "`datamitsu fix`, `lint` and `check`" + ` start every tool, in the language server's format lane too, with the environment datamitsu was started with minus the variables below, and with ` + "`NO_COLOR=1`" + `.

` + "`datamitsu exec`" + ` changes nothing: it hands its app the environment it was started with, so a tool run through ` + "`exec`" + ` in a GitHub Actions job still prints its own annotations.

## Order

1. The environment datamitsu was started with, without the removed variables.
2. The variables the operation names in [` + "`inheritEnv`" + `](./configuration-api.md#inheriting-host-variables-inheritenv), with the host's values.
3. The app's ` + "`env`" + `, then the operation's [` + "`env`" + `](./configuration-api.md#operation-environment-env); a later value wins.
4. ` + "`NO_COLOR=1`" + `, which nothing overrides.

## Removed

`)
	rows := make([][2]string, 0, len(exactNames)+len(prefixes))
	for _, e := range exactNames {
		rows = append(rows, [2]string{"`" + e.Name + "`", e.Why})
	}
	for _, e := range prefixes {
		rows = append(rows, [2]string{"`" + e.Name + "*`", e.Why})
	}
	writeTable(&b, rows)
	b.WriteString(`
A tool that needs one of them names it in its operation's ` + "`inheritEnv`" + ` and gets the host's value, or gets a fixed value through ` + "`env`" + `.

## Set

` + "`NO_COLOR=1`" + ` is set for every tool, after every other layer; neither ` + "`env`" + ` nor ` + "`inheritEnv`" + ` can name it. A tool that colours its output anyway is still parsed: the parser reads the output without its ANSI sequences, and the failure frame shows what the tool printed. The cost is that the output of a failing tool without a parser has no colour, even in a terminal.

## Kept on purpose

`)
	keptRows := make([][2]string, 0, len(kept))
	for _, e := range kept {
		keptRows = append(keptRows, [2]string{"`" + e.Name + "`", e.Why})
	}
	writeTable(&b, keptRows)
	return b.String()
}

// writeTable writes a two-column Variable | Why table padded the way prettier
// pads one.
func writeTable(b *strings.Builder, rows [][2]string) {
	header := [2]string{"Variable", "Why"}
	var width [2]int
	for _, row := range append([][2]string{header}, rows...) {
		for i, cell := range row {
			width[i] = max(width[i], utf8.RuneCountInString(cell), 3)
		}
	}
	line := func(cells [2]string) {
		b.WriteString("|")
		for i, cell := range cells {
			b.WriteString(" " + cell + strings.Repeat(" ", width[i]-utf8.RuneCountInString(cell)) + " |")
		}
		b.WriteString("\n")
	}
	line(header)
	line([2]string{strings.Repeat("-", width[0]), strings.Repeat("-", width[1])})
	for _, row := range rows {
		line(row)
	}
}
