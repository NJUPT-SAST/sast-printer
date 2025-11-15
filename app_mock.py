### THIS FILE IS FOR TESTING ONLY. DO NOT USE IN PRODUCTION ###

from fastapi import FastAPI, Form
from fastapi.responses import JSONResponse
from datetime import datetime, timedelta
import secrets
import httpx
import json
import os

app = FastAPI(title="sast-printer-mock")

# Lark API configuration
LARK_TOKEN_FILE = os.getenv("LARK_TOKEN_FILE", "token.json")
LARK_AUTH_URL = "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal"
LARK_DOWNLOAD_URL = "https://open.feishu.cn/open-apis/drive/v1/medias/{file_id}/download"


def load_lark_credentials() -> tuple[str, str]:
    """Load app_id and app_secret from token.json"""
    try:
        with open(LARK_TOKEN_FILE, "r") as f:
            data = json.load(f)
            return data["app_id"], data["app_secret"]
    except Exception as e:
        raise Exception(f"Failed to load Lark credentials: {e}")


async def get_tenant_access_token() -> str:
    """Get tenant_access_token from Lark API."""
    app_id, app_secret = load_lark_credentials()
    
    async with httpx.AsyncClient() as client:
        response = await client.post(
            LARK_AUTH_URL,
            json={
                "app_id": app_id,
                "app_secret": app_secret
            }
        )
        
        if response.status_code != 200:
            raise Exception(f"Failed to get tenant access token: HTTP {response.status_code}")
        
        data = response.json()
        if data.get("code") != 0:
            raise Exception(f"Lark API error: {data.get('msg', 'Unknown error')}")
        
        return data["tenant_access_token"]


async def download_file_from_lark(file_id: str) -> bytes:
    """Download file content from Lark Drive API."""
    token = await get_tenant_access_token()
    url = LARK_DOWNLOAD_URL.format(file_id=file_id)
    
    async with httpx.AsyncClient() as client:
        response = await client.get(
            url,
            headers={
                "Authorization": f"Bearer {token}"
            }
        )
        
        if response.status_code != 200:
            raise Exception(f"Failed to download file from Lark: HTTP {response.status_code}")
        
        return response.content


@app.get("/health")
def health():
    return {"status": "ok"}


@app.get("/printers")
def list_printers():
    # Return mock printers
    return JSONResponse(content={
        "printers": [
            {
                "name": "sast-printer",
                "device_uri": "ipp://mock-printer-1",
                "status": "idle"
            },
            {
                "name": "other-printer",
                "device_uri": "ipp://mock-printer-2",
                "status": "idle"
            }
        ]
    })


@app.post("/print")
async def print_pdf(
    file_id: str = Form(...),
    printer_id: str = Form(...),
    manual_duplex: bool = Form(False)
):
    # Try to download file from Lark to test if download works
    try:
        file_content = await download_file_from_lark(file_id)
        file_size = len(file_content)
        print(f"Successfully downloaded file {file_id}, size: {file_size} bytes")
    except Exception as e:
        # Return error if download fails
        print(f"Failed to download file {file_id}: {e}")
        if not manual_duplex:
            return JSONResponse(status_code=500, content={
                "status": f"文件下载失败：{e}",
                "printer": printer_id,
                "job_id": "",
                "continue_url": "",
                "expires_at": ""
            })
        else:
            return JSONResponse(status_code=500, content={
                "status": f"文件下载失败：{e}",
                "printer": printer_id,
                "job_id": "",
                "continue_url": "",
                "expires_at": ""
            })
    
    # Mock - always return success if download succeeded
    if not manual_duplex:
        # Normal printing success
        return JSONResponse(status_code=202, content={
            "status": "已发送到打印机",
            "printer": printer_id,
            "job_id": "12345",
            "continue_url": "",
            "expires_at": ""
        })
    else:
        # Manual duplex success
        continuation_job_id = secrets.token_urlsafe(16)
        expires_at = datetime.now() + timedelta(minutes=30)
        
        return JSONResponse(status_code=202, content={
            "status": "偶数页已发送到打印机，请翻转纸张后继续",
            "printer": printer_id,
            "job_id": "67890",
            "continue_url": f"{continuation_job_id}",
            "expires_at": expires_at.isoformat()
        })


@app.post("/print/continue/{job_id}")
def continue_manual_duplex(job_id: str):
    # Always return success
    return JSONResponse(status_code=202, content={
        "status": "奇数页已发送到打印机，手动双面打印完成",
        "printer": "sast-printer",
        "job_id": "67891"
    })
