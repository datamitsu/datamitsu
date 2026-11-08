package dockerfile

import (
	"sort"

	"github.com/datamitsu/datamitsu/internal/config"
)

func pythonExportSources(plan Plan) []RuntimeAppStage {
	byRuntime := make(map[string]RuntimeAppStage)
	for _, stage := range plan.RuntimeAppStages {
		if stage.Kind != config.RuntimeKindUV {
			continue
		}
		current, ok := byRuntime[stage.Runtime]
		if !ok || stage.App < current.App {
			byRuntime[stage.Runtime] = stage
		}
	}

	sources := make([]RuntimeAppStage, 0, len(byRuntime))
	for _, stage := range byRuntime {
		sources = append(sources, stage)
	}
	sort.Slice(sources, func(i, j int) bool {
		return sources[i].Runtime < sources[j].Runtime
	})
	return sources
}
