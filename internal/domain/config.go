package domain

// ProjectConfig defines the unified application configuration.
type ProjectConfig struct {
	WorkDir string       `yaml:"workdir,omitempty"`
	Branch  BranchConfig `yaml:"branch,omitempty"`
	Remote  RemoteConfig `yaml:"remote,omitempty"`
}

// BranchConfig defines branch configuration settings.
type BranchConfig struct {
	Default string `yaml:"default,omitempty"`
}

// RemoteConfig defines the SSH/rsync storage backend parameters.
type RemoteConfig struct {
	Host  string      `yaml:"host,omitempty"`
	Path  string      `yaml:"path,omitempty"`
	Rsync RsyncConfig `yaml:"rsync,omitempty"`
}

// RsyncConfig defines per-direction custom rsync arguments.
type RsyncConfig struct {
	Get string `yaml:"get,omitempty"`
	Put string `yaml:"put,omitempty"`
}
