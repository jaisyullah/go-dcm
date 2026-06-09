import requests
import time
import sys
import json

BASE_URL = "http://localhost:8080"
ORTHANC_URL = "http://localhost:8042"
AUTH = ("orthanc", "orthanc")

def get_patients():
    resp = requests.post(f"{ORTHANC_URL}/tools/find", json={
        "Level": "Patient",
        "Expand": True,
        "Query": {"PatientID": "123456"}
    }, auth=AUTH)
    return resp.json()

def get_studies():
    resp = requests.post(f"{ORTHANC_URL}/tools/find", json={
        "Level": "Study",
        "Expand": True,
        "Query": {"PatientID": "123456"}
    }, auth=AUTH)
    return resp.json()

def main():
    print("Clearing Orthanc database...")
    patients = requests.get(f"{ORTHANC_URL}/patients", auth=AUTH).json()
    for p in patients:
        requests.delete(f"{ORTHANC_URL}/patients/{p}", auth=AUTH)
    print("Orthanc cleared.")

    # 1. Upload initial study with OLD NAME
    print("\n--- Step 1: Uploading first study with name 'OLD NAME' ---")
    modify_payload = {
        "Replace": {
            "PatientID": "123456",
            "PatientName": "OLD NAME",
            "PatientBirthDate": "19900101",
            "PatientSex": "M",
            "AccessionNumber": "ACSN-001",
            "StudyDescription": "First Study",
            "Modality": "CR"
        }
    }
    parameters = {
        "keys": [
            "PatientID=123456",
            "PatientName=OLD NAME",
            "PatientBirthDate=19900101",
            "PatientSex=M"
        ]
    }
    data = {
        "filetype": "img",
        "orthanc_modify": json.dumps(modify_payload),
        "parameters": json.dumps(parameters)
    }
    files = {
        "file": ("sample_xray.jpg", open("/home/malifnasrulloh/.gemini/antigravity/brain/7f1e3c44-12cb-4fd1-8f5f-1f1b3d443bed/scratch/sample_xray.jpg", "rb"), "image/jpeg")
    }
    
    resp = requests.post(f"{BASE_URL}/api/v1/send-to-orthanc", data=data, files=files)
    assert resp.status_code == 202, f"Initial upload failed: {resp.text}"
    job_id = resp.json()["job_id"]
    print(f"Job created: {job_id}")

    # Wait for job completion
    for _ in range(30):
        job_status = requests.get(f"{BASE_URL}/api/v1/jobs/{job_id}").json()
        if job_status["status"] == "COMPLETED":
            break
        elif job_status["status"] == "FAILED":
            print(f"Job failed: {job_status}")
            sys.exit(1)
        time.sleep(0.5)

    patients = get_patients()
    assert len(patients) == 1, "Patient not found in Orthanc"
    print(f"Initial Patient Name in Orthanc: '{patients[0]['MainDicomTags']['PatientName']}'")
    assert patients[0]['MainDicomTags']['PatientName'] == "OLD NAME"

    # 2. Upload second study with NEW NAME (demographics mismatch!)
    print("\n--- Step 2: Uploading second study with name 'NEW NAME' (mismatch) ---")
    modify_payload2 = {
        "Replace": {
            "PatientID": "123456",
            "PatientName": "NEW NAME",
            "PatientBirthDate": "19900101",
            "PatientSex": "M",
            "AccessionNumber": "ACSN-002",
            "StudyDescription": "Second Study",
            "Modality": "CR"
        }
    }
    parameters2 = {
        "keys": [
            "PatientID=123456",
            "PatientName=NEW NAME",
            "PatientBirthDate=19900101",
            "PatientSex=M"
        ]
    }
    data2 = {
        "filetype": "img",
        "orthanc_modify": json.dumps(modify_payload2),
        "parameters": json.dumps(parameters2)
    }
    files2 = {
        "file": ("sample_xray.jpg", open("/home/malifnasrulloh/.gemini/antigravity/brain/7f1e3c44-12cb-4fd1-8f5f-1f1b3d443bed/scratch/sample_xray.jpg", "rb"), "image/jpeg")
    }

    resp2 = requests.post(f"{BASE_URL}/api/v1/send-to-orthanc", data=data2, files=files2)
    assert resp2.status_code == 202, f"Second upload failed: {resp2.text}"
    job_id2 = resp2.json()["job_id"]
    print(f"Job created: {job_id2}")

    # Wait for job completion
    for _ in range(30):
        job_status = requests.get(f"{BASE_URL}/api/v1/jobs/{job_id2}").json()
        if job_status["status"] == "COMPLETED":
            break
        elif job_status["status"] == "FAILED":
            print(f"Job failed: {job_status}")
            sys.exit(1)
        time.sleep(0.5)

    # 3. Verify final demographics in Orthanc
    print("\n--- Step 3: Verifying final state in Orthanc ---")
    patients = get_patients()
    studies = get_studies()

    print(f"Number of patients: {len(patients)}")
    print(f"Number of studies: {len(studies)}")
    
    for idx, study in enumerate(studies):
        print(f"Study {idx+1} Accession: {study['MainDicomTags'].get('AccessionNumber')}, PatientName: '{study['PatientMainDicomTags']['PatientName']}'")

    assert len(patients) == 1, f"Expected 1 patient, got {len(patients)}"
    assert len(studies) == 2, f"Expected 2 studies, got {len(studies)}"
    assert patients[0]['MainDicomTags']['PatientName'] == "NEW NAME", f"Expected Patient Name to be NEW NAME, got {patients[0]['MainDicomTags']['PatientName']}"
    
    for study in studies:
        assert study['PatientMainDicomTags']['PatientName'] == "NEW NAME", f"Study patient name not updated: {study['PatientMainDicomTags']['PatientName']}"

    print("\nSUCCESS: Pre-emptive patient demographic alignment successfully modified existing studies and unified demographics!")

if __name__ == "__main__":
    main()
