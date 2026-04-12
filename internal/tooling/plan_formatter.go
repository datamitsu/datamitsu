package tooling

import (
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/env"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// PlanFormatter defines interface for formatting execution plans
type PlanFormatter interface {
	Format(plan *ExecutionPlan, rootPath, cwdPath string, operation config.OperationType) string
}

// ========================================
// JSON Output Structures
// ========================================

// PlanJSON represents the JSON structure for execution plan
type PlanJSON struct {
	Operation string      `json:"operation"`
	RootPath  string      `json:"rootPath"`
	CwdPath   string      `json:"cwdPath"`
	Groups    []GroupJSON `json:"groups"`
}

// GroupJSON represents a task group in JSON format
type GroupJSON struct {
	Priority       int                 `json:"priority"`
	ParallelGroups []ParallelGroupJSON `json:"parallelGroups"`
}

// ParallelGroupJSON represents a group of tasks that can run in parallel
type ParallelGroupJSON struct {
	CanRunInParallel bool       `json:"canRunInParallel"`
	Tasks            []TaskJSON `json:"tasks"`
}

// TaskJSON represents a task in JSON format
type TaskJSON struct {
	ToolName   string   `json:"toolName"`
	App        string   `json:"app"`
	Args       []string `json:"args"`
	Scope      string   `json:"scope"`
	Batch      bool     `json:"batch"`
	WorkingDir string   `json:"workingDir"`
	Globs      []string `json:"globs"`
	Files      []string `json:"files"`
	FileCount  int      `json:"fileCount"`
}

// ========================================
// Helper Functions
// ========================================

// getWorkingDirForTask returns the working directory for a task
// The working directory is now determined by the Planner and stored in task.ProjectPath
func getWorkingDirForTask(task Task, rootPath string) string {
	if task.ProjectPath != "" {
		return task.ProjectPath
	}
	return rootPath
}

// buildCommandTemplate builds a command string with placeholders (not expanded)
func buildCommandTemplate(task Task) string {
	parts := []string{task.OpConfig.App}
	parts = append(parts, task.OpConfig.Args...)
	return strings.Join(parts, " ")
}

// detectParallelization groups tasks by whether they can run in parallel
// Duplicates logic from executor.detectParallelGroups()
func detectParallelization(tasks []Task) [][]Task {
	var groups [][]Task
	used := make(map[int]bool)

	for i, task1 := range tasks {
		if used[i] {
			continue
		}

		// Start a new parallel group with this task
		parallelGroup := []Task{task1}
		used[i] = true

		// Find other tasks that don't overlap with any task in this group
		for j, task2 := range tasks {
			if used[j] {
				continue
			}

			hasOverlapWithGroup := false
			for _, groupTask := range parallelGroup {
				if HasOverlap(groupTask, task2) {
					hasOverlapWithGroup = true
					break
				}
			}

			if !hasOverlapWithGroup {
				parallelGroup = append(parallelGroup, task2)
				used[j] = true
			}
		}

		groups = append(groups, parallelGroup)
	}

	return groups
}

// makeRelativePath converts absolute path to relative from base directory
func makeRelativePath(path, baseDir string) string {
	relPath, err := filepath.Rel(baseDir, path)
	if err != nil {
		return path // Return absolute path if relative conversion fails
	}
	return relPath
}

// ========================================
// Summary Formatter
// ========================================

// SummaryFormatter formats the plan in human-readable summary format (without file lists)
type SummaryFormatter struct{}

// NewSummaryFormatter creates a new summary formatter
func NewSummaryFormatter() *SummaryFormatter {
	return &SummaryFormatter{}
}

// Format formats the execution plan in summary mode
func (f *SummaryFormatter) Format(plan *ExecutionPlan, rootPath, cwdPath string, operation config.OperationType) string {
	if len(plan.Groups) == 0 {
		return "No applicable tools found for this operation.\n"
	}

	var buf strings.Builder

	fmt.Fprintf(&buf, "\nExecution Plan for '%s' operation:\n", operation)

	for i, group := range plan.Groups {
		fmt.Fprintf(&buf, "\nPriority Group %d (priority: %d):\n", i+1, group.Priority)

		// Detect parallelization within this group
		parallelGroups := detectParallelization(group.Tasks)

		for _, task := range group.Tasks {
			workingDir := getWorkingDirForTask(task, rootPath)
			commandTemplate := buildCommandTemplate(task)

			fmt.Fprintf(&buf, "  ┌─ %s\n", task.ToolName)
			fmt.Fprintf(&buf, "  │  Scope: %s\n", task.OpConfig.Scope)
			fmt.Fprintf(&buf, "  │  Command: %s\n", commandTemplate)
			fmt.Fprintf(&buf, "  │  Working Dir: %s\n", workingDir)

			// Show file count without listing files
			if len(task.Files) == 0 {
				buf.WriteString("  │  Files: whole project\n")
			} else {
				fmt.Fprintf(&buf, "  │  Files: %d matched\n", len(task.Files))
			}
			buf.WriteString("  │\n")
		}

		// Show parallelization info
		buf.WriteString(f.formatParallelizationInfo(group.Tasks, parallelGroups))
	}

	return buf.String()
}

// formatParallelizationInfo formats information about task parallelization
func (f *SummaryFormatter) formatParallelizationInfo(tasks []Task, parallelGroups [][]Task) string {
	maxWorkers := env.GetMaxParallelWorkers()

	if len(tasks) == 1 {
		return "  Parallelization: Single task (sequential)\n"
	}

	if len(parallelGroups) == 1 {
		result := fmt.Sprintf("  Parallelization: All %d tasks can run in parallel (no overlap)\n", len(tasks))
		if len(tasks) > maxWorkers {
			result += fmt.Sprintf("  Worker Pool Limit: max %d concurrent workers\n", maxWorkers)
		}
		return result
	}

	return fmt.Sprintf("  Parallelization: %d parallel groups (some tasks have overlapping files)\n", len(parallelGroups))
}

// ========================================
// Detailed Formatter
// ========================================

// DetailedFormatter formats the plan in human-readable detailed format (with file lists)
type DetailedFormatter struct{}

// NewDetailedFormatter creates a new detailed formatter
func NewDetailedFormatter() *DetailedFormatter {
	return &DetailedFormatter{}
}

// Format formats the execution plan in detailed mode
func (f *DetailedFormatter) Format(plan *ExecutionPlan, rootPath, cwdPath string, operation config.OperationType) string {
	if len(plan.Groups) == 0 {
		return "No applicable tools found for this operation.\n"
	}

	var buf strings.Builder

	fmt.Fprintf(&buf, "\nExecution Plan for '%s' operation:\n", operation)

	for i, group := range plan.Groups {
		fmt.Fprintf(&buf, "\nPriority Group %d (priority: %d):\n", i+1, group.Priority)

		// Detect parallelization within this group
		parallelGroups := detectParallelization(group.Tasks)

		for _, task := range group.Tasks {
			workingDir := getWorkingDirForTask(task, rootPath)
			commandTemplate := buildCommandTemplate(task)

			fmt.Fprintf(&buf, "  ┌─ %s\n", task.ToolName)
			fmt.Fprintf(&buf, "  │  Scope: %s\n", task.OpConfig.Scope)
			fmt.Fprintf(&buf, "  │  Command: %s\n", commandTemplate)
			fmt.Fprintf(&buf, "  │  Working Dir: %s\n", workingDir)

			// Show file lists in detailed mode
			if len(task.Files) == 0 {
				buf.WriteString("  │  Files: whole project\n")
			} else {
				fmt.Fprintf(&buf, "  │  Files (%d):\n", len(task.Files))
				for _, file := range task.Files {
					relPath := makeRelativePath(file, rootPath)
					fmt.Fprintf(&buf, "  │    • %s\n", relPath)
				}
			}
			buf.WriteString("  │\n")
		}

		// Show parallelization info
		buf.WriteString(f.formatParallelizationInfo(group.Tasks, parallelGroups))
	}

	return buf.String()
}

// formatParallelizationInfo formats information about task parallelization
func (f *DetailedFormatter) formatParallelizationInfo(tasks []Task, parallelGroups [][]Task) string {
	maxWorkers := env.GetMaxParallelWorkers()

	if len(tasks) == 1 {
		return "  Parallelization: Single task (sequential)\n"
	}

	if len(parallelGroups) == 1 {
		result := fmt.Sprintf("  Parallelization: All %d tasks can run in parallel (no overlap)\n", len(tasks))
		if len(tasks) > maxWorkers {
			result += fmt.Sprintf("  Worker Pool Limit: max %d concurrent workers\n", maxWorkers)
		}
		return result
	}

	return fmt.Sprintf("  Parallelization: %d parallel groups (some tasks have overlapping files)\n", len(parallelGroups))
}

// ========================================
// JSON Formatter
// ========================================

// JSONFormatter formats the plan in JSON format
type JSONFormatter struct{}

// NewJSONFormatter creates a new JSON formatter
func NewJSONFormatter() *JSONFormatter {
	return &JSONFormatter{}
}

// Format formats the execution plan in JSON mode
func (f *JSONFormatter) Format(plan *ExecutionPlan, rootPath, cwdPath string, operation config.OperationType) string {
	planJSON := PlanJSON{
		Operation: string(operation),
		RootPath:  rootPath,
		CwdPath:   cwdPath,
		Groups:    make([]GroupJSON, 0, len(plan.Groups)),
	}

	for _, group := range plan.Groups {
		groupJSON := GroupJSON{
			Priority:       group.Priority,
			ParallelGroups: make([]ParallelGroupJSON, 0),
		}

		parallelGroups := detectParallelization(group.Tasks)
		for _, parallelTasks := range parallelGroups {
			pgJSON := ParallelGroupJSON{
				CanRunInParallel: len(parallelTasks) > 1,
				Tasks:            make([]TaskJSON, 0, len(parallelTasks)),
			}

			for _, task := range parallelTasks {
				workingDir := getWorkingDirForTask(task, rootPath)

				// Determine batch mode
				batch := task.OpConfig.Batch
				if batch == nil {
					defaultBatch := task.OpConfig.Scope != config.ToolScopePerFile
					batch = &defaultBatch
				}

				taskJSON := TaskJSON{
					ToolName:   task.ToolName,
					App:        task.OpConfig.App,
					Args:       task.OpConfig.Args,
					Scope:      string(task.OpConfig.Scope),
					Batch:      *batch,
					WorkingDir: workingDir,
					Globs:      task.OpConfig.Globs,
					Files:      task.Files,
					FileCount:  len(task.Files),
				}
				pgJSON.Tasks = append(pgJSON.Tasks, taskJSON)
			}
			groupJSON.ParallelGroups = append(groupJSON.ParallelGroups, pgJSON)
		}
		planJSON.Groups = append(planJSON.Groups, groupJSON)
	}

	// Handle marshal errors gracefully
	jsonBytes, err := json.MarshalIndent(planJSON, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": "Failed to marshal plan: %s"}`, err.Error())
	}

	return string(jsonBytes)
}
