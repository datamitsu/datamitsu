package engine

import (
	"github.com/datamitsu/datamitsu/internal/engine/tools"
)

func (e *Engine) initTools() {
	_ = tools.RegisterIgnoreToolsInVM(e.vm)
	_ = tools.RegisterPathToolsInVM(e.vm, e.rootPath)
}
