package executor

import "embed"

//go:embed buildpacks/*
var buildpackFS embed.FS

// GetBuildpack returns the content of an embedded buildpack Dockerfile.
// name should include extension, e.g. "mcp.Dockerfile".
func GetBuildpack(name string) ([]byte, bool) {
	data, err := buildpackFS.ReadFile("buildpacks/" + name)
	if err != nil {
		return nil, false
	}
	return data, true
}

// ListBuildpacks returns the names of all available embedded buildpacks.
func ListBuildpacks() []string {
	entries, err := buildpackFS.ReadDir("buildpacks")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}
