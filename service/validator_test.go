package service

import (
	"testing"
)

func TestValidateFileExtension_Valid(t *testing.T) {
	tests := []struct {
		filename string
		allowed  map[string]bool
	}{
		{"test.jpg", AllowedImageExtensions},
		{"test.jpeg", AllowedImageExtensions},
		{"test.png", AllowedImageExtensions},
		{"test.bmp", AllowedImageExtensions},
		{"test.JPG", AllowedImageExtensions},
		{"test.Png", AllowedImageExtensions},
		{"document.pdf", AllowedDocExtensions},
		{"document.PDF", AllowedDocExtensions},
		{"report.xml", AllowedCDAExtensions},
		{"report.cda", AllowedCDAExtensions},
		{"model.stl", AllowedSTLExtensions},
	}

	for _, tt := range tests {
		if err := ValidateFileExtension(tt.filename, tt.allowed); err != nil {
			t.Errorf("expected valid extension for %s, got error: %v", tt.filename, err)
		}
	}
}

func TestValidateFileExtension_Invalid(t *testing.T) {
	tests := []struct {
		filename string
		allowed  map[string]bool
	}{
		{"test.txt", AllowedImageExtensions},
		{"test.gif", AllowedImageExtensions},
		{"test.tiff", AllowedImageExtensions},
		{"document.doc", AllowedDocExtensions},
		{"document.docx", AllowedDocExtensions},
		{"noext", AllowedImageExtensions},
	}

	for _, tt := range tests {
		if err := ValidateFileExtension(tt.filename, tt.allowed); err == nil {
			t.Errorf("expected error for %s, got nil", tt.filename)
		}
	}
}

func TestValidateKeyFormat_Valid(t *testing.T) {
	validKeys := []string{
		"PatientName=Doe^John",
		"PatientID=12345",
		"Modality=XC",
		"0010,0010=Test",
		"StudyDate=20240101",
		"AccessionNumber=ACC001",
	}

	for _, key := range validKeys {
		if err := ValidateKeyFormat(key); err != nil {
			t.Errorf("expected valid key %s, got error: %v", key, err)
		}
	}
}

func TestValidateKeyFormat_Invalid(t *testing.T) {
	invalidKeys := []string{
		"NoEqualsSign",
		"=NoKey",
		"123Invalid=Value",
		"",
	}

	for _, key := range invalidKeys {
		if err := ValidateKeyFormat(key); err == nil {
			t.Errorf("expected error for key '%s', got nil", key)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"normal.dcm", "normal.dcm"},
		{"../../../etc/passwd", "passwd"},
		{"file with spaces.dcm", "file with spaces.dcm"},
		{"", "output.dcm"},
		{".", "output.dcm"},
		{"test\x00file.dcm", "testfile.dcm"},
	}

	for _, tt := range tests {
		result := SanitizeFilename(tt.input)
		if result != tt.expected {
			t.Errorf("SanitizeFilename(%q) = %q, expected %q", tt.input, result, tt.expected)
		}
	}
}

func TestIsPNG(t *testing.T) {
	tests := []struct {
		filename string
		expected bool
	}{
		{"test.png", true},
		{"test.PNG", true},
		{"test.Png", true},
		{"test.jpg", false},
		{"test.bmp", false},
		{"test.pdf", false},
	}

	for _, tt := range tests {
		result := IsPNG(tt.filename)
		if result != tt.expected {
			t.Errorf("IsPNG(%s) = %v, expected %v", tt.filename, result, tt.expected)
		}
	}
}

func TestHasKeyPrefix(t *testing.T) {
	keys := []string{"PatientName=Test", "Modality=XC", "StudyDate=20240101"}

	if !HasKeyPrefix(keys, "Modality") {
		t.Error("expected to find Modality key")
	}
	if !HasKeyPrefix(keys, "PatientName") {
		t.Error("expected to find PatientName key")
	}
	if HasKeyPrefix(keys, "AccessionNumber") {
		t.Error("did not expect to find AccessionNumber key")
	}
}

func TestValidateKeys(t *testing.T) {
	validKeys := []string{"PatientName=Test", "Modality=XC"}
	if err := ValidateKeys(validKeys); err != nil {
		t.Errorf("expected valid keys, got error: %v", err)
	}

	invalidKeys := []string{"PatientName=Test", "InvalidKey"}
	if err := ValidateKeys(invalidKeys); err == nil {
		t.Error("expected error for invalid keys, got nil")
	}
}
