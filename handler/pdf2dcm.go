package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"go-dcm/model"
	"go-dcm/service"
)

// MaxPDFUploadSize is the maximum allowed PDF upload size (configurable via env).
var MaxPDFUploadSize int64 = 100 << 20 // 100 MB default

// Pdf2DcmRequest represents the JSON parameters for PDF-to-DICOM conversion.
// These fields map directly to pdf2dcm v3.6.9 CLI options.
type Pdf2DcmRequest struct {
	// Document title
	Title          string `json:"title,omitempty"`            // --title
	ConceptNameCSD string `json:"concept_name_csd,omitempty"` // --concept-name CSD
	ConceptNameCV  string `json:"concept_name_cv,omitempty"`  // --concept-name CV
	ConceptNameCM  string `json:"concept_name_cm,omitempty"`  // --concept-name CM

	// Patient data
	PatientName      string `json:"patient_name,omitempty"`      // --patient-name
	PatientId        string `json:"patient_id,omitempty"`        // --patient-id
	PatientBirthdate string `json:"patient_birthdate,omitempty"` // --patient-birthdate (YYYYMMDD)
	PatientSex       string `json:"patient_sex,omitempty"`       // --patient-sex (M, F, O)

	// Study and series
	GenerateUIDs *bool  `json:"generate_uids,omitempty"` // --generate (default true)
	StudyFrom    string `json:"study_from,omitempty"`    // --study-from
	SeriesFrom   string `json:"series_from,omitempty"`   // --series-from

	// Instance number
	InstanceOne bool `json:"instance_one,omitempty"` // --instance-one
	InstanceInc bool `json:"instance_inc,omitempty"` // --instance-inc
	InstanceSet int  `json:"instance_set,omitempty"` // --instance-set

	// Burned-in annotation
	AnnotationNo  bool `json:"annotation_no,omitempty"`  // --annotation-no
	AnnotationYes bool `json:"annotation_yes,omitempty"` // --annotation-yes

	// Additional DICOM keys (for Manufacturer, ManufacturerModelName, etc.)
	Keys []string `json:"keys,omitempty"` // --key
}

// ToArgs converts the request to pdf2dcm CLI arguments.
func (req *Pdf2DcmRequest) ToArgs() []string {
	var args []string

	// Document title
	if req.Title != "" {
		args = append(args, "--title", req.Title)
	}
	if req.ConceptNameCSD != "" && req.ConceptNameCV != "" && req.ConceptNameCM != "" {
		args = append(args, "--concept-name", req.ConceptNameCSD, req.ConceptNameCV, req.ConceptNameCM)
	}

	// Patient data
	if req.PatientName != "" {
		args = append(args, "--patient-name", req.PatientName)
	}
	if req.PatientId != "" {
		args = append(args, "--patient-id", req.PatientId)
	}
	if req.PatientBirthdate != "" {
		args = append(args, "--patient-birthdate", req.PatientBirthdate)
	}
	if req.PatientSex != "" {
		args = append(args, "--patient-sex", req.PatientSex)
	}

	// Study and series UIDs
	if req.GenerateUIDs != nil && *req.GenerateUIDs {
		args = append(args, "--generate")
	}
	if req.StudyFrom != "" {
		args = append(args, "--study-from", req.StudyFrom)
	}
	if req.SeriesFrom != "" {
		args = append(args, "--series-from", req.SeriesFrom)
	}

	// Instance number
	if req.InstanceOne {
		args = append(args, "--instance-one")
	}
	if req.InstanceInc {
		args = append(args, "--instance-inc")
	}
	if req.InstanceSet > 0 {
		args = append(args, "--instance-set", strconv.Itoa(req.InstanceSet))
	}

	// Burned-in annotation
	if req.AnnotationNo {
		args = append(args, "--annotation-no")
	} else if req.AnnotationYes {
		args = append(args, "--annotation-yes")
	}

	// Inject mandatory DICOM tags if not provided
	req.injectDefaults()

	// Additional DICOM keys
	for _, key := range req.Keys {
		args = append(args, "--key", key)
	}

	return args
}

// injectDefaults adds mandatory DICOM tags if not already specified.
func (req *Pdf2DcmRequest) injectDefaults() {
	today := time.Now().Format("20060102")

	// StudyDate — defaults to today
	if !service.HasKeyPrefix(req.Keys, "StudyDate") {
		req.Keys = append(req.Keys, "StudyDate="+today)
	}

	// ContentDate — defaults to today
	if !service.HasKeyPrefix(req.Keys, "ContentDate") {
		req.Keys = append(req.Keys, "ContentDate="+today)
	}
}

// HandlePdf2Dcm handles POST /api/v1/convert/pdf2dcm
func HandlePdf2Dcm(w http.ResponseWriter, r *http.Request) {
	// Parse multipart form
	if err := r.ParseMultipartForm(MaxPDFUploadSize); err != nil {
		model.WriteError(w, http.StatusBadRequest, "INVALID_FORM", "Failed to parse multipart form", err.Error())
		return
	}

	// Get the uploaded file
	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		model.WriteError(w, http.StatusBadRequest, "MISSING_FILE", "Missing 'file' field in form data", "")
		return
	}
	defer file.Close()

	// Validate file extension
	if err := service.ValidateFileExtension(fileHeader.Filename, service.AllowedDocExtensions); err != nil {
		model.WriteError(w, http.StatusBadRequest, "INVALID_FILE_TYPE", err.Error(), "Supported format: PDF")
		return
	}

	// Parse optional parameters JSON
	paramsStr := r.FormValue("parameters")
	var reqBody Pdf2DcmRequest
	if paramsStr != "" {
		if err := json.Unmarshal([]byte(paramsStr), &reqBody); err != nil {
			model.WriteError(w, http.StatusBadRequest, "INVALID_PARAMS", "Invalid parameters JSON", err.Error())
			return
		}
	}

	// Validate keys format
	if err := service.ValidateKeys(reqBody.Keys); err != nil {
		model.WriteError(w, http.StatusBadRequest, "INVALID_KEY_FORMAT", err.Error(), "Expected format: TagName=Value or gggg,eeee=Value")
		return
	}

	// Create temp directory
	tempDir, err := os.MkdirTemp("", "pdf2dcm_*")
	if err != nil {
		slog.Error("failed to create temp directory", "error", err)
		model.WriteError(w, http.StatusInternalServerError, "TEMP_DIR_ERROR", "Failed to create temporary directory", "")
		return
	}
	defer os.RemoveAll(tempDir)

	// Save uploaded file
	inputFilePath := filepath.Join(tempDir, service.SanitizeFilename(fileHeader.Filename))
	out, err := os.Create(inputFilePath)
	if err != nil {
		slog.Error("failed to create temp file", "error", err)
		model.WriteError(w, http.StatusInternalServerError, "FILE_SAVE_ERROR", "Failed to save uploaded file", "")
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		slog.Error("failed to write uploaded file", "error", err)
		model.WriteError(w, http.StatusInternalServerError, "FILE_WRITE_ERROR", "Failed to write uploaded file", "")
		return
	}
	out.Close()

	// Validate PDF magic bytes
	if err := service.ValidateMIMEType(inputFilePath, []string{"application/pdf"}); err != nil {
		model.WriteError(w, http.StatusBadRequest, "INVALID_PDF", "File does not appear to be a valid PDF", err.Error())
		return
	}

	// Define output path
	outputFilePath := filepath.Join(tempDir, "output.dcm")

	// Execute pdf2dcm (NOT dcmencap — which doesn't exist in DCMTK 3.6.9+)
	args := reqBody.ToArgs()
	if err := service.RunDCMTK(r.Context(), "pdf2dcm", inputFilePath, outputFilePath, args); err != nil {
		model.WriteError(w, http.StatusInternalServerError, "CONVERSION_FAILED", "DICOM conversion failed", err.Error())
		return
	}

	// Determine output filename
	outputFilename := resolveOutputFilename(reqBody.Keys, "document")

	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, outputFilename))
	w.Header().Set("Content-Type", "application/dicom")
	http.ServeFile(w, r, outputFilePath)
}
