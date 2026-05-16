# go-dcm — DICOM Conversion & Orthanc Integration REST API

[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![DCMTK](https://img.shields.io/badge/DCMTK-3.6.7+-orange)](https://dicom.offis.de/dcmtk.php.en)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A production-grade Go REST API for converting images (JPEG, PNG, BMP), PDFs, CDA documents, and STL 3D models into standards-compliant DICOM (.dcm) files using [DCMTK](https://dicom.offis.de/dcmtk.php.en) — with optional direct upload and tag modification via [Orthanc](https://www.orthanc-server.com/) PACS.

---

## Table of Contents

- [Features](#features)
- [Architecture](#architecture)
- [Prerequisites](#prerequisites)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [API Reference](#api-reference)
  - [Health Check](#health-check)
  - [Convert Image to DICOM](#convert-image-to-dicom)
  - [Convert PDF to DICOM](#convert-pdf-to-dicom)
  - [Convert CDA to DICOM](#convert-cda-to-dicom)
  - [Convert STL to DICOM](#convert-stl-to-dicom)
  - [Convert & Send to Orthanc](#convert--send-to-orthanc)
  - [Error Responses](#error-responses)
- [DICOM Compliance](#dicom-compliance)
- [Testing](#testing)
- [Project Structure](#project-structure)
- [License](#license)
- [Credits](#credits)

---

## Features

| Feature | Description |
|---|---|
| **Image → DICOM** | JPEG, BMP, PNG to Secondary Capture, VL Photographic, or Ophthalmic Photography SOP classes |
| **PDF → DICOM** | Encapsulated PDF Storage DICOM objects |
| **CDA → DICOM** | Encapsulated CDA XML documents |
| **STL → DICOM** | Encapsulated 3D STL models |
| **Send to Orthanc** | Convert & push to Orthanc with tag modification in a single API call |
| **PNG Auto-Conversion** | PNG → lossless BMP before DICOM conversion (zero quality loss) |
| **DICOM Compliance** | Auto-injects mandatory tags (Modality, StudyDate, ContentDate) |
| **Tag Modification** | Modify patient/study-level DICOM tags via Orthanc's REST API |
| **Rollback on Failure** | Auto-deletes uploaded instance from Orthanc if tag modification fails |
| **Structured Logging** | JSON-formatted logs via `log/slog` |
| **Health Checks** | `/health` with DCMTK + Orthanc connectivity status |
| **Docker Ready** | Multi-stage Dockerfile with non-root user |

---

## Architecture

```
                                ┌─────────────────────────────────────────┐
                                │              go-dcm API                 │
                                │                                         │
  ┌──────────┐   POST           │  ┌──────────┐    ┌──────────────────┐   │     ┌──────────┐
  │  Client   │ ──────────────► │  │ Handlers │───►│ DCMTK (img2dcm, │   │     │          │
  │ (curl,    │   multipart     │  │          │    │ pdf2dcm, cda2dcm,│   │     │  Orthanc │
  │  app,     │   form-data     │  │          │    │ stl2dcm)         │   │     │   PACS   │
  │  SIMRS)   │ ◄────────────── │  │          │    └──────────────────┘   │────►│          │
  │           │   .dcm / JSON   │  │          │                           │     │          │
  └──────────┘                  │  └──────────┘                           │     └──────────┘
                                │         │                               │       ▲    │
                                │         │  /send-to-orthanc             │       │    │
                                │         └───────────────────────────────│───────┘    │
                                │              upload + modify            │   response │
                                │                                         │◄───────────┘
                                └─────────────────────────────────────────┘
```

**Two modes of operation:**

1. **Convert only** (`/api/v1/convert/*`) — Returns the `.dcm` file binary. Client handles storage.
2. **Convert & send** (`/api/v1/send-to-orthanc`) — Converts, uploads to Orthanc, modifies tags, returns JSON result. Ideal for SIMRS/HIS integration.

---

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

---

## Quick Start

### Run Locally

```bash
# Install dependencies
go mod tidy

# Start server (without Orthanc integration)
go run main.go

# Start server (with Orthanc integration)
ORTHANC_URL=http://localhost ORTHANC_PORT=8042 ORTHANC_USER=admin ORTHANC_PASS=admin go run main.go
```

The server starts at `http://localhost:8080`.

### Run with Docker Compose

```bash
# Build and start (includes Orthanc for testing)
docker compose up -d

# Check health
curl http://localhost:8080/health

# Orthanc Web UI available at http://localhost:8042 (admin/admin)
```

### Run Standalone (Docker)

```bash
docker build -t go-dcm .
docker run -p 8080:8080 \
  -e ORTHANC_URL=http://your-orthanc-server \
  -e ORTHANC_PORT=8042 \
  -e ORTHANC_USER=your_user \
  -e ORTHANC_PASS=your_pass \
  go-dcm
```

---

## Configuration

All configuration is via environment variables. No config files needed.

### Server Settings

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP server listen port |
| `MAX_IMAGE_UPLOAD_MB` | `50` | Maximum image upload size in MB |
| `MAX_PDF_UPLOAD_MB` | `100` | Maximum PDF upload size in MB |
| `MAX_CDA_UPLOAD_MB` | `50` | Maximum CDA upload size in MB |
| `MAX_STL_UPLOAD_MB` | `100` | Maximum STL upload size in MB |

### Orthanc Connection (Optional)

> **Note:** These variables are only required if you use the `/api/v1/send-to-orthanc` endpoint. All `/convert/*` endpoints work without Orthanc configuration.

| Variable | Default | Description |
|---|---|---|
| `ORTHANC_URL` | _(empty)_ | Orthanc server base URL (e.g. `http://192.168.1.100`) |
| `ORTHANC_PORT` | `8042` | Orthanc REST API port |
| `ORTHANC_USER` | _(empty)_ | Basic auth username (optional, omit for no auth) |
| `ORTHANC_PASS` | _(empty)_ | Basic auth password (optional, omit for no auth) |

When `ORTHANC_URL` is empty, the `/send-to-orthanc` endpoint returns `503 Service Unavailable`. All other endpoints function normally.

The `docker-compose.yml` includes a test Orthanc instance with default credentials (`admin`/`admin`). **For production, point these variables to your own Orthanc server.**

---

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
    "orthanc": "connected",
    "go_version": "go1.25.0"
  }
}
```

Orthanc status values: `connected`, `unreachable: <error>`, `not_configured`.

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

**Example:**
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

**Example:**
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

### Convert & Send to Orthanc

> **This is the recommended endpoint for SIMRS/HIS integration.** It handles conversion, upload, and tag correction in a single call — ensuring DICOM tags are always correct in Orthanc.

```
POST /api/v1/send-to-orthanc
Content-Type: multipart/form-data
```

> **Requires** `ORTHANC_URL` environment variable to be configured.

**Form Fields:**
| Field | Type | Required | Description |
|---|---|---|---|
| `file` | file | ✅ | Source file (JPEG, PNG, BMP, PDF, XML/CDA, STL) |
| `filetype` | string | ✅ | One of: `img`, `pdf`, `cda`, `stl` |
| `parameters` | text/json | ❌ | Conversion parameters (same as the matching `/convert/*` endpoint) |
| `orthanc_modify` | text/json | ✅ | Orthanc study modify payload (see below) |

**`orthanc_modify` Payload:**

This is the same payload format as Orthanc's `POST /studies/{id}/modify` API. The `Replace` field maps DICOM tag names to their desired values:

```json
{
  "Replace": {
    "PatientBirthDate": "20260531",
    "PatientID": "12738972",
    "PatientName": "Patient^Name",
    "PatientSex": "M",
    "AccessionNumber": "127397298",
    "ReferringPhysicianName": "Dr^Smith",
    "StudyID": "12739213",
    "StudyTime": "120000"
  },
  "Remove": [],
  "Keep": [],
  "KeepSource": false,
  "KeepLabels": true,
  "Force": true
}
```

| Modify Field | Type | Description |
|---|---|---|
| `Replace` | JSON object | DICOM tags to set/replace (key = tag name; value = string for simple tags, or nested JSON arrays/objects for sequences e.g. `ScheduledProcedureStepSequence`) |
| `Remove` | `[]string` | DICOM tag names to remove |
| `Keep` | `[]string` | DICOM tag names to preserve during modification |
| `KeepSource` | `bool` | `false` = replace original study, `true` = keep original + create modified copy |
| `KeepLabels` | `bool` | Preserve private tag labels |
| `Force` | `bool` | Required `true` to modify protected tags (PatientID, StudyID, etc.) |

**Example — Image to Orthanc:**
```bash
curl -X POST http://localhost:8080/api/v1/send-to-orthanc \
  -F "file=@photo.jpg" \
  -F "filetype=img" \
  -F 'parameters={"output_sop_class":"sec-capture","keys":["Modality=OT"]}' \
  -F 'orthanc_modify={"Replace":{"PatientID":"12738972","PatientName":"Doe^John","PatientSex":"M","PatientBirthDate":"19900101","AccessionNumber":"ACC001","ReferringPhysicianName":"Dr^Smith","StudyID":"STD001"},"Remove":[],"Keep":[],"KeepSource":false,"KeepLabels":true,"Force":true}'
```

**Example — PDF to Orthanc:**
```bash
curl -X POST http://localhost:8080/api/v1/send-to-orthanc \
  -F "file=@report.pdf" \
  -F "filetype=pdf" \
  -F 'parameters={"title":"Lab Report","patient_name":"Doe^John","patient_id":"12345"}' \
  -F 'orthanc_modify={"Replace":{"PatientID":"12738972","PatientName":"Doe^John","AccessionNumber":"ACC002","StudyID":"STD002"},"Remove":[],"Keep":[],"KeepSource":false,"KeepLabels":true,"Force":true}'
```

**Success Response (200):**
```json
{
  "status": "success",
  "upload": {
    "ID": "47136abb-48f4ac54-b61a8e55-0ef1136c-0295ba12",
    "ParentPatient": "da39a3ee-5e6b4b0d-3255bfef-95601890-afd80709",
    "ParentSeries": "6dded9e0-d49378b5-b3d75c8e-a27a7363-3f369811",
    "ParentStudy": "53ca7d61-9774a573-51a484d7-29aaeb5e-3a8ed40e",
    "Path": "/instances/47136abb-48f4ac54-b61a8e55-0ef1136c-0295ba12",
    "Status": "Success"
  },
  "modify": {
    "ID": "new-study-id-after-modify",
    "PatientID": "12738972",
    "Path": "/studies/new-study-id-after-modify",
    "Type": "Study"
  }
}
```

**Behavior Notes:**
- Uses **synchronous** modify — the API blocks until Orthanc completes the tag modification, so the response is always definitive (success or failure).
- **Rollback on failure** — if the upload succeeds but tag modification fails, the uploaded instance is automatically deleted from Orthanc. No orphaned data.
- `KeepSource: false` — the original study (with incorrect tags) is replaced. Set to `true` if you want to keep the original.

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
| `MISSING_FILETYPE` | 400 | Missing `filetype` parameter on `/send-to-orthanc` |
| `INVALID_FILETYPE` | 400 | Invalid `filetype` value (must be img/pdf/cda/stl) |
| `MISSING_ORTHANC_MODIFY` | 400 | Missing `orthanc_modify` payload |
| `INVALID_ORTHANC_MODIFY` | 400 | Malformed `orthanc_modify` JSON |
| `ORTHANC_NOT_CONFIGURED` | 503 | `ORTHANC_URL` env var not set |
| `ORTHANC_UPLOAD_FAILED` | 502 | Failed to upload DICOM to Orthanc |
| `ORTHANC_MODIFY_FAILED` | 502 | Failed to modify study tags (upload rolled back) |
| `CONVERSION_FAILED` | 500 | DCMTK conversion failed |
| `TEMP_DIR_ERROR` | 500 | Cannot create temp directory |
| `FILE_SAVE_ERROR` | 500 | Cannot save uploaded file |

---

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
- **Patient Module**: PatientName, PatientID, PatientBirthDate, PatientSex
- **Study Module**: StudyInstanceUID, StudyDate, AccessionNumber
- **Series Module**: SeriesInstanceUID, Modality
- **SOP Common**: SOPClassUID, SOPInstanceUID
- **Transfer Syntax** in File Meta
- **Image-specific**: Rows, Columns, BitsAllocated, PhotometricInterpretation, PixelData
- **PDF-specific**: EncapsulatedDocument, MIMETypeOfEncapsulatedDocument, BurnedInAnnotation

---

## Testing

### Unit Tests

```bash
go test ./... -v -race -cover
```

### Integration Test (with Orthanc)

```bash
# Start Orthanc
docker compose up -d orthanc

# Test send-to-orthanc endpoint
curl -X POST http://localhost:8080/api/v1/send-to-orthanc \
  -F "file=@test.jpg" \
  -F "filetype=img" \
  -F 'orthanc_modify={"Replace":{"PatientID":"TEST001","PatientName":"Test^Patient"},"KeepSource":false,"Force":true}'

# Verify in Orthanc Web UI
open http://localhost:8042  # admin/admin
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

---

## Project Structure

```
go-dcm/
├── main.go                      # Entry point, graceful shutdown, config loading
├── go.mod / go.sum              # Go module dependencies
├── Dockerfile                   # Multi-stage Docker build (non-root)
├── docker-compose.yml           # go-dcm + Orthanc (test instance)
├── openapi.yaml                 # OpenAPI 3.0 specification
├── handler/
│   ├── img2dcm.go               # Image → DICOM endpoint
│   ├── pdf2dcm.go               # PDF → DICOM endpoint
│   ├── cda2dcm.go               # CDA → DICOM endpoint
│   ├── stl2dcm.go               # STL → DICOM endpoint
│   ├── send_to_orthanc.go       # Convert & send to Orthanc endpoint
│   ├── health.go                # Health check (DCMTK + Orthanc)
│   ├── img2dcm_test.go          # Image handler tests
│   └── pdf2dcm_test.go          # PDF handler tests
├── service/
│   ├── dcmtk_runner.go          # DCMTK command executor with timeout
│   ├── orthanc_client.go        # Orthanc REST client (upload, modify, delete, ping)
│   ├── image_converter.go       # PNG → BMP converter
│   ├── validator.go             # Input validation (extensions, MIME, keys)
│   ├── dcmtk_runner_test.go     # Runner tests
│   └── validator_test.go        # Validator tests
├── model/
│   └── response.go              # JSON response types
├── router/
│   └── router.go                # Chi router + middleware + CORS
├── tests/
│   └── validate_dicom.py        # pydicom validation script
└── .github/workflows/
    └── ci.yml                   # GitHub Actions CI
```

---

## License

MIT License — See [LICENSE](LICENSE) for details.

## Credits

Original implementation by **Jaisyullah Rafiul Islam** for the **Transformation and Digitalization Team, Ministry of Health Indonesia**.

Refactored and hardened by the go-dcm community.
