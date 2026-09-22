package cmd

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"

	"github.com/datamitsu/datamitsu/internal/inspector"
	"github.com/datamitsu/datamitsu/internal/inspectortheme"
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"github.com/spf13/cobra"
)

func newInspectCommand() *cobra.Command {
	var output string
	var port int
	var themePath string
	var printTheme bool
	command := &cobra.Command{
		Use: "inspect", Short: "Explore the resolved config or export a standalone HTML inspector",
		Long: `Explore tools, apps and managed files in a local configuration inspector.
The server binds to the loopback interface and serves a snapshot of the loaded config.
Use --output to export the same inspector as one self-contained HTML file, or
--output - to write HTML to stdout. No Node.js or network is needed to view it.

The artifact contains display metadata, not environment values, command arguments,
managed file contents or download URLs. Filters show declared applicability, not
an execution plan. Restart the command to refresh the snapshot.`,
		Example: "  datamitsu inspect\n  datamitsu inspect --port 0\n  datamitsu inspect --output atlas.html\n  datamitsu inspect --output - > atlas.html",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if port < 0 || port > 65535 {
				return errors.New("port must be between 0 and 65535")
			}
			if cmd.Flags().Changed("output") && output == "" {
				return errors.New("output must be a file path or -")
			}
			theme, err := inspectorTheme(themePath)
			if err != nil {
				return err
			}
			for _, warning := range inspectortheme.ContrastWarnings(theme) {
				if _, err = fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warning); err != nil {
					return fmt.Errorf("report theme contrast: %w", err)
				}
			}
			if printTheme {
				data, themeErr := inspectortheme.JSON(theme)
				if themeErr != nil {
					return themeErr
				}
				if _, err = cmd.OutOrStdout().Write(data); err != nil {
					return fmt.Errorf("write inspector theme: %w", err)
				}
				return nil
			}
			cfg, _, _, err := loadConfigWithPaths(commandContext(cmd), BeforeConfigPaths, NoAutoConfig, ConfigPaths)
			if err != nil {
				return fmt.Errorf("load inspector config: %w", err)
			}
			html, err := inspector.Render(inspector.Snapshot(cfg, ldflags.Version), theme)
			if err != nil {
				return err
			}
			if output != "" {
				if output == "-" {
					_, err = cmd.OutOrStdout().Write(html)
					if err != nil {
						return fmt.Errorf("write inspector to stdout: %w", err)
					}
					return nil
				}
				if err = os.WriteFile(output, html, 0o600); err != nil {
					return fmt.Errorf("write inspector: %w", err)
				}
				_, err = fmt.Fprintf(cmd.ErrOrStderr(), "Inspector written to %s\n", output)
				if err != nil {
					return fmt.Errorf("report inspector output: %w", err)
				}
				return nil
			}
			var listenConfig net.ListenConfig
			listener, err := listenConfig.Listen(commandContext(cmd), "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
			if err != nil {
				return fmt.Errorf("listen for inspector: %w", err)
			}
			defer func() { _ = listener.Close() }()
			if _, err = fmt.Fprintf(cmd.ErrOrStderr(), "Inspector: http://%s/#/operations\nPress Ctrl+C to stop.\n", listener.Addr()); err != nil {
				return fmt.Errorf("report inspector address: %w", err)
			}
			ctx, cancel := signal.NotifyContext(commandContext(cmd), os.Interrupt)
			defer cancel()
			return inspector.Serve(ctx, listener, html)
		},
	}
	command.Flags().StringVarP(&output, "output", "o", "", "Write standalone HTML to a file, or - for stdout")
	command.Flags().IntVar(&port, "port", 7744, "Local server port (0 chooses an available port)")
	command.Flags().StringVar(&themePath, "theme", "", "Color overrides to merge onto the built-in theme (JSON)")
	command.Flags().BoolVar(&printTheme, "print-theme", false, "Print the theme that would be applied as JSON and exit")
	command.MarkFlagsMutuallyExclusive("output", "port")
	command.MarkFlagsMutuallyExclusive("print-theme", "output")
	command.MarkFlagsMutuallyExclusive("print-theme", "port")
	return command
}

// inspectorTheme merges a user's file onto the built-in palette. An unreadable or
// invalid file is an error: a typo that silently kept the default colors would
// look like the flag had no effect.
func inspectorTheme(path string) (inspectortheme.Theme, error) {
	theme, err := inspector.DefaultTheme()
	if err != nil {
		return nil, err
	}
	if path == "" {
		return theme, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read inspector theme: %w", err)
	}
	overrides, err := inspectortheme.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return inspectortheme.Merge(theme, overrides), nil
}

func init() { rootCmd.AddCommand(newInspectCommand()) }
