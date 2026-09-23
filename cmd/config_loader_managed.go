package cmd

import (
	"fmt"
	"sort"

	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/hashutil"
)

// finalizeManagedConfigPlacement runs once the whole chain is known, which is
// the earliest point where placement can be decided: a base layer declares its
// managed configs before a later layer ejects any of them. It validates the
// eject declarations, derives each entry's placement and renders what that
// placement needs — the .datamitsu/configs/ content of every internal entry,
// and, for a load that reconciles, the pristine repository render a deletion
// is checked against.
//
// Internal renders are part of the evaluated config, so they are stored in the
// config-evaluation cache and a hit needs no JavaScript to know what init
// writes or whether the file on disk is stale.
func finalizeManagedConfigPlacement(cfg *config.Config, layers []config.ManagedConfigLayer, rootPath string, layerMap config.ManagedConfigLayerMap, opts loadConfigOptions, obs *chainObservations) error {
	if err := config.ValidateManagedConfigToolRefs(cfg.ManagedConfigs, cfg.Tools); err != nil {
		return err
	}
	if err := config.ValidateEject(cfg); err != nil {
		return err
	}
	config.ApplyManagedConfigPlacements(cfg)

	keys := make([]string, 0, len(cfg.ManagedConfigs))
	for key, mc := range cfg.ManagedConfigs {
		if mc.Ejectable {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	for _, key := range keys {
		mc := cfg.ManagedConfigs[key]
		if mc.Placement == config.PlacementInternal {
			rendered, err := config.RenderManagedConfigFromScratch(layers, key, rootPath, config.PlacementInternal)
			if err != nil {
				return err
			}
			if rendered == nil {
				return fmt.Errorf("managed config %q: ejectable, but no config layer renders content for %s", key, config.InternalConfigRelPath(key))
			}
			mc.Render = &config.ManagedConfigRender{Content: *rendered, Hash: hashutil.XXH3Hex([]byte(*rendered))}
			cfg.ManagedConfigs[key] = mc
		}
		if opts.evaluateManagedConfigContent {
			pristine, err := config.RenderManagedConfigFromScratch(layers, key, rootPath, config.PlacementRepo)
			if err != nil {
				return err
			}
			history, ok := layerMap[key]
			if !ok {
				history = &config.ManagedConfigLayerHistory{FileName: key}
				layerMap[key] = history
			}
			history.PristineContent = pristine
		}
	}

	for key, history := range layerMap {
		if mc, ok := cfg.ManagedConfigs[key]; ok && history != nil {
			history.FinalConfig.Placement = mc.Placement
		}
	}

	// A content function runs in whichever engine created it — a remote layer's
	// included — so every engine of the chain is observed again.
	obs.recordAll()
	return nil
}
