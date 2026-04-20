package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"go-dcm/service"
)

type Pdf2DcmRequest struct {
	FileType         string   `json:"filetype,omitempty"`           // --filetype-pdf (default), --filetype-cda, etc
	Title            string   `json:"title,omitempty"`              // --title
	ConceptNameCSD   string   `json:"concept_name_csd,omitempty"`   // --concept-name CSD
	ConceptNameCV    string   `json:"concept_name_cv,omitempty"`    // --concept-name CV
	ConceptNameCM    string   `json:"concept_name_cm,omitempty"`    // --concept-name CM
	PatientName      string   `json:"patient_name,omitempty"`       // --patient-name
	PatientId        string   `json:"patient_id,omitempty"`         // --patient-id
	PatientBirthdate string   `json:"patient_birthdate,omitempty"`  // --patient-birthdate
	PatientSex       string   `json:"patient_sex,omitempty"`        // --patient-sex
	Manufacturer     string   `json:"manufacturer,omitempty"`       // --manufacturer
	ManufacturerModel string  `json:"manufacturer_model,omitempty"` // --manufacturer-model
	DeviceSerial     string   `json:"device_serial,omitempty"`      // --device-serial
	SoftwareVersions string   `json:"software_versions,omitempty"`  // --software-versions
	GenerateUIDs     *bool    `json:"generate_uids,omitempty"`      // --generate (default true)
	StudyFrom        string   `json:"study_from,omitempty"`         // --study-from
	SeriesFrom       string   `json:"series_from,omitempty"`        // --series-from
	InstanceOne      bool     `json:"instance_one,omitempty"`       // --instance-one
	InstanceInc      bool     `json:"instance_inc,omitempty"`       // --instance-inc
	InstanceSet      int      `json:"instance_set,omitempty"`       // --instance-set
	AnnotationNo     bool     `json:"annotation_no,omitempty"`      // --annotation-no
	Override         bool     `json:"override,omitempty"`           // --override
	Keys             []string `json:"keys,omitempty"`               // --key
}

func (req *Pdf2DcmRequest) ToArgs() []string {
	var args []string

	// Filetype
	if req.FileType == "" {
		args = append(args, "--filetype-pdf") // default
	} else {
		args = append(args, "--filetype-"+req.FileType)
	}

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
	if req.Manufacturer != "" {
		args = append(args, "--manufacturer", req.Manufacturer)
	}
	if req.ManufacturerModel != "" {
		args = append(args, "--manufacturer-model", req.ManufacturerModel)
	}
	if req.DeviceSerial != "" {
		args = append(args, "--device-serial", req.DeviceSerial)
	}
	if req.SoftwareVersions != "" {
		args = append(args, "--software-versions", req.SoftwareVersions)
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
		args = append(args, "--instance-set", strconv.Itoa(req.InstanceSet))
	}
	if req.AnnotationNo {
		args = append(args, "--annotation-no")
	}
	if req.Override {
		args = append(args, "--override")
	}
	for _, key := range req.Keys {
		args = append(args, "--key", key)
	}

	return args
}

func HandlePdf2Dcm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(20 << 20); err != nil { // 20 MB max
		http.Error(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Missing 'file' field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	paramsStr := r.FormValue("parameters")
	var reqBody Pdf2DcmRequest
	if paramsStr != "" {
		if err := json.Unmarshal([]byte(paramsStr), &reqBody); err != nil {
			http.Error(w, "Invalid parameters JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	tempDir, err := os.MkdirTemp("", "pdf2dcm_*")
	if err != nil {
		http.Error(w, "Error creating temp dir", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tempDir)

	inputFilePath := filepath.Join(tempDir, fileHeader.Filename)
	out, err := os.Create(inputFilePath)
	if err != nil {
		http.Error(w, "Error saving file", http.StatusInternalServerError)
		return
	}
	io.Copy(out, file)
	out.Close()

	outputFilePath := filepath.Join(tempDir, "output.dcm")

	// Notice that pdf2dcm behavior is covered by dcmencap in DCMTK
	args := reqBody.ToArgs()
	if err := service.RunDCMTK("dcmencap", inputFilePath, outputFilePath, args); err != nil {
		http.Error(w, fmt.Sprintf("Conversion failed: %v", err), http.StatusInternalServerError)
		return
	}

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
