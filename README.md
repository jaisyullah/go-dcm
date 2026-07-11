# dicom-converter-api — DICOM Conversion & Orthanc Integration REST API

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![DCMTK](https://img.shields.io/badge/DCMTK-3.6.7+-orange)](https://dicom.offis.de/dcmtk.php.en)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A production-grade Go REST API for converting images (JPEG, PNG, BMP), PDFs, CDA documents, and STL 3D models into standards-compliant DICOM (.dcm) files using [DCMTK](https://dicom.offis.de/dcmtk.php.en) — with optional direct upload and tag modification via [Orthanc](https://www.orthanc-server.com/) PACS.

---

## Table of Contents

- [dicom-converter-api — DICOM Conversion \& Orthanc Integration REST API](#dicom-converter-api--dicom-conversion--orthanc-integration-rest-api)
  - [Table of Contents](#table-of-contents)
  - [Features](#features)
  - [Architecture](#architecture)
  - [Prerequisites](#prerequisites)
    - [System Dependencies](#system-dependencies)
    - [Install DCMTK](#install-dcmtk)
  - [Quick Start](#quick-start)
    - [Run Locally](#run-locally)
    - [Run with Docker Compose](#run-with-docker-compose)
    - [Run Standalone (Docker)](#run-standalone-docker)
  - [Configuration](#configuration)
    - [Server Settings](#server-settings)
    - [Orthanc Connection (Optional)](#orthanc-connection-optional)
  - [API Reference](#api-reference)
    - [Health Check](#health-check)
    - [Convert Image to DICOM](#convert-image-to-dicom)
    - [Convert PDF to DICOM](#convert-pdf-to-dicom)
    - [Convert CDA to DICOM](#convert-cda-to-dicom)
    - [Convert STL to DICOM](#convert-stl-to-dicom)
    - [Convert \& Send to Orthanc (Async)](#convert--send-to-orthanc-async)
    - [Convert \& Send from URLs (Async)](#convert--send-from-urls-async)
    - [Poll Job Status](#poll-job-status)
    - [`orthanc_modify` Payload](#orthanc_modify-payload)
    - [Modify Study Tags](#modify-study-tags)
    - [Find Study by Accession Number](#find-study-by-accession-number)
    - [Find Patient Studies](#find-patient-studies)
    - [Send Study to Modality](#send-study-to-modality)
    - [Orchestrate Upload and Send (Composite)](#orchestrate-upload-and-send-composite)
  - [Error Responses](#error-responses)
  - [DICOM Compliance](#dicom-compliance)
    - [Generated SOP Classes](#generated-sop-classes)
    - [Mandatory Tags Included](#mandatory-tags-included)
  - [Self-Recovery \& Reliability](#self-recovery--reliability)
    - [1. Transient Error Self-Healing (Exponential Backoff Retries)](#1-transient-error-self-healing-exponential-backoff-retries)
    - [2. Patient Demographic Mismatch Auto-Alignment](#2-patient-demographic-mismatch-auto-alignment)
  - [Testing](#testing)
    - [Unit Tests](#unit-tests)
    - [Integration Test (with Orthanc)](#integration-test-with-orthanc)
    - [DICOM Validation with pydicom](#dicom-validation-with-pydicom)
    - [Validate with DCMTK](#validate-with-dcmtk)
  - [Project Structure](#project-structure)
  - [License](#license)
  - [Credits](#credits)

---

## Features

| Feature                     | Description                                                                                    |
| --------------------------- | ---------------------------------------------------------------------------------------------- |
| **Image → DICOM**           | JPEG, BMP, PNG to Secondary Capture, VL Photographic, or Ophthalmic Photography SOP classes    |
| **PDF → DICOM**             | Encapsulated PDF Storage DICOM objects                                                         |
| **CDA → DICOM**             | Encapsulated CDA XML documents                                                                 |
| **STL → DICOM**             | Encapsulated 3D STL models                                                                     |
| **Send to Orthanc**         | **Asynchronous** conversion & push to Orthanc with tag modification                            |
| **Send from URLs**          | Async download from URLs, convert, and push to Orthanc                                         |
| **Async Job Queue**         | Bounded concurrency via worker pool (sized to CPU cores) to prevent OOM and timeouts           |
| **PNG Auto-Conversion**     | PNG → lossless BMP before DICOM conversion (zero quality loss)                                 |
| **DICOM Compliance**        | Auto-injects mandatory tags (Modality, StudyDate, ContentDate)                                 |
| **Tag Modification**        | Modify patient/study-level DICOM tags via Orthanc's REST API                                   |
| **Rollback on Failure**     | Auto-deletes uploaded instance from Orthanc if tag modification fails                          |
| **Self-Recovery & Retries** | Automatic backoff retry on transient storage errors + auto-alignment on demographic mismatches |
| **Structured Logging**      | JSON-formatted logs via `log/slog`                                                             |
| **Health Checks**           | `/health` with DCMTK + Orthanc connectivity status                                             |
| **Docker Ready**            | Multi-stage Dockerfile with non-root user                                                      |

---

## Architecture

```
                                ┌─────────────────────────────────────────┐
                                │              dicom-converter-api API    │
                                │                                         │
  ┌──────────┐   POST           │  ┌──────────┐    ┌──────────────────┐   │     ┌──────────┐
  │  Client  │ ──────────────►  │  │ Handlers │───►│  Worker Pool     │   │     │          │
  │ (curl,   │   multipart      │  │ (Async)  │    │ (Bounded Concur.)│───┼────►│  Orthanc │
  │  app,    │   form-data      │  │          │    └──────────────────┘   │     │   PACS   │
  │  SIMRS)  │ ◄──────────────  │  │          │             │             │     │          │
  └──────────┘   202 Accepted   │  └──────────┘             ▼             │     └──────────┘
       │          (job_id)      │         │        ┌──────────────────┐   │       ▲    │
       │                        │         │        │ DCMTK (img2dcm,  │   │       │    │
       │      GET /jobs/{id}    │         └───────►│ pdf2dcm, etc.)   │   │       │    │
       └────────────────────────┼────────────────► └──────────────────┘   │───────┘    │
              (Polling)         │              upload + modify            │   response │
                                │                                         │◄───────────┘
                                └─────────────────────────────────────────┘
```

**Two modes of operation:**

1. **Convert only** (`/api/v1/convert/*`) — **Synchronous**. Returns the `.dcm` file binary. Client handles storage.
2. **Convert & send** (`/api/v1/send-to-orthanc`, `/api/v1/send-to-orthanc-from-urls`, `/api/v1/orchestrate/upload-and-send`) — **Asynchronous**. Returns a `job_id` immediately. A background worker handles the heavy lifting. Ideal for SIMRS/HIS integration to prevent UI timeouts.

---

## Prerequisites

### System Dependencies

| Dependency | Version | Purpose                                                       |
| ---------- | ------- | ------------------------------------------------------------- |
| **Go**     | 1.25+   | Compiles and runs the API server                              |
| **DCMTK**  | 3.6.7+  | Provides `img2dcm`, `pdf2dcm`, `cda2dcm`, `stl2dcm` CLI tools |

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
ORTHANC_URL=http://localhost ORTHANC_PORT=8042 ORTHANC_USER=orthanc ORTHANC_PASS=orthanc go run main.go
```

The server starts at `http://localhost:8080`.

### Run with Docker Compose

```bash
# Build and start (includes Orthanc for testing)
docker compose up -d

# Check health
curl http://localhost:8080/health

# Orthanc Web UI available at http://localhost:8042 (orthanc/orthanc)
```

### Run Standalone (Docker)

```bash
docker build -t dicom-converter-api .
docker run -p 8080:8080 \
  -e ORTHANC_URL=http://your-orthanc-server \
  -e ORTHANC_PORT=8042 \
  -e ORTHANC_USER=your_user \
  -e ORTHANC_PASS=your_pass \
  dicom-converter-api
```

---

## Configuration

All configuration is via environment variables. No config files needed.

### Server Settings

| Variable              | Default | Description                     |
| --------------------- | ------- | ------------------------------- |
| `PORT`                | `8080`  | HTTP server listen port         |
| `MAX_IMAGE_UPLOAD_MB` | `50`    | Maximum image upload size in MB |
| `MAX_PDF_UPLOAD_MB`   | `100`   | Maximum PDF upload size in MB   |
| `MAX_CDA_UPLOAD_MB`   | `50`    | Maximum CDA upload size in MB   |
| `MAX_STL_UPLOAD_MB`   | `100`   | Maximum STL upload size in MB   |

### Orthanc Connection (Optional)

> **Note:** These variables are only required if you use the `/api/v1/send-to-orthanc`, `/api/v1/send-to-orthanc-from-urls`, or `/api/v1/orchestrate/upload-and-send` endpoints. All `/convert/*` endpoints work without Orthanc configuration.

| Variable       | Default   | Description                                           |
| -------------- | --------- | ----------------------------------------------------- |
| `ORTHANC_URL`  | _(empty)_ | Orthanc server base URL (e.g. `http://192.168.1.100`) |
| `ORTHANC_PORT` | `8042`    | Orthanc REST API port                                 |
| `ORTHANC_USER` | _(empty)_ | Basic auth username (optional, omit for no auth)      |
| `ORTHANC_PASS` | _(empty)_ | Basic auth password (optional, omit for no auth)      |

When `ORTHANC_URL` is empty, the Orthanc-dependent endpoints return `503 Service Unavailable`. All other endpoints function normally.

The `docker-compose.yml` includes a test Orthanc instance with default credentials (`orthanc`/`orthanc`). **For production, point these variables to your own Orthanc server.**

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
| Field        | Type      | Required | Description                       |
| ------------ | --------- | -------- | --------------------------------- |
| `file`       | file      | ✅        | JPEG, BMP, or PNG image file      |
| `parameters` | text/json | ❌        | Conversion parameters (see below) |

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
| Field        | Type      | Required | Description           |
| ------------ | --------- | -------- | --------------------- |
| `file`       | file      | ✅        | PDF document          |
| `parameters` | text/json | ❌        | Conversion parameters |

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

> **Note:** `generate_uids` must be explicitly set to `true` if you want DCMTK to auto-generate study/series UIDs. The default behavior (when omitted) does not auto-generate UIDs.

**Example:**
```bash
curl -o report.dcm \
  -F "file=@report.pdf" \
  -F 'parameters={"title":"Lab Report","patient_name":"Doe^John","patient_id":"12345","generate_uids":true,"keys":["AccessionNumber=ACC002","Manufacturer=RS_PRIMA"]}' \
  http://localhost:8080/api/v1/convert/pdf2dcm
```

---

### Convert CDA to DICOM

```
POST /api/v1/convert/cda2dcm
Content-Type: multipart/form-data
```

**Form Fields:**
| Field        | Type      | Required | Description              |
| ------------ | --------- | -------- | ------------------------ |
| `file`       | file      | ✅        | CDA/XML or HTML document |
| `parameters` | text/json | ❌        | Conversion parameters    |

**Parameters JSON** (same parameter structure as PDF, with the same fields except `annotation_yes`):
```json
{
  "title": "Clinical Document",
  "patient_name": "Doe^John",
  "patient_id": "123456",
  "patient_birthdate": "19900101",
  "patient_sex": "M",
  "generate_uids": true,
  "annotation_no": true,
  "keys": [
    "AccessionNumber=ACC003"
  ]
}
```

> **Note:** CDA and STL converters do not support the `annotation_yes` flag. Only `annotation_no` is available.

---

### Convert STL to DICOM

```
POST /api/v1/convert/stl2dcm
Content-Type: multipart/form-data
```

**Form Fields:**
| Field        | Type      | Required | Description                                                    |
| ------------ | --------- | -------- | -------------------------------------------------------------- |
| `file`       | file      | ✅        | STL 3D model file                                              |
| `parameters` | text/json | ❌        | Conversion parameters (same structure as CDA — see note above) |

---

### Convert & Send to Orthanc (Async)

> **This is the recommended endpoint for SIMRS/HIS integration.** It handles conversion, upload, and tag correction in the background — ensuring the UI remains responsive and DICOM tags are eventually synced in Orthanc.

```
POST /api/v1/send-to-orthanc
Content-Type: multipart/form-data
```

> **Requires** `ORTHANC_URL` environment variable to be configured.

**Form Fields:**
| Field            | Type      | Required | Description                                                        |
| ---------------- | --------- | -------- | ------------------------------------------------------------------ |
| `file`           | file      | ✅        | Source file (JPEG, PNG, BMP, PDF, XML/CDA, STL)                    |
| `filetype`       | string    | ✅        | One of: `img`, `pdf`, `cda`, `stl`                                 |
| `parameters`     | text/json | ❌        | Conversion parameters (same as the matching `/convert/*` endpoint) |
| `orthanc_modify` | text/json | ✅        | Orthanc study modify payload (see below)                           |

**Success Response (202 Accepted):**
```json
{
  "status": "success",
  "job_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

**Workflow:**
1. API returns `202 Accepted` immediately with a `job_id`.
2. The heavy work (conversion + upload + modify) starts in a background worker.
3. Client polls the status using the `/jobs/{id}` endpoint.

---

### Convert & Send from URLs (Async)

> Download files from remote URLs, convert each to DICOM, upload to Orthanc, and modify study tags — all asynchronously. Supports multiple URLs in a single job (batch upload).

```
POST /api/v1/send-to-orthanc-from-urls
Content-Type: application/json
```

> **Requires** `ORTHANC_URL` environment variable to be configured.

**Request Body:**
```json
{
  "filetype": "img",
  "urls": [
    "https://example.com/image1.jpg",
    "https://example.com/image2.jpg"
  ],
  "parameters": {
    "output_sop_class": "sec-capture",
    "keys": [
      "PatientName=Doe^John",
      "PatientID=12345",
      "Modality=OT",
      "AccessionNumber=ACC001"
    ]
  },
  "orthanc_modify": {
    "Replace": {
      "PatientID": "12738972",
      "PatientName": "Doe^John",
      "PatientSex": "M",
      "PatientBirthDate": "19900101",
      "AccessionNumber": "127397298",
      "ReferringPhysicianName": "Dr^Smith",
      "StudyID": "12739213"
    },
    "Remove": [],
    "Keep": [],
    "KeepSource": false,
    "KeepLabels": true,
    "Force": true
  }
}
```

**Field Descriptions:**
| Field            | Type       | Required | Description                                                    |
| ---------------- | ---------- | -------- | -------------------------------------------------------------- |
| `filetype`       | string     | ✅        | One of: `img`, `pdf`, `cda`, `stl`                             |
| `urls`           | `[]string` | ✅        | Array of URLs to download                                      |
| `parameters`     | object     | ❌        | Conversion parameters (same as matching `/convert/*` endpoint) |
| `orthanc_modify` | object     | ✅        | Orthanc study modify payload                                   |

**Success Response (202 Accepted):**
```json
{
  "status": "success",
  "job_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

**Behavior Notes:**
- Each URL is downloaded, converted, and uploaded sequentially within the same study.
- On any failure (download, conversion, upload, or modify), all previously uploaded instances are automatically rolled back (deleted from Orthanc).
- Poll `/api/v1/jobs/{id}` for completion status.

---

### Poll Job Status

Returns the current state and result of a background conversion task.

```
GET /api/v1/jobs/{job_id}
```

**Possible Job Statuses:**
* `PENDING`: Job is in the queue.
* `PROCESSING`: Job is currently being handled by a worker.
* `COMPLETED`: Job finished successfully. Result contains Orthanc data.
* `FAILED`: Job failed. Error field contains details.

**Example Completed Response (200):**
```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "COMPLETED",
  "result": {
    "upload": { "ID": "...", "ParentStudy": "..." },
    "modify": { "ID": "...", "PatientID": "..." }
  },
  "created_at": "2026-06-03T10:00:00Z",
  "updated_at": "2026-06-03T10:00:05Z"
}
```

---

### `orthanc_modify` Payload

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

| Modify Field | Type        | Description                                                                                                                                                   |
| ------------ | ----------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Replace`    | JSON object | DICOM tags to set/replace (key = tag name; value = string for simple tags, or nested JSON arrays/objects for sequences e.g. `ScheduledProcedureStepSequence`) |
| `Remove`     | `[]string`  | DICOM tag names to remove                                                                                                                                     |
| `Keep`       | `[]string`  | DICOM tag names to preserve during modification                                                                                                               |
| `KeepSource` | `bool`      | `false` = replace original study, `true` = keep original + create modified copy                                                                               |
| `KeepLabels` | `bool`      | Preserve private tag labels                                                                                                                                   |
| `Force`      | `bool`      | Required `true` to modify protected tags (PatientID, StudyID, etc.)                                                                                           |

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

**Behavior Notes:**
- Uses **asynchronous** processing — the API returns immediately. Background workers handle the DCMTK conversion and Orthanc interaction.
- **Polling Required** — clients must poll `/api/v1/jobs/{id}` to know when the task is complete.
- **Rollback on failure** — if the upload succeeds but tag modification fails, the background worker automatically deletes the uploaded instance from Orthanc. No orphaned data.
- `KeepSource: false` — the original study (with incorrect tags) is replaced. Set to `true` if you want to keep the original.

---

### Modify Study Tags

Proxies Orthanc's `POST /studies/{id}/modify` endpoint. Useful for updating DICOM tags on an existing study already stored in Orthanc.

```
POST /api/v1/studies/{id}/modify
Content-Type: application/json
```

**Request Body:** Same `orthanc_modify` payload structure documented above.

**Example:**
```bash
curl -X POST http://localhost:8080/api/v1/studies/53ca7d61-9774a573-51a484d7-29aaeb5e-3a8ed40e/modify \
  -H "Content-Type: application/json" \
  -d '{
    "Replace": {
      "PatientName": "Updated^Name",
      "AccessionNumber": "NEWACSN"
    },
    "KeepSource": false,
    "Force": true
  }'
```

> **Note:** Unlike other handlers, this endpoint returns raw JSON on error (`{"status": "error", "message": "..."}`) instead of the standard error envelope.

---

### Find Study by Accession Number

Searches Orthanc for a study matching the given `AccessionNumber`.

```
POST /api/v1/studies/find-by-acsn
Content-Type: application/json
```

**Request Body:**
```json
{
  "accession_number": "ACC001"
}
```

**Response:**
```json
{
  "study_id": "53ca7d61-9774a573-51a484d7-29aaeb5e-3a8ed40e"
}
```

Returns `"study_id": ""` if no study is found.

---

### Find Patient Studies

Returns all studies for a given Orthanc patient ID (no date or modality filters).

```
POST /api/v1/patients/{id}/studies
```

**Response:** Raw JSON array of Orthanc study objects.

---

### Send Study to Modality

Sends a study from Orthanc to a DICOM modality (C-MOVE / C-STORE).

```
POST /api/v1/studies/{id}/send-to-modality/{ae}
```

**Response:**
```json
{
  "status": "success",
  "message": "Study sent to modality"
}
```

---

### Orchestrate Upload and Send (Composite)

> **All-in-one composite endpoint.** Downloads files from URLs (or targets an existing study), converts, uploads, modifies metadata, optionally sets AccessionNumber, sends to a DICOM modality, and optionally notifies a callback URL on completion.

```
POST /api/v1/orchestrate/upload-and-send
Content-Type: application/json
```

> **Requires** `ORTHANC_URL` environment variable to be configured.

**Request Body:**
```json
{
  "urls": [
    "https://example.com/scan.jpg"
  ],
  "study_id": "",
  "filetype": "img",
  "parameters": {
    "output_sop_class": "sec-capture",
    "keys": [
      "PatientName=Doe^John",
      "PatientID=12345",
      "Modality=OT"
    ]
  },
  "orthanc_modify": {
    "Replace": {
      "AccessionNumber": "ACC001",
      "StudyID": "STD001"
    },
    "KeepSource": false,
    "Force": true
  },
  "send_to_modality": "MODALITY_AE",
  "target_accession_number": "ACC001",
  "callback_url": "https://your-simrs.com/webhook/dicom-complete"
}
```

**Field Descriptions:**
| Field                     | Type       | Required | Description                                                                |
| ------------------------- | ---------- | -------- | -------------------------------------------------------------------------- |
| `urls`                    | `[]string` | ✅*       | URLs to download and convert. Required for new uploads.                    |
| `study_id`                | string     | ✅*       | Existing Orthanc study ID. Required when not uploading new files.          |
| `filetype`                | string     | ✅        | One of: `img`, `pdf`, `cda`, `stl`. Required when `urls` is provided.      |
| `parameters`              | object     | ❌        | Conversion parameters (same as the matching `/convert/*` endpoint).        |
| `orthanc_modify`          | object     | ✅        | Orthanc modify payload. Must have at least `Replace` or `Remove`.          |
| `send_to_modality`        | string     | ❌        | AE Title of the DICOM modality to forward the study to after modification. |
| `target_accession_number` | string     | ❌        | Sets `AccessionNumber` on the study after upload.                          |
| `callback_url`            | string     | ❌        | URL to POST the job completion result to (asynchronous notification).      |

\* Either `urls` (for new upload) or `study_id` (for existing study) must be provided.

**Phases of Execution:**
1. **Download & Convert** — Downloads each URL, converts to DICOM, uploads to Orthanc.
2. **Modify Metadata** — Applies `orthanc_modify` tags. Demographic tags (`PatientName`, `PatientID`, `PatientBirthDate`, `PatientSex`) are stripped from the modify payload because they are embedded during conversion via `--key` flags.
3. **Set AccessionNumber** — If `target_accession_number` is provided, applies it via a secondary modify call (non-fatal on failure).
4. **Send to Modality** — If `send_to_modality` is provided, forwards the study to the DICOM AE (non-fatal on failure).
5. **Callback** — If `callback_url` is provided, POSTs the final result asynchronously.

**Success Response (202 Accepted):**
```json
{
  "status": "success",
  "job_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

**Completed Job Result:**
```json
{
  "study_id": "53ca7d61-9774a573-51a484d7-29aaeb5e-3a8ed40e",
  "accession_number": "ACC001",
  "is_new_upload": true,
  "modify_result": { ... },
  "send_to_modality_ok": true,
  "upload_details": [
    { "ID": "...", "ParentStudy": "...", "Status": "Success" }
  ]
}
```

**Rollback Behavior:** If any phase fails after instances have been uploaded, all uploaded instances are automatically deleted from Orthanc.

---

## Error Responses

All errors return structured JSON:

```json
{
  "error": "unsupported file extension '.gif'",
  "code": "INVALID_FILE_TYPE",
  "details": "Supported formats: JPEG, BMP, PNG"
}
```

**Error Codes:**
| Code                     | HTTP Status | Description                                               |
| ------------------------ | ----------- | --------------------------------------------------------- |
| `INVALID_FORM`           | 400         | Malformed multipart form                                  |
| `MISSING_FILE`           | 400         | No file uploaded                                          |
| `INVALID_FILE_TYPE`      | 400         | Unsupported file extension                                |
| `INVALID_PARAMS`         | 400         | Malformed parameters JSON                                 |
| `INVALID_KEY_FORMAT`     | 400         | Invalid DICOM tag key format                              |
| `INVALID_PDF`            | 400         | File content is not a valid PDF                           |
| `PNG_CONVERSION_ERROR`   | 400         | Failed to convert PNG to BMP                              |
| `MISSING_FILETYPE`       | 400         | Missing `filetype` parameter on `/send-to-orthanc`        |
| `INVALID_FILETYPE`       | 400         | Invalid `filetype` value (must be img/pdf/cda/stl)        |
| `MISSING_ORTHANC_MODIFY` | 400         | Missing `orthanc_modify` payload                          |
| `INVALID_ORTHANC_MODIFY` | 400         | Malformed `orthanc_modify` JSON                           |
| `INVALID_JSON`           | 400         | Failed to parse JSON body                                 |
| `MISSING_URLS`           | 400         | `urls` array is empty on `/send-to-orthanc-from-urls`     |
| `MISSING_ACSN`           | 400         | `accession_number` is required on `/studies/find-by-acsn` |
| `MISSING_PATIENT_ID`     | 400         | Patient ID path parameter missing                         |
| `MISSING_PARAMS`         | 400         | Study ID or AE Title missing on send-to-modality          |
| `MISSING_TARGET`         | 400         | Neither `urls` nor `study_id` provided on orchestrate     |
| `MISSING_MODIFY`         | 400         | `orthanc_modify` missing Replace/Remove on orchestrate    |
| `JOB_NOT_FOUND`          | 404         | Job ID does not exist                                     |
| `ORTHANC_NOT_CONFIGURED` | 503         | `ORTHANC_URL` env var not set                             |
| `ORTHANC_UPLOAD_FAILED`  | 502         | Failed to upload DICOM to Orthanc                         |
| `ORTHANC_MODIFY_FAILED`  | 502         | Failed to modify study tags (upload rolled back)          |
| `ORTHANC_ERROR`          | 500         | Generic Orthanc proxy error                               |
| `CONVERSION_FAILED`      | 500         | DCMTK conversion failed                                   |
| `TEMP_DIR_ERROR`         | 500         | Cannot create temp directory                              |
| `FILE_SAVE_ERROR`        | 500         | Cannot save uploaded file                                 |
| `FILE_WRITE_ERROR`       | 500         | Cannot write uploaded file to disk                        |

> **Note:** The `/api/v1/studies/{id}/modify` endpoint returns a different error format: `{"status": "error", "message": "..."}` instead of the standard `ErrorResponse` envelope above.

---

## DICOM Compliance

### Generated SOP Classes

| Input                    | SOP Class                            | SOP Class UID                    |
| ------------------------ | ------------------------------------ | -------------------------------- |
| JPEG/BMP/PNG             | Secondary Capture Image Storage      | 1.2.840.10008.5.1.4.1.1.7        |
| JPEG/BMP/PNG (vl-photo)  | VL Photographic Image Storage        | 1.2.840.10008.5.1.4.1.1.77.1.4   |
| JPEG/BMP/PNG (oph-photo) | Ophthalmic Photography Image Storage | 1.2.840.10008.5.1.4.1.1.77.1.5.1 |
| PDF                      | Encapsulated PDF Storage             | 1.2.840.10008.5.1.4.1.1.104.1    |
| CDA                      | Encapsulated CDA Storage             | 1.2.840.10008.5.1.4.1.1.104.2    |
| STL                      | Encapsulated STL Storage             | 1.2.840.10008.5.1.4.1.1.104.3    |

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

## Self-Recovery & Reliability

To ensure zero data loss and absolute reliability in production hospital environments, `dicom-converter-api` includes automated self-recovery mechanisms:

### 1. Transient Error Self-Healing (Exponential Backoff Retries)
* **What it solves**: Transient server write failures (like Orthanc filesystem locks, full-disk scenarios, or minor network latency glitches).
* **How it works**: For both **DICOM uploads** (`POST /instances`) and **study modifications** (`POST /studies/{id}/modify`), the API automatically retries the operation up to **5 times** with exponential backoff delays (starting at `1s`, then `2s`, then `4s`, then `8s`, up to a total of **15 seconds** of patience) upon encountering status `>= 500` or TCP dial network errors.

### 2. Patient Demographic Mismatch Auto-Alignment
* **What it solves**: Orthanc rejects study modifications with `HTTP 400 Bad Request` if the target `PatientID` already exists in the database and has other studies but the new demographic tags (e.g. `PatientName`, `PatientBirthDate`, `PatientSex`) have spelling or formatting mismatches.
* **How it works**:
  1. If `/studies/{id}/modify` returns `400 Bad Request` with a demographic mismatch error, `dicom-converter-api` intercepts the error.
  2. It queries Orthanc's `/tools/find` endpoint to fetch the existing patient's main DICOM tags exactly as stored in Orthanc.
  3. It auto-aligns the modify request payload's demographics (`PatientName`, `PatientBirthDate`, `PatientSex`) to match the existing patient's demographics character-for-character.
  4. It automatically retries the modification with the aligned metadata, guaranteeing a successful mapping and database sync without manual intervention!

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
open http://localhost:8042  # orthanc/orthanc
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
dicom-converter-api/
├── main.go                      # Entry point, graceful shutdown, config loading
├── go.mod / go.sum              # Go module dependencies
├── Dockerfile                   # Multi-stage Docker build (non-root)
├── docker-compose.yml           # dicom-converter-api + Orthanc (test instance)
├── openapi.yaml                 # OpenAPI 3.0 specification
├── .github/workflows/ci.yml     # GitHub Actions CI (build, test, docker, lint)
├── handler/
│   ├── img2dcm.go               # Image → DICOM endpoint
│   ├── pdf2dcm.go               # PDF → DICOM endpoint
│   ├── cda2dcm.go               # CDA → DICOM endpoint
│   ├── stl2dcm.go               # STL → DICOM endpoint
│   ├── send_to_orthanc.go       # Convert & send to Orthanc (multipart upload)
│   ├── send_to_orthanc_from_urls.go  # Convert & send from URLs
│   ├── orchestrate.go           # Composite: download → convert → upload → modify → send
│   ├── studies.go               # Orthanc study proxy (find-by-acsn, patient studies, send-to-modality)
│   ├── modify_study.go          # Orthanc tag modification proxy
│   ├── jobs.go                  # Job status polling endpoint
│   ├── health.go                # Health check (DCMTK + Orthanc)
│   ├── img2dcm_test.go          # Image handler tests
│   └── pdf2dcm_test.go          # PDF handler tests
├── service/
│   ├── dcmtk_runner.go          # DCMTK command executor with timeout
│   ├── orthanc_client.go        # Orthanc REST client (upload, modify, delete, ping, retry)
│   ├── job_manager.go            # Async worker pool (in-memory job registry)
│   ├── image_converter.go        # PNG → BMP converter
│   ├── validator.go               # Input validation (extensions, MIME, keys)
│   ├── dcmtk_runner_test.go     # Runner tests
│   ├── validator_test.go        # Validator tests
│   └── orthanc_client_test.go   # Orthanc client tests
├── model/
│   └── response.go              # JSON response types (ErrorResponse, HealthResponse)
├── router/
│   └── router.go                # Chi router + middleware + CORS
├── tests/
│   ├── validate_dicom.py        # pydicom validation script
│   └── test_preemptive_alignment.py  # Preemptive demographic alignment test
└── README.md                      # This file
```

---

## License

MIT License — See [LICENSE](LICENSE) for details.

## Credits

Original implementation by **Jaisyullah Rafiul Islam** for the **Transformation and Digitalization Team, Ministry of Health Indonesia**.

Refactored and hardened by the dicom-converter-api community.
