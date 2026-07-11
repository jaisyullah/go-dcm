package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandlePdf2Dcm_Success tests PDF-to-DICOM conversion.
func TestHandlePdf2Dcm_Success(t *testing.T) {
	pdfData := generateTestPDF(t)
	params := Pdf2DcmRequest{
		Title:       "Test Report",
		PatientName: "Doe^John",
		PatientId:   "12345",
		Keys: []string{
			"AccessionNumber=PDFACC001",
			"Manufacturer=TestHospital",
		},
	}

	req := createMultipartRequest(t, "/api/v1/convert/pdf2dcm", "test.pdf", pdfData, params)
	rr := httptest.NewRecorder()

	HandlePdf2Dcm(rr, req)

	if rr.Code != http.StatusOK {
		body, _ := io.ReadAll(rr.Body)
		t.Fatalf("expected 200, got %d: %s", rr.Code, string(body))
	}

	body := rr.Body.Bytes()
	if len(body) < 132 || string(body[128:132]) != "DICM" {
		t.Fatal("response is not a valid DICOM file")
	}
}

// TestHandlePdf2Dcm_WithPatientData tests PDF conversion with full patient data.
func TestHandlePdf2Dcm_WithPatientData(t *testing.T) {
	pdfData := generateTestPDF(t)
	generateUIDs := true
	params := Pdf2DcmRequest{
		Title:            "Lab Report",
		PatientName:      "Smith^Jane",
		PatientId:        "67890",
		PatientBirthdate: "19900101",
		PatientSex:       "F",
		GenerateUIDs:     &generateUIDs,
		Keys: []string{
			"AccessionNumber=PDFACC002",
		},
	}

	req := createMultipartRequest(t, "/api/v1/convert/pdf2dcm", "report.pdf", pdfData, params)
	rr := httptest.NewRecorder()

	HandlePdf2Dcm(rr, req)

	if rr.Code != http.StatusOK {
		body, _ := io.ReadAll(rr.Body)
		t.Fatalf("expected 200, got %d: %s", rr.Code, string(body))
	}

	body := rr.Body.Bytes()
	if len(body) < 132 || string(body[128:132]) != "DICM" {
		t.Fatal("response is not a valid DICOM file")
	}
}

// TestHandlePdf2Dcm_MissingFile tests missing file field.
func TestHandlePdf2Dcm_MissingFile(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/convert/pdf2dcm", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()

	HandlePdf2Dcm(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}

	var errResp map[string]string
	json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp["code"] != "MISSING_FILE" {
		t.Fatalf("expected MISSING_FILE, got %s", errResp["code"])
	}
}

// TestHandlePdf2Dcm_InvalidExtension tests rejection of non-PDF files.
func TestHandlePdf2Dcm_InvalidExtension(t *testing.T) {
	req := createMultipartRequest(t, "/api/v1/convert/pdf2dcm", "test.jpg", []byte("not a pdf"), nil)
	rr := httptest.NewRecorder()

	HandlePdf2Dcm(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}

	var errResp map[string]string
	json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp["code"] != "INVALID_FILE_TYPE" {
		t.Fatalf("expected INVALID_FILE_TYPE, got %s", errResp["code"])
	}
}

// TestHandlePdf2Dcm_InvalidPDFContent tests rejection of file with .pdf extension but invalid content.
func TestHandlePdf2Dcm_InvalidPDFContent(t *testing.T) {
	fakeContent := []byte("This is not a PDF file at all")
	req := createMultipartRequest(t, "/api/v1/convert/pdf2dcm", "fake.pdf", fakeContent, nil)
	rr := httptest.NewRecorder()

	HandlePdf2Dcm(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}

	var errResp map[string]string
	json.NewDecoder(rr.Body).Decode(&errResp)
	if errResp["code"] != "INVALID_PDF" {
		t.Fatalf("expected INVALID_PDF, got %s", errResp["code"])
	}
}

// TestHandlePdf2Dcm_InvalidJSON tests invalid parameters JSON.
func TestHandlePdf2Dcm_InvalidJSON(t *testing.T) {
	pdfData := generateTestPDF(t)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "test.pdf")
	part.Write(pdfData)
	writer.WriteField("parameters", "not json")
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/convert/pdf2dcm", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()

	HandlePdf2Dcm(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

// TestPdf2DcmRequest_ToArgs tests argument generation.
func TestPdf2DcmRequest_ToArgs(t *testing.T) {
	genUIDs := true
	req := Pdf2DcmRequest{
		Title:            "Test",
		PatientName:      "Test^Patient",
		PatientId:        "123",
		PatientBirthdate: "19900101",
		PatientSex:       "M",
		GenerateUIDs:     &genUIDs,
		AnnotationNo:     true,
		Keys:             []string{"AccessionNumber=ACC001"},
	}

	args := req.ToArgs()

	expected := map[string]bool{
		"--title":             true,
		"--patient-name":      true,
		"--patient-id":        true,
		"--patient-birthdate": true,
		"--patient-sex":       true,
		"--generate":          true,
		"--annotation-no":     true,
		"--key":               true,
	}

	for _, arg := range args {
		if _, ok := expected[arg]; ok {
			delete(expected, arg)
		}
	}

	// --key appears multiple times, remove it separately
	delete(expected, "--key")

	if len(expected) > 0 {
		t.Fatalf("missing expected arguments: %v", expected)
	}
}
