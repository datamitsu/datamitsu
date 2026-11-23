package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/datamitsu/datamitsu/internal/cienv"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/exitcode"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/report"
	"github.com/datamitsu/datamitsu/internal/report/render"
	"github.com/datamitsu/datamitsu/internal/report/render/json"
	"github.com/datamitsu/datamitsu/internal/runner"

	"github.com/spf13/cobra"
)

var reportCmd = &cobra.Command{
	Aliases:                    nil,
	SuggestFor:                 nil,
	GroupID:                    "",
	Example:                    "",
	ValidArgs:                  nil,
	ValidArgsFunction:          nil,
	ArgAliases:                 nil,
	BashCompletionFunction:     "",
	Deprecated:                 "",
	Annotations:                nil,
	Version:                    "",
	PersistentPreRun:           nil,
	PersistentPreRunE:          nil,
	PreRun:                     nil,
	PreRunE:                    nil,
	Run:                        nil,
	RunE:                       nil,
	PostRun:                    nil,
	PostRunE:                   nil,
	PersistentPostRun:          nil,
	PersistentPostRunE:         nil,
	FParseErrWhitelist:         cobra.FParseErrWhitelist{UnknownFlags: false, UnknownFlagsHandling: 0},
	CompletionOptions:          cobra.CompletionOptions{DisableDefaultCmd: false, DisableNoDescFlag: false, DisableDescriptions: false, HiddenDefaultCmd: false, DefaultShellCompDirective: nil},
	TraverseChildren:           false,
	Hidden:                     false,
	SilenceErrors:              false,
	SilenceUsage:               false,
	DisableFlagParsing:         false,
	DisableAutoGenTag:          false,
	DisableFlagsInUseLine:      false,
	DisableSuggestions:         false,
	SuggestionsMinimumDistance: 0,
	Use:                        "report",
	Short:                      "Work with the reports fix, lint and check write",
	Long: `Work with the reports fix, lint and check write with --report.

A report is written once, by the run it describes. These commands read one
that exists; none of them runs a tool.`,
	Args: usageArgs(cobra.NoArgs),
}

var (
	reportRenderInput        string
	reportRenderFormat       string
	reportRenderOutput       string
	reportRenderAllowPartial bool
)

var reportRenderCmd = &cobra.Command{
	Aliases:                    nil,
	SuggestFor:                 nil,
	GroupID:                    "",
	Example:                    "",
	ValidArgs:                  nil,
	ValidArgsFunction:          nil,
	ArgAliases:                 nil,
	BashCompletionFunction:     "",
	Deprecated:                 "",
	Annotations:                nil,
	Version:                    "",
	PersistentPreRun:           nil,
	PersistentPreRunE:          nil,
	PreRun:                     nil,
	PreRunE:                    nil,
	Run:                        nil,
	PostRun:                    nil,
	PostRunE:                   nil,
	PersistentPostRun:          nil,
	PersistentPostRunE:         nil,
	FParseErrWhitelist:         cobra.FParseErrWhitelist{UnknownFlags: false, UnknownFlagsHandling: 0},
	CompletionOptions:          cobra.CompletionOptions{DisableDefaultCmd: false, DisableNoDescFlag: false, DisableDescriptions: false, HiddenDefaultCmd: false, DefaultShellCompDirective: nil},
	TraverseChildren:           false,
	Hidden:                     false,
	SilenceErrors:              false,
	SilenceUsage:               false,
	DisableFlagParsing:         false,
	DisableAutoGenTag:          false,
	DisableFlagsInUseLine:      false,
	DisableSuggestions:         false,
	SuggestionsMinimumDistance: 0,
	Use:                        "render",
	Short:                      "Render a run's own JSON report into a report format, offline",
	Long: `Render the own JSON document a run wrote with --report json=<path> into a
report format, as the run would have written it: the same renderers, and the
same completeness rule. The document holds everything a renderer needs, so this
works on another machine and after the checkout changed.

A format that lists findings is refused for a document of a narrowed run
(named files, a subdirectory, --file-scoped, --tools) unless --allow-partial,
exactly as --report is; a document without its completeness fields is read as
incomplete, never as complete. A format written with a completeness companion
gets it beside --output, never on stdout, and an --output that ends in / is a
directory, for a format that splits a run over files (sarif).

  ` + "datamitsu report render --input out/run.json --format json --output -\n" +
		"  datamitsu report render --input out/run.json --format sarif --output sarif/",
	Args: usageArgs(cobra.NoArgs),
	RunE: runReportRender,
}

func init() {
	reportRenderCmd.Flags().StringVar(&reportRenderInput, "input", "", "The own JSON document to read (datamitsu.report/1)")
	reportRenderCmd.Flags().StringVar(&reportRenderFormat, "format", "",
		"The format to write: "+strings.Join(render.Names(), ", ")+"; a format's options follow a ?")
	reportRenderCmd.Flags().StringVar(&reportRenderOutput, "output", render.Stdout, "Where to write it; - is stdout")
	reportRenderCmd.Flags().BoolVar(&reportRenderAllowPartial, "allow-partial", false,
		"Render a format that lists findings for a document of a narrowed run")
	_ = reportRenderCmd.MarkFlagRequired("input")
	_ = reportRenderCmd.MarkFlagRequired("format")
	reportCmd.AddCommand(reportRenderCmd)
	rootCmd.AddCommand(reportCmd)
}

func runReportRender(cmd *cobra.Command, _ []string) error {
	secrets := report.SecretValues(env.EnvironAll())
	name, options, _ := strings.Cut(reportRenderFormat, "?")
	raw := name + "=" + reportRenderOutput
	if options != "" {
		raw += "?" + options
	}
	spec, err := render.ParseSpec(raw)
	if err != nil {
		return maskCodedError(exitcode.UsageErrorf("invalid --format: %w", err), secrets)
	}
	run, err := readReport(reportRenderInput)
	if err != nil {
		return maskCodedError(err, secrets)
	}
	if spec.Stdout() {
		stdoutOwned = true
	}
	report.Mask(run, secrets)
	report.MaskAll(&spec.Options, secrets)
	if why := narrowedBy(run.Selection); len(why) > 0 && !reportRenderAllowPartial && len(render.Listing([]render.Spec{spec})) > 0 {
		return maskCodedError(exitcode.UsageErrorf("the run of %s was narrowed (%s), so report %s would not list every finding: "+
			"pass --allow-partial to render it with the reasons it is incomplete",
			reportRenderInput, strings.Join(why, ", "), spec.Format), secrets)
	}
	if err := render.CheckRunCapacity(spec, run); err != nil {
		return maskCodedError(exitcode.UsageError{Err: err}, secrets)
	}

	for _, note := range render.Notes(run, []render.Spec{spec}) {
		logger.Logger.Warn(note)
	}
	target := render.Open(spec, cmd.OutOrStdout())
	ci, _ := cienv.Current()
	target.Guard = runner.CommandGuard("", ci.Vendor)
	err = target.Write(run)
	if _, declined := errors.AsType[render.DeclinedError](err); declined {
		return nil
	}
	if err != nil {
		return maskCodedError(exitcode.ExportError{Err: fmt.Errorf("report %s: %s: %w", spec.Format, spec.Path, err)}, secrets)
	}
	return nil
}

func maskCodedError(err error, secrets []string) error {
	if err == nil || len(secrets) == 0 {
		return err
	}
	message := err.Error()
	report.MaskAll(&message, secrets)
	if coded, ok := errors.AsType[CodedError](err); ok {
		return maskedCodedError{message: message, code: coded.ExitCode()}
	}
	return errors.New(message)
}

type maskedCodedError struct {
	message string
	code    int
}

func (e maskedCodedError) Error() string { return e.message }

func (e maskedCodedError) ExitCode() int { return e.code }

func readReport(path string) (*report.Run, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read report: %w", err)
	}
	defer func() { _ = f.Close() }()
	run, err := json.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	report.Revise(run)
	return run, nil
}

// narrowedBy says how a document's run was narrowed at plan time.
func narrowedBy(sel report.Selection) []string {
	var why []string
	switch {
	case sel.FileScoped:
		why = append(why, "--file-scoped")
	case sel.Mode == "paths":
		why = append(why, "files named")
	case sel.Mode == "subtree":
		why = append(why, "run in a subdirectory")
	case sel.Mode != "all":
		why = append(why, "selection "+fmt.Sprintf("%q", sel.Mode))
	case !sel.RepositoryWide():
		why = append(why, "recorded paths or directory")
	}
	if len(sel.Tools) > 0 || len(sel.ExcludedTools) > 0 {
		why = append(why, "--tools")
	}
	return why
}
