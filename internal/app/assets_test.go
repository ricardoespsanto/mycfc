package app

import "testing"

func TestLoadAssetManifest(t *testing.T) {
	manifest, err := loadAssetManifest()
	if err != nil {
		t.Fatalf("loadAssetManifest() error = %v", err)
	}
	if manifest["app.css"] == "" || manifest["app.js"] == "" {
		t.Fatalf("manifest = %#v", manifest)
	}
}
