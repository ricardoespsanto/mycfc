package app

import (
	"encoding/json"
	"fmt"

	staticassets "github.com/cfcoimbra/mycfc/ui/static"
)

type AssetManifest map[string]string

func loadAssetManifest() (AssetManifest, error) {
	body, err := staticassets.Files.ReadFile("dist/manifest.json")
	if err != nil {
		return nil, fmt.Errorf("read asset manifest: %w", err)
	}
	var manifest AssetManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("decode asset manifest: %w", err)
	}
	for _, required := range []string{"app.css", "app.js"} {
		if manifest[required] == "" {
			return nil, fmt.Errorf("asset manifest missing %s", required)
		}
	}
	return manifest, nil
}
