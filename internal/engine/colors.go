package engine

import (
	"github.com/dop251/goja"
	"github.com/fatih/color"
)

func (e *Engine) initColors() {
	obj := e.vm.NewObject()

	register := func(name string, attrs ...color.Attribute) {
		fn := color.New(attrs...).SprintFunc()
		_ = obj.Set(name, func(call goja.FunctionCall) goja.Value {
			return e.vm.ToValue(fn(argsToString(call.Arguments)))
		})
	}

	// Foreground colors
	register("black", color.FgBlack)
	register("red", color.FgRed)
	register("green", color.FgGreen)
	register("yellow", color.FgYellow)
	register("blue", color.FgBlue)
	register("magenta", color.FgMagenta)
	register("cyan", color.FgCyan)
	register("white", color.FgWhite)

	// Bright foreground colors
	register("hiBlack", color.FgHiBlack)
	register("hiRed", color.FgHiRed)
	register("hiGreen", color.FgHiGreen)
	register("hiYellow", color.FgHiYellow)
	register("hiBlue", color.FgHiBlue)
	register("hiMagenta", color.FgHiMagenta)
	register("hiCyan", color.FgHiCyan)
	register("hiWhite", color.FgHiWhite)

	// Text attributes
	register("bold", color.Bold)
	register("faint", color.Faint)
	register("italic", color.Italic)
	register("underline", color.Underline)
	register("blink", color.BlinkSlow)
	register("blinkRapid", color.BlinkRapid)
	register("reverse", color.ReverseVideo)
	register("concealed", color.Concealed)
	register("strikethrough", color.CrossedOut)

	// Background colors
	register("bgBlack", color.BgBlack)
	register("bgRed", color.BgRed)
	register("bgGreen", color.BgGreen)
	register("bgYellow", color.BgYellow)
	register("bgBlue", color.BgBlue)
	register("bgMagenta", color.BgMagenta)
	register("bgCyan", color.BgCyan)
	register("bgWhite", color.BgWhite)

	// Bright background colors
	register("bgHiBlack", color.BgHiBlack)
	register("bgHiRed", color.BgHiRed)
	register("bgHiGreen", color.BgHiGreen)
	register("bgHiYellow", color.BgHiYellow)
	register("bgHiBlue", color.BgHiBlue)
	register("bgHiMagenta", color.BgHiMagenta)
	register("bgHiCyan", color.BgHiCyan)
	register("bgHiWhite", color.BgHiWhite)

	_ = e.vm.Set("colors", obj)
}
