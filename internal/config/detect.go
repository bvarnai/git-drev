package config

import (
	"bufio"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// scpRegex matches SCP-like git URLs: git@host:owner/repo.git
var scpRegex = regexp.MustCompile(`^(?:([^@]+)@)?([^:]+):/?(.+?)(?:\.git)?$`)

// ExtractRepoFromURL extracts the repository slug from a git remote URL.
func ExtractRepoFromURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}

	var p string
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") || strings.HasPrefix(trimmed, "ssh://") || strings.HasPrefix(trimmed, "file://") {
		u, err := url.Parse(trimmed)
		if err == nil && u.Path != "" {
			p = u.Path
		}
	} else if matches := scpRegex.FindStringSubmatch(trimmed); len(matches) == 4 {
		p = matches[3]
	} else {
		p = trimmed
	}

	p = strings.TrimSuffix(p, ".git")
	p = strings.TrimSuffix(p, "/")
	base := path.Base(p)
	if base == "." || base == "/" || base == "" {
		return ""
	}
	return base
}

// DetectRepoName determines the repository name from Git config or directory basename.
func DetectRepoName(workDir string) string {
	cleanDir := filepath.Clean(workDir)
	if cleanDir == "." || cleanDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			cleanDir = cwd
		}
	}

	// 1. Try reading .git/config directly if present
	gitConfigPath := filepath.Join(cleanDir, ".git", "config")
	if data, err := os.ReadFile(gitConfigPath); err == nil {
		if remoteURL := extractOriginURLFromGitConfig(string(data)); remoteURL != "" {
			if slug := ExtractRepoFromURL(remoteURL); slug != "" {
				return slug
			}
		}
	}

	// 2. Fallback to directory basename
	base := filepath.Base(cleanDir)
	if base != "" && base != "." && base != string(filepath.Separator) {
		return base
	}
	return "repository"
}

func extractOriginURLFromGitConfig(content string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	inOriginSection := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			inOriginSection = strings.EqualFold(line, `[remote "origin"]`)
			continue
		}
		if inOriginSection && strings.HasPrefix(line, "url") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}
