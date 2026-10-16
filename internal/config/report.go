package config

import (
	"fmt"
	"strings"

	"git-drev/internal/domain"
	"gopkg.in/yaml.v3"
)

// ConfigReport holds the resolved configuration along with provenance and default metadata.
type ConfigReport struct {
	UserConfigPath   string
	SharedConfigPath string
	RepoConfigPath   string
	Config           *domain.ProjectConfig
	InferredFields   map[string]string
}

// FormatReport renders the configuration report as YAML with source headers and inline annotations.
func FormatReport(report *ConfigReport, sourcesOnly bool) (string, error) {
	var b strings.Builder
	userPath := report.UserConfigPath
	if userPath == "" {
		userPath = "none"
	}
	sharedPath := report.SharedConfigPath
	if sharedPath == "" {
		sharedPath = "unset"
	}
	repoPath := report.RepoConfigPath
	if repoPath == "" {
		repoPath = "none"
	}

	b.WriteString("# Source hierarchy:\n")
	b.WriteString(fmt.Sprintf("#   %-72s %s\n", "Tier 1 (User global [~/.config/git-drev/config.yaml, ~/.git-drev.yaml]):", userPath))
	b.WriteString(fmt.Sprintf("#   %-72s %s\n", "Tier 2 (Shared global [$GIT_DREV_CONFIG]):", sharedPath))
	b.WriteString(fmt.Sprintf("#   %-72s %s\n", "Tier 3 (Repo local [.git-drev.yaml, .git-drev/config.yaml]):", repoPath))

	if sourcesOnly {
		return b.String(), nil
	}

	b.WriteString("\n")

	// Convert Config to yaml.Node to annotate inferred/defaulted fields
	data, err := yaml.Marshal(report.Config)
	if err != nil {
		return "", err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", err
	}

	if len(doc.Content) > 0 {
		annotateYAMLNode(doc.Content[0], "", report.InferredFields)
	}

	yamlBytes, err := yaml.Marshal(&doc)
	if err != nil {
		return "", err
	}

	b.Write(yamlBytes)
	return b.String(), nil
}

func annotateYAMLNode(node *yaml.Node, prefix string, inferred map[string]string) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]

		fullKey := keyNode.Value
		if prefix != "" {
			fullKey = prefix + "." + keyNode.Value
		}

		if reason, ok := inferred[strings.ToLower(fullKey)]; ok {
			if valNode.Kind == yaml.ScalarNode {
				valNode.LineComment = reason
			} else {
				keyNode.LineComment = reason
			}
		}

		if valNode.Kind == yaml.MappingNode {
			annotateYAMLNode(valNode, fullKey, inferred)
		}
	}
}
