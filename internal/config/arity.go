package config

import "strings"

const (
	placeholderFile   = "{file}"
	placeholderFiles  = "{files}"
	placeholderTarget = "{target}"
)

// ArgsReferenceFiles reports whether args put the matched file list in argv.
// When they do not, the list is only a trigger — it decides whether and where
// the operation runs, not what it is given — so chunking it would re-run one
// identical command N times. {target} does not count: it carries a directory.
func ArgsReferenceFiles(args []string) bool {
	return argsContain(args, placeholderFiles) || argsContain(args, placeholderFile)
}

// ArgsReferenceTarget reports whether args carry the {target} directory placeholder.
func ArgsReferenceTarget(args []string) bool {
	return argsContain(args, placeholderTarget)
}

// InferArity derives the argv path shape from the placeholders in args. The
// order is total: a directory argument and a file list are mutually exclusive
// shapes, and the validator rejects configs asking for both.
func InferArity(args []string) ToolArity {
	switch {
	case ArgsReferenceTarget(args):
		return ArityDir
	case argsContain(args, placeholderFiles):
		return ArityMany
	case argsContain(args, placeholderFile):
		return ArityOne
	default:
		return ArityNone
	}
}

// EffectiveArity ignores any declared value: op.Arity is a load-time assertion
// (ValidateTools), never an override, so reading it here would let a config
// that bypassed validation change dispatch.
func EffectiveArity(op ToolOperation) ToolArity {
	return InferArity(op.Args)
}

// RunsPerFile reports whether an operation runs one process per matched file.
//
// ArityOne says so directly. Per-file scope also does, even with no path in
// argv: the planner has already atomised the task to a single file, the tool
// reads it from the working directory, and the stdin/stdout contract is
// implemented only on that path. ArityMany opts out of both — an explicit
// {files} list is one process by definition.
func RunsPerFile(op ToolOperation, fileCount int) bool {
	if fileCount == 0 || EffectiveArity(op) == ArityMany {
		return false
	}
	return EffectiveArity(op) == ArityOne || op.Scope == ToolScopePerFile
}

func argsContain(args []string, token string) bool {
	for _, arg := range args {
		if strings.Contains(arg, token) {
			return true
		}
	}
	return false
}
