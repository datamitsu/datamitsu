package tooling

import (
	"bytes"
	"fmt"
	"path/filepath"

	"github.com/datamitsu/datamitsu/internal/config"
)

func (r *ExecutionResult) addNotStarted(files []string) {
	for _, file := range files {
		r.addProcess(ProcessResult{Files: []string{filepath.Clean(file)}, State: ProcessNotStarted, Extraction: ExtractionNone})
	}
}

// outputTail is the last OutputTailBytes of output, copied so it does not pin
// the whole capture.
func outputTail(output []byte) []byte {
	if len(output) > OutputTailBytes {
		output = output[len(output)-OutputTailBytes:]
	}
	return bytes.Clone(output)
}

func cachedOf(planned, toProcess []string) []string {
	if len(planned) == len(toProcess) {
		return nil
	}
	run := make(map[string]bool, len(toProcess))
	for _, file := range toProcess {
		run[file] = true
	}
	var cached []string
	for _, file := range planned {
		if !run[file] {
			cached = append(cached, file)
		}
	}
	return cached
}

// WholeUnit reports whether a task's argv carries no file path, so its one
// process answers for the unit rather than for the files that selected it.
func WholeUnit(task Task) bool {
	if config.RunsPerFile(task.OpConfig, len(task.Files)) {
		return false
	}
	switch config.EffectiveArity(task.OpConfig) {
	case config.ArityNone, config.ArityDir:
		return true
	case config.ArityOne, config.ArityMany:
		return false
	}
	return false
}

// describeFiles fills what a result says about the task's files: which ones it
// was planned with, what became of each, and which process checked it. fallback
// is the state of a file no process and no cache accounts for. It numbers the
// processes, so it runs once every process is recorded.
func describeFiles(task Task, result *ExecutionResult, fallback FileState) {
	result.WholeUnit = WholeUnit(task)
	result.UnitDir = result.RelativeDir
	if config.InferGranularity(task.OpConfig) != config.GranularityFile {
		result.UnitDir = task.UnitDir
	}
	planned := task.Files
	if result.WholeUnit && len(task.UnitMembers) > 0 {
		planned = task.UnitMembers
	}
	result.Files = cleanPaths(planned)

	for i := range result.Processes {
		result.Processes[i].ID = fmt.Sprintf("%s#%d", result.TaskID, i+1)
	}

	cached := make(map[string]bool, len(result.cached))
	for _, file := range result.cached {
		cached[filepath.Clean(file)] = true
	}
	byFile := make(map[string]int)
	whole := -1 // a process given no path answers for every file not cached
	for i, proc := range result.Processes {
		if len(proc.Files) == 0 {
			whole = i
			continue
		}
		for _, file := range proc.Files {
			byFile[file] = i
		}
	}

	result.FileResults = make([]FileResult, 0, len(result.Files))
	gated := make(map[int]gatedFiles)
	for _, file := range result.Files {
		fr := FileResult{File: file, State: fallback}
		idx, ok := byFile[file]
		if !ok && whole >= 0 && !cached[file] {
			idx, ok = whole, true
		}
		switch {
		case cached[file]:
			fr.State, fr.Success = FileCached, true
		case ok:
			proc := result.Processes[idx]
			fr.State = FileState(proc.State)
			if proc.State == ProcessRan {
				fr.ProcessID, fr.Success, fr.ExitCode = proc.ID, proc.Success, proc.ExitCode
				if proc.ThresholdFailed {
					g, seen := gated[idx]
					if !seen {
						g = gatedFilesOf(proc)
						gated[idx] = g
					}
					fr.Success = !g.all && !g.files[file]
				}
				if len(proc.Files) == 1 {
					fr.Edits, fr.Patch = proc.edits, proc.patch
				}
			}
		}
		result.FileResults = append(result.FileResults, fr)
	}

	result.Cached = len(result.Processes) == 0 && len(result.cached) > 0 && len(result.cached) == len(result.Files)
}

// gatedFiles are the files of a process its gating findings belong to; all
// is set when one names no file, or a file the process was not given, and so
// could be about any of them.
type gatedFiles struct {
	files map[string]bool
	all   bool
}

func gatedFilesOf(proc ProcessResult) gatedFiles {
	given := make(map[string]bool, len(proc.Files))
	for _, f := range proc.Files {
		given[f] = true
	}
	g := gatedFiles{files: make(map[string]bool)}
	for _, d := range proc.Diagnostics {
		if !d.Gates {
			continue
		}
		if d.File == "" || (len(proc.Files) > 0 && !given[d.File]) {
			g.all = true
		}
		g.files[d.File] = true
	}
	return g
}

// describeVerdictHit is describeFiles for a unit whose verdict held: the verdict
// answers for every member, not only for the files that selected the task.
func describeVerdictHit(task Task, result *ExecutionResult) {
	result.WholeUnit = WholeUnit(task)
	result.UnitDir = task.UnitDir
	result.Files = cleanPaths(task.UnitMembers)
	result.FileResults = make([]FileResult, 0, len(result.Files))
	for _, file := range result.Files {
		result.FileResults = append(result.FileResults, FileResult{File: file, State: FileVerdictHit, Success: true})
	}
	result.Cached = true
}

func cleanPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Clean(p)
	}
	return out
}
