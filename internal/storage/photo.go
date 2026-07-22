package storage

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"

	_ "golang.org/x/image/webp"
)

const maxImageDimension = 12_000

type ValidatedPhoto struct {
	Bytes       []byte
	ContentType string
	Extension   string
	Size        int64
	Width       int
	Height      int
}

func ValidateRepairPhoto(reader io.Reader, maxBytes int64) (ValidatedPhoto, error) {
	if maxBytes < 1 {
		return ValidatedPhoto{}, errors.New("invalid photo size limit")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return ValidatedPhoto{}, fmt.Errorf("read image: %w", err)
	}
	if len(body) == 0 {
		return ValidatedPhoto{}, errors.New("O ficheiro de imagem está vazio.")
	}
	if int64(len(body)) > maxBytes {
		return ValidatedPhoto{}, errors.New("A imagem excede o tamanho máximo permitido.")
	}

	sniffLength := min(len(body), 512)
	detected := http.DetectContentType(body[:sniffLength])
	config, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return ValidatedPhoto{}, errors.New("O ficheiro não contém uma imagem válida.")
	}

	expectedType, extension, ok := imageType(format)
	if !ok || detected != expectedType {
		return ValidatedPhoto{}, errors.New("A imagem deve estar no formato JPEG, PNG ou WebP.")
	}
	if config.Width < 1 || config.Height < 1 || config.Width > maxImageDimension || config.Height > maxImageDimension {
		return ValidatedPhoto{}, errors.New("As dimensões da imagem não são permitidas.")
	}

	return ValidatedPhoto{
		Bytes:       body,
		ContentType: expectedType,
		Extension:   extension,
		Size:        int64(len(body)),
		Width:       config.Width,
		Height:      config.Height,
	}, nil
}

func imageType(format string) (contentType, extension string, ok bool) {
	switch format {
	case "jpeg":
		return "image/jpeg", "jpg", true
	case "png":
		return "image/png", "png", true
	case "webp":
		return "image/webp", "webp", true
	default:
		return "", "", false
	}
}
