package handler

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/image/bmp"
)

// createMultipartRequest creates a multipart form request with a file and optional parameters.
func createMultipartRequest(t *testing.T, url string, filename string, fileData []byte, params interface{}) *http.Request {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add file
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	if _, err := part.Write(fileData); err != nil {
		t.Fatalf("failed to write file data: %v", err)
	}

	// Add parameters if provided
	if params != nil {
		paramsJSON, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("failed to marshal parameters: %v", err)
		}
		if err := writer.WriteField("parameters", string(paramsJSON)); err != nil {
			t.Fatalf("failed to write parameters field: %v", err)
		}
	}

	writer.Close()

	req := httptest.NewRequest(http.MethodPost, url, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

// generateTestJPEG creates a minimal JPEG image in memory.
func generateTestJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	buf := &bytes.Buffer{}
	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("failed to encode test JPEG: %v", err)
	}
	return buf.Bytes()
}

// generateTestPNG creates a minimal PNG image in memory.
func generateTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 0, G: 0, B: 255, A: 255})
		}
	}
	buf := &bytes.Buffer{}
	if err := png.Encode(buf, img); err != nil {
		t.Fatalf("failed to encode test PNG: %v", err)
	}
	return buf.Bytes()
}

// generateTestBMP creates a minimal BMP image in memory.
func generateTestBMP(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 0, G: 255, B: 0, A: 255})
		}
	}
	buf := &bytes.Buffer{}
	if err := bmp.Encode(buf, img); err != nil {
		t.Fatalf("failed to encode test BMP: %v", err)
	}
	return buf.Bytes()
}

// generateTestPDF creates a minimal valid PDF document.
func generateTestPDF(t *testing.T) []byte {
	t.Helper()
	return []byte(`%PDF-1.4
1 0 obj
<< /Type /Catalog /Pages 2 0 R >>
endobj
2 0 obj
<< /Type /Pages /Kids [3 0 R] /Count 1 >>
endobj
3 0 obj
<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>
endobj
xref
0 4
0000000000 65535 f 
0000000009 00000 n 
0000000058 00000 n 
0000000115 00000 n 
trailer
<< /Size 4 /Root 1 0 R >>
startxref
190
%%EOF`)
}

// TestHandleImg2Dcm_JPEG tests JPEG-to-DICOM conversion.
func TestHandleImg2Dcm_JPEG(t *testing.T) {
	jpegData := generateTestJPEG(t, 100, 100)
	params := Img2DcmRequest{
		Keys: []string{
			"PatientName=Test^Patient",
			"PatientID=12345",
			"Modality=XC",
			"AccessionNumber=TESTACC001",
		},
	}

	req := createMultipartRequest(t, "/api/v1/convert/img2dcm", "test.jpg", jpegData, params)
	rr := httptest.NewRecorder()

	HandleImg2Dcm(rr, req)

	if rr.Code != http.StatusOK {
		body, _ := io.ReadAll(rr.Body)
		t.Fatalf("expected 200, got %d: %s", rr.Code, string(body))
	}

	// Verify it's a DICOM file (starts with 128 bytes preamble + "DICM")
	body := rr.Body.Bytes()
	if len(body) < 132 {
		t.Fatal("response too short to be a valid DICOM file")
	}
	if string(body[128:132]) != "DICM" {
		t.Fatal("response does not start with DICM magic bytes")
	}

	// Check Content-Disposition header
	cd := rr.Header().Get("Content-Disposition")
	if cd == "" {
		t.Fatal("missing Content-Disposition header")
	}
	if rr.Header().Get("Content-Type") != "application/dicom" {
		// http.ServeFile may set application/octet-stream, which is also acceptable
		t.Logf("Content-Type: %s (acceptable)", rr.Header().Get("Content-Type"))
	}
}

// TestHandleImg2Dcm_PNG tests PNG-to-DICOM conversion (with auto-BMP conversion).
func TestHandleImg2Dcm_PNG(t *testing.T) {
	pngData := generateTestPNG(t, 100, 100)
	params := Img2DcmRequest{
		Keys: []string{
			"PatientName=Test^PNGPatient",
			"PatientID=67890",
		},
	}

	req := createMultipartRequest(t, "/api/v1/convert/img2dcm", "test.png", pngData, params)
	rr := httptest.NewRecorder()

	HandleImg2Dcm(rr, req)

	if rr.Code != http.StatusOK {
		body, _ := io.ReadAll(rr.Body)
		t.Fatalf("expected 200, got %d: %s", rr.Code, string(body))
	}

	body := rr.Body.Bytes()
	if len(body) < 132 || string(body[128:132]) != "DICM" {
		t.Fatal("response is not a valid DICOM file")
	}
}

// TestHandleImg2Dcm_BMP tests BMP-to-DICOM conversion.
func TestHandleImg2Dcm_BMP(t *testing.T) {
	bmpData := generateTestBMP(t, 100, 100)
	params := Img2DcmRequest{
		InputFormat: "BMP",
		Keys: []string{
			"PatientName=Test^BMPPatient",
			"PatientID=11111",
		},
	}

	req := createMultipartRequest(t, "/api/v1/convert/img2dcm", "test.bmp", bmpData, params)
	rr := httptest.NewRecorder()

	HandleImg2Dcm(rr, req)

	if rr.Code != http.StatusOK {
		body, _ := io.ReadAll(rr.Body)
		t.Fatalf("expected 200, got %d: %s", rr.Code, string(body))
	}

	body := rr.Body.Bytes()
	if len(body) < 132 || string(body[128:132]) != "DICM" {
		t.Fatal("response is not a valid DICOM file")
	}
}

// TestHandleImg2Dcm_MissingFile tests missing file field.
func TestHandleImg2Dcm_MissingFile(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/convert/img2dcm", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()

	HandleImg2Dcm(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}

	var errResp map[string]string
	json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp["code"] != "MISSING_FILE" {
		t.Fatalf("expected MISSING_FILE error code, got %s", errResp["code"])
	}
}

// TestHandleImg2Dcm_InvalidExtension tests rejection of unsupported file types.
func TestHandleImg2Dcm_InvalidExtension(t *testing.T) {
	req := createMultipartRequest(t, "/api/v1/convert/img2dcm", "test.txt", []byte("not an image"), nil)
	rr := httptest.NewRecorder()

	HandleImg2Dcm(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}

	var errResp map[string]string
	json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp["code"] != "INVALID_FILE_TYPE" {
		t.Fatalf("expected INVALID_FILE_TYPE, got %s", errResp["code"])
	}
}

// TestHandleImg2Dcm_InvalidJSON tests invalid parameters JSON.
func TestHandleImg2Dcm_InvalidJSON(t *testing.T) {
	jpegData := generateTestJPEG(t, 50, 50)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, _ := writer.CreateFormFile("file", "test.jpg")
	part.Write(jpegData)
	writer.WriteField("parameters", "{ invalid json }")
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/convert/img2dcm", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()

	HandleImg2Dcm(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// TestHandleImg2Dcm_InvalidKeyFormat tests rejection of malformed keys.
func TestHandleImg2Dcm_InvalidKeyFormat(t *testing.T) {
	jpegData := generateTestJPEG(t, 50, 50)
	params := Img2DcmRequest{
		Keys: []string{"InvalidKeyWithoutEquals"},
	}

	req := createMultipartRequest(t, "/api/v1/convert/img2dcm", "test.jpg", jpegData, params)
	rr := httptest.NewRecorder()

	HandleImg2Dcm(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}

	var errResp map[string]string
	json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp["code"] != "INVALID_KEY_FORMAT" {
		t.Fatalf("expected INVALID_KEY_FORMAT, got %s", errResp["code"])
	}
}

// TestHandleImg2Dcm_DefaultModality tests that Modality is auto-injected.
func TestHandleImg2Dcm_DefaultModality(t *testing.T) {
	req := &Img2DcmRequest{
		Keys: []string{"PatientName=Test^Patient"},
	}

	// Call ToArgs which triggers injectDefaults
	args := req.ToArgs()

	foundModality := false
	for _, arg := range args {
		if arg == "Modality=OT" {
			foundModality = true
		}
	}
	if !foundModality {
		t.Fatal("expected default Modality=OT to be injected")
	}
}

// TestHandleImg2Dcm_VLPhotoModality tests correct modality for VL Photo class.
func TestHandleImg2Dcm_VLPhotoModality(t *testing.T) {
	req := &Img2DcmRequest{
		OutputSopClass: "vl-photo",
		Keys:           []string{"PatientName=Test^Patient"},
	}

	args := req.ToArgs()

	foundModality := false
	for _, arg := range args {
		if arg == "Modality=XC" {
			foundModality = true
		}
	}
	if !foundModality {
		t.Fatal("expected Modality=XC for vl-photo SOP class")
	}
}

// TestHandleImg2Dcm_AccessionNumberFilename tests filename from AccessionNumber.
func TestHandleImg2Dcm_AccessionNumberFilename(t *testing.T) {
	keys := []string{"AccessionNumber=ACC999"}
	filename := resolveOutputFilename(keys, "image")
	if filename != "ACC999.dcm" {
		t.Fatalf("expected ACC999.dcm, got %s", filename)
	}
}

// TestHandleImg2Dcm_DefaultFilename tests default filename without AccessionNumber.
func TestHandleImg2Dcm_DefaultFilename(t *testing.T) {
	keys := []string{"PatientName=Test"}
	filename := resolveOutputFilename(keys, "image")
	if filename != "image_output.dcm" {
		t.Fatalf("expected image_output.dcm, got %s", filename)
	}
}
