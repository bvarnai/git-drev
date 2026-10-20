package config

import (
	"github.com/bvarnai/git-drev/internal/domain"
)

// MergeConfigs overlays override on top of base and returns a new merged ProjectConfig.
func MergeConfigs(base, override *domain.ProjectConfig) *domain.ProjectConfig {
	if base == nil && override == nil {
		return &domain.ProjectConfig{}
	}
	if base == nil {
		return cloneConfig(override)
	}
	if override == nil {
		return cloneConfig(base)
	}

	res := cloneConfig(base)

	// WorkDir
	if override.WorkDir != "" {
		res.WorkDir = override.WorkDir
	}

	// Branch
	if override.Branch.Default != "" {
		res.Branch.Default = override.Branch.Default
	}

	// Remote
	if override.Remote.Host != "" {
		res.Remote.Host = override.Remote.Host
	}
	if override.Remote.Path != "" {
		res.Remote.Path = override.Remote.Path
	}
	if override.Remote.Rsync.Get != "" {
		res.Remote.Rsync.Get = override.Remote.Rsync.Get
	}
	if override.Remote.Rsync.Put != "" {
		res.Remote.Rsync.Put = override.Remote.Rsync.Put
	}

	return res
}

func cloneConfig(c *domain.ProjectConfig) *domain.ProjectConfig {
	if c == nil {
		return nil
	}
	clone := *c
	return &clone
}
