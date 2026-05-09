package service

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// AllowedImageExtensions lists valid image file extensions.
var AllowedImageExtensions = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".bmp":  true,
	".png":  true,
}

// AllowedDocExtensions lists valid document file extensions.
var AllowedDocExtensions = map[string]bool{
	".pdf": true,
}

// AllowedCDAExtensions lists valid CDA file extensions.
var AllowedCDAExtensions = map[string]bool{
	".xml":  true,
	".cda":  true,
	".html": true,
}

// AllowedSTLExtensions lists valid STL file extensions.
var AllowedSTLExtensions = map[string]bool{
	".stl": true,
}

// keyFormatRegex validates DICOM tag key=value format.
// Supports: TagName=Value or gggg,eeee=Value
var keyFormatRegex = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9]*|[0-9a-fA-F]{4},[0-9a-fA-F]{4})=.+$`)

// ValidateFileExtension checks if a filename has an allowed extension.
func ValidateFileExtension(filename string, allowed map[string]bool) error {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == "" {
		return fmt.Errorf("file has no extension")
	}
	if !allowed[ext] {
		exts := make([]string, 0, len(allowed))
		for k := range allowed {
			exts = append(exts, k)
		}
		return fmt.Errorf("unsupported file extension '%s', allowed: %s", ext, strings.Join(exts, ", "))
	}
	return nil
}

// ValidateMIMEType checks the actual content type of a file by reading its magic bytes.
func ValidateMIMEType(filePath string, allowedMIMEs []string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file for MIME validation: %w", err)
	}
	defer f.Close()

	// Read first 512 bytes for MIME detection
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to read file header: %w", err)
	}

	detectedType := http.DetectContentType(buf[:n])

	for _, allowed := range allowedMIMEs {
		if strings.HasPrefix(detectedType, allowed) {
			return nil
		}
	}

	return fmt.Errorf("invalid file content type '%s', expected one of: %s", detectedType, strings.Join(allowedMIMEs, ", "))
}

// ValidateKeyFormat validates that a --key argument follows DICOM tag format.
func ValidateKeyFormat(key string) error {
	if !keyFormatRegex.MatchString(key) {
		return fmt.Errorf("invalid key format '%s', expected 'TagName=Value' or 'gggg,eeee=Value'", key)
	}
	return nil
}

// ValidateKeys validates a slice of --key arguments.
func ValidateKeys(keys []string) error {
	for _, k := range keys {
		if err := ValidateKeyFormat(k); err != nil {
			return err
		}
	}
	return nil
}

// SanitizeFilename removes path traversal characters and returns a safe filename.
func SanitizeFilename(filename string) string {
	// Take only the base name (strip any directory components)
	filename = filepath.Base(filename)

	// Replace any potentially dangerous characters
	replacer := strings.NewReplacer(
		"..", "",
		"/", "",
		"\\", "",
		"\x00", "",
	)
	filename = replacer.Replace(filename)

	if filename == "" || filename == "." {
		filename = "output.dcm"
	}

	return filename
}

// IsPNG checks if a filename has a PNG extension.
func IsPNG(filename string) bool {
	return strings.ToLower(filepath.Ext(filename)) == ".png"
}

// HasKeyPrefix checks if a key list contains a key with the given prefix.
func HasKeyPrefix(keys []string, prefix string) bool {
	for _, k := range keys {
		if strings.HasPrefix(k, prefix+"=") {
			return true
		}
	}
	return false
}
