package pricing

import "embed"

//go:embed pricing.v1.json
var profilesFS embed.FS

func DefaultProfileBytes() ([]byte, error) {
	return profilesFS.ReadFile("pricing.v1.json")
}
