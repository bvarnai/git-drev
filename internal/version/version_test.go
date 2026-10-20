package version_test

import (
	"testing"

	"github.com/bvarnai/git-drev/internal/version"
	"github.com/stretchr/testify/assert"
)

func TestVersionString(t *testing.T) {
	s := version.String()
	assert.Contains(t, s, "git-drev version")
	info := version.Get()
	assert.NotEmpty(t, info.GoVersion)
	assert.NotEmpty(t, info.Platform)
}
