package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"go-dcm/service"
)

type Img2DcmRequest struct {
	InputFormat      string   `json:"input_format,omitempty"`      // --input-format (JPEG, BMP)
	DatasetFrom      string   `json:"dataset_from,omitempty"`      // --dataset-from
	StudyFrom        string   `json:"study_from,omitempty"`        // --study-from
	SeriesFrom       string   `json:"series_from,omitempty"`       // --series-from
	InstanceInc      bool     `json:"instance_inc,omitempty"`      // --instance-inc
	DisableProgr     bool     `json:"disable_progr,omitempty"`     // --disable-progr
	DisableExt       bool     `json:"disable_ext,omitempty"`       // --disable-ext
	InsistOnJfif     bool     `json:"insist_on_jfif,omitempty"`    // --insist-on-jfif
	KeepAppn         bool     `json:"keep_appn,omitempty"`         // --keep-appn
	RemoveCom        bool     `json:"remove_com,omitempty"`        // --remove-com
	NoChecks         bool     `json:"no_checks,omitempty"`         // --no-checks
	NoType2Insert    bool     `json:"no_type2_insert,omitempty"`   // --no-type2-insert
	NoType1Invent    bool     `json:"no_type1_invent,omitempty"`   // --no-type1-invent
	Transliterate    bool     `json:"transliterate,omitempty"`     // --transliterate
	DiscardIllegal   bool     `json:"discard_illegal,omitempty"`   // --discard-illegal
	Keys             []string `json:"keys,omitempty"`              // --key
	OutputSopClass   string   `json:"output_sop_class,omitempty"`  // --sec-capture, --new-sc, --vl-photo, --oph-photo
	WriteDataset     bool     `json:"write_dataset,omitempty"`     // --write-dataset
	GroupLengthRemove bool    `json:"group_length_remove,omitempty"` // --group-length-remove
	GroupLengthCreate bool    `json:"group_length_create,omitempty"` // --group-length-create
	LengthUndefined  bool     `json:"length_undefined,omitempty"`  // --length-undefined
	PaddingOff       bool     `json:"padding_off,omitempty"`       // --padding-off
}

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

func HandleImg2Dcm(w http.ResponseWriter, r *http.Request) {
	// Parse Multipart form
	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10 MB max
		http.Error(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	// 1. Get the file
	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Missing 'file' field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Parse parameters JSON (if any)
	paramsStr := r.FormValue("parameters")
	var reqBody Img2DcmRequest
	if paramsStr != "" {
		if err := json.Unmarshal([]byte(paramsStr), &reqBody); err != nil {
			http.Error(w, "Invalid parameters JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	// Setup Temp Directory
	tempDir, err := os.MkdirTemp("", "img2dcm_*")
	if err != nil {
		http.Error(w, "Error creating temp dir", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tempDir) // clean up

	// Save input file
	inputFilePath := filepath.Join(tempDir, fileHeader.Filename)
	out, err := os.Create(inputFilePath)
	if err != nil {
		http.Error(w, "Error saving file", http.StatusInternalServerError)
		return
	}
	io.Copy(out, file)
	out.Close()

	// Define output file path
	outputFilePath := filepath.Join(tempDir, "output.dcm")

	// Call DCMTK Service
	args := reqBody.ToArgs()
	if err := service.RunDCMTK("img2dcm", inputFilePath, outputFilePath, args); err != nil {
		http.Error(w, fmt.Sprintf("Conversion failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Output resulting file
	// Determine output filename from AccessionNumber if provided in keys
	outputFilename := "output.dcm"
	for _, k := range reqBody.Keys {
		// Keys usually match "AccessionNumber=VALUE"
		const prefix = "AccessionNumber="
		if len(k) > len(prefix) && k[:len(prefix)] == prefix {
			outputFilename = k[len(prefix):] + ".dcm"
		}
	}

	w.Header().Set("Content-Disposition", "attachment; filename=\""+outputFilename+"\"")
	w.Header().Set("Content-Type", "application/dicom")
	http.ServeFile(w, r, outputFilePath)
}
