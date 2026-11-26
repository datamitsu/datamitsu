package config

// AIConfig declares the project-local artifacts provisioned by init.
type AIConfig struct {
	Skills map[string]AISkill `json:"skills,omitempty"`
	Agents map[string]AIAgent `json:"agents,omitempty"`
}

// AISkill installs a bundle directory into client discovery locations.
type AISkill struct {
	Enabled *bool                    `json:"enabled,omitempty"`
	Source  AISkillSource            `json:"source"`
	Targets map[string]AISkillTarget `json:"targets"`
}

// AISkillSource identifies a directory relative to the installed bundle root.
type AISkillSource struct {
	Bundle string `json:"bundle"`
	Path   string `json:"path"`
}

// AISkillTarget overrides metadata for one destination.
type AISkillTarget struct {
	Enabled                *bool   `json:"enabled,omitempty"`
	Description            *string `json:"description,omitempty"`
	DisableModelInvocation *bool   `json:"disableModelInvocation,omitempty"`
}

// AIAgent defines profile content shared across enabled destinations.
type AIAgent struct {
	Enabled      *bool                    `json:"enabled,omitempty"`
	Description  string                   `json:"description"`
	Instructions string                   `json:"instructions"`
	Targets      map[string]AIAgentTarget `json:"targets"`
}

// AIAgentTarget overrides one client profile without changing other destinations.
type AIAgentTarget struct {
	Enabled      *bool   `json:"enabled,omitempty"`
	Description  *string `json:"description,omitempty"`
	Instructions *string `json:"instructions,omitempty"`
	Model        *string `json:"model,omitempty"`
}
