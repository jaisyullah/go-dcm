package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"go-dcm/model"
	"go-dcm/service"
)

// MaxSTLUploadSize is the maximum allowed STL upload size.
var MaxSTLUploadSize int64 = 100 << 20 // 100 MB default

// Stl2DcmRequest represents the JSON parameters for STL-to-DICOM conversion.
// Maps to DCMTK stl2dcm CLI options.
type Stl2DcmRequest struct {
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
	GenerateUIDs *bool  `json:"generate_uids,omitempty"` // --generate
	StudyFrom    string `json:"study_from,omitempty"`    // --study-from
	SeriesFrom   string `json:"series_from,omitempty"`   // --series-from

	// Instance number
	InstanceOne bool `json:"instance_one,omitempty"` // --instance-one
	InstanceInc bool `json:"instance_inc,omitempty"` // --instance-inc
	InstanceSet int  `json:"instance_set,omitempty"` // --instance-set

	// Burned-in annotation
	AnnotationNo bool `json:"annotation_no,omitempty"` // --annotation-no

	// Additional DICOM keys
	Keys []string `json:"keys,omitempty"` // --key
}

// ToArgs converts the request to stl2dcm CLI arguments.
func (req *Stl2DcmRequest) ToArgs() []string {
	var args []string

	if req.Title != "" {
		args = append(args, "--title", req.Title)
	}
	if req.ConceptNameCSD != "" && req.ConceptNameCV != "" && req.ConceptNameCM != "" {
		args = append(args, "--concept-name", req.ConceptNameCSD, req.ConceptNameCV, req.ConceptNameCM)
	}
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
	if req.GenerateUIDs != nil && *req.GenerateUIDs {
		args = append(args, "--generate")
	}
	if req.StudyFrom != "" {
		args = append(args, "--study-from", req.StudyFrom)
	}
	if req.SeriesFrom != "" {
		args = append(args, "--series-from", req.SeriesFrom)
	}
	if req.InstanceOne {
		args = append(args, "--instance-one")
	}
	if req.InstanceInc {
		args = append(args, "--instance-inc")
	}
	if req.InstanceSet > 0 {
		args = append(args, "--instance-set", fmt.Sprintf("%d", req.InstanceSet))
	}
	if req.AnnotationNo {
		args = append(args, "--annotation-no")
	}

	// Inject mandatory defaults
	req.injectDefaults()

	for _, key := range req.Keys {
		args = append(args, "--key", key)
	}

	return args
}

func (req *Stl2DcmRequest) injectDefaults() {
	today := time.Now().Format("20060102")
	if !service.HasKeyPrefix(req.Keys, "StudyDate") {
		req.Keys = append(req.Keys, "StudyDate="+today)
	}
	if !service.HasKeyPrefix(req.Keys, "ContentDate") {
		req.Keys = append(req.Keys, "ContentDate="+today)
	}
}

// HandleStl2Dcm handles POST /api/v1/convert/stl2dcm
func HandleStl2Dcm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(MaxSTLUploadSize); err != nil {
		model.WriteError(w, http.StatusBadRequest, "INVALID_FORM", "Failed to parse multipart form", err.Error())
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		model.WriteError(w, http.StatusBadRequest, "MISSING_FILE", "Missing 'file' field in form data", "")
		return
	}
	defer file.Close()

	if err := service.ValidateFileExtension(fileHeader.Filename, service.AllowedSTLExtensions); err != nil {
		model.WriteError(w, http.StatusBadRequest, "INVALID_FILE_TYPE", err.Error(), "Supported format: STL")
		return
	}

	paramsStr := r.FormValue("parameters")
	var reqBody Stl2DcmRequest
	if paramsStr != "" {
		if err := json.Unmarshal([]byte(paramsStr), &reqBody); err != nil {
			model.WriteError(w, http.StatusBadRequest, "INVALID_PARAMS", "Invalid parameters JSON", err.Error())
			return
		}
	}

	if err := service.ValidateKeys(reqBody.Keys); err != nil {
		model.WriteError(w, http.StatusBadRequest, "INVALID_KEY_FORMAT", err.Error(), "")
		return
	}

	tempDir, err := os.MkdirTemp("", "stl2dcm_*")
	if err != nil {
		slog.Error("failed to create temp directory", "error", err)
		model.WriteError(w, http.StatusInternalServerError, "TEMP_DIR_ERROR", "Failed to create temporary directory", "")
		return
	}
	defer os.RemoveAll(tempDir)

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

	outputFilePath := filepath.Join(tempDir, "output.dcm")
// Execute stl2dcm
args := reqBody.ToArgs()
if err := service.RunDCMTK(r.Context(), "stl2dcm", inputFilePath, outputFilePath, args); err != nil {
	model.WriteError(w, http.StatusInternalServerError, "CONVERSION_FAILED", "DICOM conversion failed", err.Error())
	return
}

	outputFilename := resolveOutputFilename(reqBody.Keys, "stl_model")

	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, outputFilename))
	w.Header().Set("Content-Type", "application/dicom")
	http.ServeFile(w, r, outputFilePath)
}
