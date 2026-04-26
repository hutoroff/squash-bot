package changelogs

import "embed"

//go:embed all:*
var FS embed.FS

// Read returns the changelog content for the given version, or empty string if not found.
// Version format: "1.4.0" (no "v" prefix). Looks for file "1.4.0.md".
func Read(version string) (string, error) {
	data, err := FS.ReadFile(version + ".md")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
