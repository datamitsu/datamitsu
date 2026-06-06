package engine

import (
	"fmt"
	"os"
	"strings"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/logger"
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
	_ = e.vm.Set("console", map[string]any{
		"log": func(call goja.FunctionCall) goja.Value {
			fmt.Println(argsToString(call.Arguments))
			return goja.Undefined()
		},
		"info": func(call goja.FunctionCall) goja.Value {
			fmt.Println(clr.Cyan("[info]"), argsToString(call.Arguments))
			return goja.Undefined()
		},
		"warn": func(call goja.FunctionCall) goja.Value {
			fmt.Fprintln(os.Stderr, clr.Yellow("[warn]"), argsToString(call.Arguments))
			return goja.Undefined()
		},
		"error": func(call goja.FunctionCall) goja.Value {
			fmt.Fprintln(os.Stderr, clr.Red("[error]"), argsToString(call.Arguments))
			return goja.Undefined()
		},
		"debug": func(call goja.FunctionCall) goja.Value {
			logger.Logger.Debug(argsToString(call.Arguments), zap.String("source", "js"))
			return goja.Undefined()
		},
	})
}
