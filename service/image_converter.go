package service

import (
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/bmp"
)

// ConvertPNGtoBMP converts a PNG image to BMP format (lossless) for DCMTK compatibility.
// Returns the path to the converted BMP file.
func ConvertPNGtoBMP(inputPath string, outputDir string) (string, error) {
	f, err := os.Open(inputPath)
	if err != nil {
		return "", fmt.Errorf("failed to open PNG file: %w", err)
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		return "", fmt.Errorf("failed to decode PNG: %w", err)
	}

	// Generate output path with .bmp extension
	baseName := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	outputPath := filepath.Join(outputDir, baseName+".bmp")

	out, err := os.Create(outputPath)
	if err != nil {
		return "", fmt.Errorf("failed to create BMP file: %w", err)
	}
	defer out.Close()

	if err := bmp.Encode(out, img); err != nil {
		return "", fmt.Errorf("failed to encode BMP: %w", err)
	}

	return outputPath, nil
}

// DetectImageFormat detects whether a file is JPEG, PNG, or BMP based on its content.
// Returns the format string: "jpeg", "png", "bmp", or an error if unrecognized.
func DetectImageFormat(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	_, format, err := image.DecodeConfig(f)
	if err != nil {
		return "", fmt.Errorf("failed to detect image format: %w", err)
	}

	return format, nil
}
