# go-dcm — DICOM Conversion REST API

A production-grade Go REST API for converting images (JPEG, PNG, BMP), PDFs, CDA documents, and STL 3D models into standards-compliant DICOM (.dcm) files using [DCMTK](https://dicom.offis.de/dcmtk.php.en).

## Features

- **Image → DICOM**: Convert JPEG, BMP, and PNG images to Secondary Capture, VL Photographic, or Ophthalmic Photography SOP classes
- **PDF → DICOM**: Encapsulate PDF documents as Encapsulated PDF Storage DICOM objects
- **CDA → DICOM**: Encapsulate Clinical Document Architecture (CDA) XML files
- **STL → DICOM**: Encapsulate 3D STL models as Encapsulated STL Storage
- **PNG Auto-Conversion**: PNG files are automatically converted to lossless BMP before DICOM conversion (zero quality loss)
- **DICOM Compliance**: Auto-injects mandatory tags (Modality, StudyDate, ContentDate) if not provided
- **Orthanc Compatible**: Generated DICOM files validated against Orthanc PACS and standard DICOM viewers
- **Structured Logging**: JSON-formatted logs via `log/slog`
- **Health Checks**: `/health` endpoint with DCMTK dependency status
- **Docker Ready**: Multi-stage Dockerfile with non-root user

## Prerequisites

### System Dependencies

| Dependency | Version | Purpose |
|---|---|---|
| **Go** | 1.24+ | Compiles and runs the API server |
| **DCMTK** | 3.6.7+ | Provides `img2dcm`, `pdf2dcm`, `cda2dcm`, `stl2dcm` CLI tools |

### Install DCMTK

**Debian/Ubuntu:**
```bash
sudo apt-get install -y dcmtk
```

**macOS:**
```bash
brew install dcmtk
```

**Verify installation:**
```bash
img2dcm --version
pdf2dcm --version
```

## Quick Start

### Run Locally

```bash
# Install dependencies
go mod tidy

# Start server
go run main.go
```

The server starts at `http://localhost:8080`.

### Run with Docker

```bash
# Build and start
docker compose up -d

# Check health
curl http://localhost:8080/health
```

### Run with Docker (go-dcm only)

```bash
docker build -t go-dcm .
docker run -p 8080:8080 go-dcm
```

## Configuration

All configuration is via environment variables:

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP server port |
| `MAX_IMAGE_UPLOAD_MB` | `50` | Maximum image upload size in MB |
| `MAX_PDF_UPLOAD_MB` | `100` | Maximum PDF upload size in MB |
| `MAX_CDA_UPLOAD_MB` | `50` | Maximum CDA upload size in MB |
| `MAX_STL_UPLOAD_MB` | `100` | Maximum STL upload size in MB |

## API Reference

### Health Check

```
GET /health
GET /api/v1/health
```

**Response:**
```json
{
  "status": "healthy",
  "version": "dev",
  "dependencies": {
    "img2dcm": "available",
    "pdf2dcm": "available",
    "cda2dcm": "available",
    "stl2dcm": "available",
    "dcmdump": "available",
    "go_version": "go1.25.0"
  }
}
```

---

### Convert Image to DICOM

```
POST /api/v1/convert/img2dcm
Content-Type: multipart/form-data
```

**Form Fields:**
| Field | Type | Required | Description |
|---|---|---|---|
| `file` | file | ✅ | JPEG, BMP, or PNG image file |
| `parameters` | text/json | ❌ | Conversion parameters (see below) |

**Parameters JSON:**
```json
{
  "output_sop_class": "sec-capture",
  "keys": [
    "PatientName=Doe^John",
    "PatientID=123456",
    "PatientBirthDate=19900101",
    "PatientSex=M",
    "Modality=XC",
    "StudyDate=20260509",
    "AccessionNumber=ACC001",
    "StudyInstanceUID=1.2.3.4.5",
    "SeriesInstanceUID=1.2.3.4.5.1"
  ]
}
```

**SOP Class Options:** `sec-capture` (default), `new-sc`, `vl-photo`, `oph-photo`

**Example with curl:**
```bash
curl -o output.dcm \
  -F "file=@photo.jpg" \
  -F 'parameters={"output_sop_class":"sec-capture","keys":["PatientName=Doe^John","PatientID=12345","Modality=XC","AccessionNumber=ACC001"]}' \
  http://localhost:8080/api/v1/convert/img2dcm
```

**Auto-injected tags** (if not provided):
- `Modality=OT` (or `XC` for vl-photo, `OP` for oph-photo)
- `StudyDate=<today>`
- `ContentDate=<today>`

---

### Convert PDF to DICOM

```
POST /api/v1/convert/pdf2dcm
Content-Type: multipart/form-data
```

**Form Fields:**
| Field | Type | Required | Description |
|---|---|---|---|
| `file` | file | ✅ | PDF document |
| `parameters` | text/json | ❌ | Conversion parameters |

**Parameters JSON:**
```json
{
  "title": "Lab Report",
  "patient_name": "Doe^John",
  "patient_id": "123456",
  "patient_birthdate": "19900101",
  "patient_sex": "M",
  "generate_uids": true,
  "annotation_no": true,
  "keys": [
    "AccessionNumber=ACC002",
    "Manufacturer=Hospital_Name",
    "ManufacturerModelName=SIMRS"
  ]
}
```

**Example with curl:**
```bash
curl -o report.dcm \
  -F "file=@report.pdf" \
  -F 'parameters={"title":"Lab Report","patient_name":"Doe^John","patient_id":"12345","keys":["AccessionNumber=ACC002","Manufacturer=RS_PRIMA"]}' \
  http://localhost:8080/api/v1/convert/pdf2dcm
```

---

### Convert CDA to DICOM

```
POST /api/v1/convert/cda2dcm
Content-Type: multipart/form-data
```

**Form Fields:**
| Field | Type | Required | Description |
|---|---|---|---|
| `file` | file | ✅ | CDA/XML document |
| `parameters` | text/json | ❌ | Conversion parameters (same structure as pdf2dcm) |

---

### Convert STL to DICOM

```
POST /api/v1/convert/stl2dcm
Content-Type: multipart/form-data
```

**Form Fields:**
| Field | Type | Required | Description |
|---|---|---|---|
| `file` | file | ✅ | STL 3D model file |
| `parameters` | text/json | ❌ | Conversion parameters (same structure as pdf2dcm) |

---

### Error Responses

All errors return structured JSON:

```json
{
  "error": "unsupported file extension '.gif'",
  "code": "INVALID_FILE_TYPE",
  "details": "Supported formats: JPEG, BMP, PNG"
}
```

**Error Codes:**
| Code | HTTP Status | Description |
|---|---|---|
| `INVALID_FORM` | 400 | Malformed multipart form |
| `MISSING_FILE` | 400 | No file uploaded |
| `INVALID_FILE_TYPE` | 400 | Unsupported file extension |
| `INVALID_PARAMS` | 400 | Malformed parameters JSON |
| `INVALID_KEY_FORMAT` | 400 | Invalid DICOM tag key format |
| `INVALID_PDF` | 400 | File content is not a valid PDF |
| `PNG_CONVERSION_ERROR` | 400 | Failed to convert PNG to BMP |
| `CONVERSION_FAILED` | 500 | DCMTK conversion failed |
| `TEMP_DIR_ERROR` | 500 | Cannot create temp directory |
| `FILE_SAVE_ERROR` | 500 | Cannot save uploaded file |

## DICOM Compliance

### Generated SOP Classes

| Input | SOP Class | SOP Class UID |
|---|---|---|
| JPEG/BMP/PNG | Secondary Capture Image Storage | 1.2.840.10008.5.1.4.1.1.7 |
| JPEG/BMP/PNG (vl-photo) | VL Photographic Image Storage | 1.2.840.10008.5.1.4.1.1.77.1.4 |
| PDF | Encapsulated PDF Storage | 1.2.840.10008.5.1.4.1.1.104.1 |
| CDA | Encapsulated CDA Storage | 1.2.840.10008.5.1.4.1.1.104.2 |
| STL | Encapsulated STL Storage | 1.2.840.10008.5.1.4.1.1.104.3 |

### Mandatory Tags Included

All generated DICOM files include:
- Patient Module: PatientName, PatientID, PatientBirthDate, PatientSex
- Study Module: StudyInstanceUID, StudyDate, AccessionNumber
- Series Module: SeriesInstanceUID, Modality
- SOP Common: SOPClassUID, SOPInstanceUID
- Transfer Syntax in File Meta
- Image-specific: Rows, Columns, BitsAllocated, PhotometricInterpretation, PixelData
- PDF-specific: EncapsulatedDocument, MIMETypeOfEncapsulatedDocument, BurnedInAnnotation

## Testing

### Unit Tests

```bash
go test ./... -v -race -cover
```

### DICOM Validation with pydicom

```bash
# Validate a single file
python3 tests/validate_dicom.py output.dcm

# Test running server end-to-end
python3 tests/validate_dicom.py --test-server http://localhost:8080
```

### Validate with DCMTK

```bash
dcmdump output.dcm
```

## Project Structure

```
go-dcm/
├── main.go                     # Entry point, graceful shutdown, config
├── go.mod / go.sum             # Go module dependencies
├── Dockerfile                  # Multi-stage Docker build
├── docker-compose.yml          # go-dcm + Orthanc
├── openapi.yaml                # OpenAPI 3.0 specification
├── handler/
│   ├── img2dcm.go              # Image → DICOM endpoint
│   ├── pdf2dcm.go              # PDF → DICOM endpoint
│   ├── cda2dcm.go              # CDA → DICOM endpoint
│   ├── stl2dcm.go              # STL → DICOM endpoint
│   ├── health.go               # Health check endpoint
│   ├── img2dcm_test.go         # Image handler tests
│   └── pdf2dcm_test.go         # PDF handler tests
├── service/
│   ├── dcmtk_runner.go         # DCMTK command executor
│   ├── image_converter.go      # PNG → BMP converter
│   ├── validator.go            # Input validation
│   ├── dcmtk_runner_test.go    # Runner tests
│   └── validator_test.go       # Validator tests
├── model/
│   └── response.go             # JSON response types
├── router/
│   └── router.go               # Chi router + middleware
├── tests/
│   └── validate_dicom.py       # pydicom validation script
└── .github/workflows/
    └── ci.yml                  # GitHub Actions CI
```

## License

MIT License — See [LICENSE](LICENSE) for details.

## Credits

Original implementation by **Jaisyullah Rafiul Islam** for the **Transformation and Digitalization Team, Ministry of Health Indonesia**.

Refactored and hardened by the go-dcm community.
