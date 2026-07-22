package storage

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func TestValidateRepairPhotoJPEG(t *testing.T) {
	var encoded bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(0, 0, color.White)
	if err := jpeg.Encode(&encoded, img, nil); err != nil {
		t.Fatal(err)
	}
	photo, err := ValidateRepairPhoto(&encoded, 1<<20)
	if err != nil {
		t.Fatalf("ValidateRepairPhoto() error = %v", err)
	}
	if photo.ContentType != "image/jpeg" || photo.Extension != "jpg" || photo.Width != 3 || photo.Height != 2 {
		t.Fatalf("photo = %#v", photo)
	}
}

func TestValidateRepairPhotoPNG(t *testing.T) {
	var encoded bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 4))
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	photo, err := ValidateRepairPhoto(&encoded, 1<<20)
	if err != nil {
		t.Fatalf("ValidateRepairPhoto() error = %v", err)
	}
	if photo.ContentType != "image/png" || photo.Extension != "png" {
		t.Fatalf("photo = %#v", photo)
	}
}

func TestValidateRepairPhotoRejectsInvalidAndOversized(t *testing.T) {
	if _, err := ValidateRepairPhoto(strings.NewReader("<svg></svg>"), 1<<20); err == nil {
		t.Fatal("SVG accepted")
	}
	if _, err := ValidateRepairPhoto(strings.NewReader(strings.Repeat("x", 11)), 10); err == nil {
		t.Fatal("oversized body accepted")
	}
	if _, err := ValidateRepairPhoto(strings.NewReader(""), 10); err == nil {
		t.Fatal("empty body accepted")
	}
}

func TestValidateRepairPhotoRejectsOversizedDimensions(t *testing.T) {
	var encoded bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, maxImageDimension+1, 1))
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateRepairPhoto(&encoded, int64(encoded.Len()+1)); err == nil {
		t.Fatal("oversized dimensions accepted")
	}
}
