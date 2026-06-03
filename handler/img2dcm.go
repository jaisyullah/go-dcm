package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-dcm/model"
	"go-dcm/service"
)

// MaxImageUploadSize is the maximum allowed image upload size (configurable via env).
var MaxImageUploadSize int64 = 50 << 20 // 50 MB default

// Img2DcmRequest represents the JSON parameters for image-to-DICOM conversion.
type Img2DcmRequest struct {
	InputFormat       string   `json:"input_format,omitempty"`       // --input-format (JPEG, BMP) — auto-detected if omitted
	DatasetFrom       string   `json:"dataset_from,omitempty"`       // --dataset-from
	StudyFrom         string   `json:"study_from,omitempty"`         // --study-from
	SeriesFrom        string   `json:"series_from,omitempty"`        // --series-from
	InstanceInc       bool     `json:"instance_inc,omitempty"`       // --instance-inc
	DisableProgr      bool     `json:"disable_progr,omitempty"`      // --disable-progr
	DisableExt        bool     `json:"disable_ext,omitempty"`        // --disable-ext
	InsistOnJfif      bool     `json:"insist_on_jfif,omitempty"`     // --insist-on-jfif
	KeepAppn          bool     `json:"keep_appn,omitempty"`          // --keep-appn
	RemoveCom         bool     `json:"remove_com,omitempty"`         // --remove-com
	NoChecks          bool     `json:"no_checks,omitempty"`          // --no-checks
	NoType2Insert     bool     `json:"no_type2_insert,omitempty"`    // --no-type2-insert
	NoType1Invent     bool     `json:"no_type1_invent,omitempty"`    // --no-type1-invent
	Transliterate     bool     `json:"transliterate,omitempty"`      // --transliterate
	DiscardIllegal    bool     `json:"discard_illegal,omitempty"`    // --discard-illegal
	Keys              []string `json:"keys,omitempty"`               // --key
	OutputSopClass    string   `json:"output_sop_class,omitempty"`   // --sec-capture, --new-sc, --vl-photo, --oph-photo
	WriteDataset      bool     `json:"write_dataset,omitempty"`      // --write-dataset
	GroupLengthRemove bool     `json:"group_length_remove,omitempty"` // --group-length-remove
	GroupLengthCreate bool     `json:"group_length_create,omitempty"` // --group-length-create
	LengthUndefined   bool    `json:"length_undefined,omitempty"`   // --length-undefined
	PaddingOff        bool     `json:"padding_off,omitempty"`        // --padding-off
}

// ToArgs converts the request struct to DCMTK img2dcm CLI arguments.
func (req *Img2DcmRequest) ToArgs() []string {
	var args []string

	if req.InputFormat != "" {
		args = append(args, "--input-format", req.InputFormat)
	}
	if req.DatasetFrom != "" {
		args = append(args, "--dataset-from", req.DatasetFrom)
	}
	if req.StudyFrom != "" {
		args = append(args, "--study-from", req.StudyFrom)
	}
	if req.SeriesFrom != "" {
		args = append(args, "--series-from", req.SeriesFrom)
	}
	if req.InstanceInc {
		args = append(args, "--instance-inc")
	}
	if req.DisableProgr {
		args = append(args, "--disable-progr")
	}
	if req.DisableExt {
		args = append(args, "--disable-ext")
	}
	if req.InsistOnJfif {
		args = append(args, "--insist-on-jfif")
	}
	if req.KeepAppn {
		args = append(args, "--keep-appn")
	}
	if req.RemoveCom {
		args = append(args, "--remove-com")
	}
	if req.NoChecks {
		args = append(args, "--no-checks")
	}
	if req.NoType2Insert {
		args = append(args, "--no-type2-insert")
	}
	if req.NoType1Invent {
		args = append(args, "--no-type1-invent")
	}
	if req.Transliterate {
		args = append(args, "--transliterate")
	}
	if req.DiscardIllegal {
		args = append(args, "--discard-illegal")
	}

	// Inject mandatory DICOM tags if not provided by caller
	req.injectDefaults()

	for _, key := range req.Keys {
		args = append(args, "--key", key)
	}

	switch req.OutputSopClass {
	case "sec-capture":
		args = append(args, "--sec-capture")
	case "new-sc":
		args = append(args, "--new-sc")
	case "vl-photo":
		args = append(args, "--vl-photo")
	case "oph-photo":
		args = append(args, "--oph-photo")
	}

	if req.WriteDataset {
		args = append(args, "--write-dataset")
	}
	if req.GroupLengthRemove {
		args = append(args, "--group-length-remove")
	} else if req.GroupLengthCreate {
		args = append(args, "--group-length-create")
	}
	if req.LengthUndefined {
		args = append(args, "--length-undefined")
	}
	if req.PaddingOff {
		args = append(args, "--padding-off")
	}

	return args
}

// injectDefaults adds mandatory DICOM tags if not already specified.
func (req *Img2DcmRequest) injectDefaults() {
	today := time.Now().Format("20060102")

	// Modality is mandatory — default to OT (Other) for Secondary Capture
	if !service.HasKeyPrefix(req.Keys, "Modality") {
		modality := "OT"
		switch req.OutputSopClass {
		case "vl-photo":
			modality = "XC" // External Camera Photography
		case "oph-photo":
			modality = "OP" // Ophthalmic Photography
		}
		req.Keys = append(req.Keys, "Modality="+modality)
	}

	// StudyDate — defaults to today
	if !service.HasKeyPrefix(req.Keys, "StudyDate") {
		req.Keys = append(req.Keys, "StudyDate="+today)
	}

	// ContentDate — defaults to today
	if !service.HasKeyPrefix(req.Keys, "ContentDate") {
		req.Keys = append(req.Keys, "ContentDate="+today)
	}
}

// HandleImg2Dcm handles POST /api/v1/convert/img2dcm
func HandleImg2Dcm(w http.ResponseWriter, r *http.Request) {
	// Parse multipart form with configurable size limit
	if err := r.ParseMultipartForm(MaxImageUploadSize); err != nil {
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
	if err := service.ValidateFileExtension(fileHeader.Filename, service.AllowedImageExtensions); err != nil {
		model.WriteError(w, http.StatusBadRequest, "INVALID_FILE_TYPE", err.Error(), "Supported formats: JPEG, BMP, PNG")
		return
	}

	// Parse optional parameters JSON
	paramsStr := r.FormValue("parameters")
	var reqBody Img2DcmRequest
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
	tempDir, err := os.MkdirTemp("", "img2dcm_*")
	if err != nil {
		slog.Error("failed to create temp directory", "error", err)
		model.WriteError(w, http.StatusInternalServerError, "TEMP_DIR_ERROR", "Failed to create temporary directory", "")
		return
	}
	defer os.RemoveAll(tempDir)

	// Save uploaded file to temp directory
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

	// If PNG, convert to BMP (lossless) since img2dcm only supports JPEG and BMP
	if service.IsPNG(fileHeader.Filename) {
		slog.Info("converting PNG to BMP for DCMTK compatibility", "file", fileHeader.Filename)
		bmpPath, err := service.ConvertPNGtoBMP(inputFilePath, tempDir)
		if err != nil {
			model.WriteError(w, http.StatusBadRequest, "PNG_CONVERSION_ERROR", "Failed to convert PNG to BMP", err.Error())
			return
		}
		inputFilePath = bmpPath

		// Force BMP input format for img2dcm
		reqBody.InputFormat = "BMP"
	}

	// Define output file path
	outputFilePath := filepath.Join(tempDir, "output.dcm")

	// Execute img2dcm
	args := reqBody.ToArgs()
	if err := service.RunDCMTK(r.Context(), "img2dcm", inputFilePath, outputFilePath, args); err != nil {
		model.WriteError(w, http.StatusInternalServerError, "CONVERSION_FAILED", "DICOM conversion failed", err.Error())
		return
	}

	// Determine output filename
	outputFilename := resolveOutputFilename(reqBody.Keys, "image")

	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, outputFilename))
	w.Header().Set("Content-Type", "application/dicom")
	http.ServeFile(w, r, outputFilePath)
}

// resolveOutputFilename extracts a filename from AccessionNumber key, or uses a default.
func resolveOutputFilename(keys []string, prefix string) string {
	for _, k := range keys {
		const accPrefix = "AccessionNumber="
		if strings.HasPrefix(k, accPrefix) {
			accNum := k[len(accPrefix):]
			if accNum != "" {
				return service.SanitizeFilename(accNum + ".dcm")
			}
		}
	}
	return prefix + "_output.dcm"
}
