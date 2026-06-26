package engine

import (
	"fmt"
	"os"
	"strings"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/logger"
	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/dop251/goja"
	"go.uber.org/zap"
)

func argsToString(args []goja.Value) string {
	parts := make([]string, len(args))
	for i, arg := range args {
		parts[i] = arg.String()
	}
	return strings.Join(parts, " ")
}

func (e *Engine) initConsole() {
	// In JSON-L (quiet) mode every console.* call is diverted to the debug logger
	// instead of stdout/stderr: a user config's console.log would otherwise inject
	// a stray non-JSON line onto the clean stdout machine-data channel (or onto the
	// typed stderr event stream for warn/error), breaking the JSON-L contract.
	_ = e.vm.Set("console", map[string]any{
		"log": func(call goja.FunctionCall) goja.Value {
			if ui.Quiet() {
				logger.Logger.Debug(argsToString(call.Arguments), zap.String("source", "js"), zap.String("level", "log"))
			} else {
				fmt.Println(argsToString(call.Arguments))
			}
			return goja.Undefined()
		},
		"info": func(call goja.FunctionCall) goja.Value {
			if ui.Quiet() {
				logger.Logger.Debug(argsToString(call.Arguments), zap.String("source", "js"), zap.String("level", "info"))
			} else {
				fmt.Println(clr.Cyan("[info]"), argsToString(call.Arguments))
			}
			return goja.Undefined()
		},
		"warn": func(call goja.FunctionCall) goja.Value {
			if ui.Quiet() {
				logger.Logger.Debug(argsToString(call.Arguments), zap.String("source", "js"), zap.String("level", "warn"))
			} else {
				fmt.Fprintln(os.Stderr, clr.Yellow("[warn]"), argsToString(call.Arguments))
			}
			return goja.Undefined()
		},
		"error": func(call goja.FunctionCall) goja.Value {
			if ui.Quiet() {
				logger.Logger.Debug(argsToString(call.Arguments), zap.String("source", "js"), zap.String("level", "error"))
			} else {
				fmt.Fprintln(os.Stderr, clr.Red("[error]"), argsToString(call.Arguments))
			}
			return goja.Undefined()
		},
		"debug": func(call goja.FunctionCall) goja.Value {
			logger.Logger.Debug(argsToString(call.Arguments), zap.String("source", "js"))
			return goja.Undefined()
		},
	})
}
