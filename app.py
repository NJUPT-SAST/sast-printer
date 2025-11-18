from fastapi import FastAPI, Form, HTTPException
from fastapi.responses import JSONResponse
import tempfile
import os
import cups
from pypdf import PdfReader, PdfWriter
from datetime import datetime, timedelta
from typing import Optional, Callable
import secrets
import threading
import httpx
import json

app = FastAPI(title="sast-printer", docs_url=None, redoc_url=None)

# Lark API configuration
LARK_TOKEN_FILE = os.getenv("LARK_TOKEN_FILE", "token.json")
LARK_AUTH_URL = "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal"
LARK_DOWNLOAD_URL = "https://open.feishu.cn/open-apis/drive/v1/medias/{file_id}/download"

# Cache for tenant access token
_tenant_token_cache = {"token": None, "expires_at": 0}
_token_lock = threading.Lock()


def load_lark_credentials() -> tuple[str, str]:
    """Load app_id and app_secret from token.json"""
    try:
        with open(LARK_TOKEN_FILE, "r") as f:
            data = json.load(f)
            return data["app_id"], data["app_secret"]
    except Exception as e:
        raise HTTPException(
            status_code=500,
            detail=f"Failed to load Lark credentials: {e}"
        )


async def get_tenant_access_token() -> str:
    """
    Get tenant_access_token from Lark API.
    Implements caching to avoid frequent API calls.
    """
    with _token_lock:
        # Check if cached token is still valid (with 5min buffer)
        if _tenant_token_cache["token"] and _tenant_token_cache["expires_at"] > datetime.now().timestamp() + 300:
            return _tenant_token_cache["token"]
    
    # Fetch new token
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
            raise HTTPException(
                status_code=500,
                detail=f"Failed to get tenant access token: HTTP {response.status_code}"
            )
        
        data = response.json()
        if data.get("code") != 0:
            raise HTTPException(
                status_code=500,
                detail=f"Lark API error: {data.get('msg', 'Unknown error')}"
            )
        
        token = data["tenant_access_token"]
        expire = data.get("expire", 7200)  # Default 2 hours
        
        # Cache the token
        with _token_lock:
            _tenant_token_cache["token"] = token
            _tenant_token_cache["expires_at"] = datetime.now().timestamp() + expire
        
        return token


async def fetch_file_from_api(file_id: str) -> bytes:
    """
    Fetch file content from Lark Drive API using file_id.
    """
    # Get tenant access token
    token = await get_tenant_access_token()
    
    # Download file from Lark
    url = LARK_DOWNLOAD_URL.format(file_id=file_id)
    
    async with httpx.AsyncClient() as client:
        response = await client.get(
            url,
            headers={
                "Authorization": f"Bearer {token}"
            }
        )
        
        if response.status_code != 200:
            raise HTTPException(
                status_code=500,
                detail=f"Failed to download file from Lark: HTTP {response.status_code}"
            )
        
        return response.content

# In-memory store for manual duplex jobs
# Structure: {job_id: {"odd_pages_path": str, "printer_id": str, "filename": str, "expires_at": datetime}}
duplex_jobs = {}
duplex_jobs_lock = threading.Lock()


def get_cups_connection():
    try:
        return cups.Connection()
    except RuntimeError as e:
        raise HTTPException(status_code=500, detail=f"CUPS connection failed: {e}")


def cleanup_expired_jobs():
    """Remove expired duplex jobs and their temporary files."""
    with duplex_jobs_lock:
        now = datetime.now()
        expired = [job_id for job_id, data in duplex_jobs.items() if data["expires_at"] < now]
        for job_id in expired:
            job_data = duplex_jobs.pop(job_id)
            # Clean up temporary file
            if os.path.exists(job_data["odd_pages_path"]):
                try:
                    os.remove(job_data["odd_pages_path"])
                except Exception:
                    pass


def split_pdf_for_manual_duplex(pdf_path: str, printer_id: str) -> tuple[str, str]:
    """
    Split PDF into even pages and odd pages for manual duplex printing.
    Pages are reversed so that when printed, the stack comes out in correct order.
    If total pages is odd, add a blank page to even pages.
    
    Special handling for 'sast-printer': only reverse even pages, not odd pages.
    
    Returns: (even_pages_path, odd_pages_path)
    """
    reader = PdfReader(pdf_path)
    total_pages = len(reader.pages)
    
    # Collect pages in separate lists first
    even_pages = []
    odd_pages = []
    
    for i in range(total_pages):
        page_num = i + 1  # 1-indexed
        if page_num % 2 == 0:
            even_pages.append(reader.pages[i])
        else:
            odd_pages.append(reader.pages[i])
    
    # If total pages is odd, add blank page to even pages
    if total_pages % 2 == 1:
        # Create a blank page with same size as last page
        last_page = reader.pages[-1]
        blank_page = PdfWriter()
        blank_page.add_blank_page(
            width=last_page.mediabox.width,
            height=last_page.mediabox.height
        )
        even_pages.append(blank_page.pages[0])
    
    # Reverse pages based on printer behavior
    if printer_id == "sast-printer":
        # For sast-printer: only reverse even pages
        even_pages.reverse()
    else:
        # For other printers: reverse both lists
        even_pages.reverse()
        odd_pages.reverse()
    
    # Create writers and add pages
    even_writer = PdfWriter()
    odd_writer = PdfWriter()
    
    for page in even_pages:
        even_writer.add_page(page)
    for page in odd_pages:
        odd_writer.add_page(page)
    
    # Write to temporary files
    even_tmp = tempfile.NamedTemporaryFile(delete=False, suffix="_even.pdf")
    odd_tmp = tempfile.NamedTemporaryFile(delete=False, suffix="_odd.pdf")
    
    with open(even_tmp.name, "wb") as f:
        even_writer.write(f)
    with open(odd_tmp.name, "wb") as f:
        odd_writer.write(f)
    
    return even_tmp.name, odd_tmp.name


def make_collated_pdf(input_pdf_path: str, copies: int) -> str:
    """
    Generate a new PDF that repeats the entire document 'copies' times in order
    to guarantee collated output (1..N, 1..N, ...), regardless of printer support.
    Returns path to the generated temporary PDF.
    """
    reader = PdfReader(input_pdf_path)
    writer = PdfWriter()
    if copies < 1:
        copies = 1
    for _ in range(copies):
        for i in range(len(reader.pages)):
            writer.add_page(reader.pages[i])
    out_tmp = tempfile.NamedTemporaryFile(delete=False, suffix="_collated.pdf")
    with open(out_tmp.name, "wb") as f:
        writer.write(f)
    return out_tmp.name


@app.get("/health")
def health():
    return {"status": "ok"}


@app.get("/printers")
def list_printers():
    conn = get_cups_connection()
    printers = conn.getPrinters()
    # Convert to simple list of names and a few attributes
    result = []
    for name, attrs in printers.items():
        result.append({
            "name": name,
            "device_uri": attrs.get("device-uri"),
            "status": attrs.get("printer-state-message") or attrs.get("printer-state-reasons"),
        })
    return JSONResponse(content={"printers": result})


@app.post("/print")
async def print_pdf(
    file_id: str = Form(...),
    printer_id: str = Form(...),
    manual_duplex: bool = Form(False),
    copies: int = Form(1)
):
    # Cleanup expired jobs first
    cleanup_expired_jobs()
    
    # Validate copies
    if copies < 1:
        copies = 1
    elif copies > 10:
        copies = 10  # Limit to prevent abuse
    
    # Fetch file from API
    try:
        file_content = await fetch_file_from_api(file_id)
    except Exception as e:
        return JSONResponse(status_code=500, content={
            "status": f"获取文件失败：{e}",
            "printer": printer_id,
            "job_id": "",
            "continue_url": "",
            "expires_at": "",
            "copies": copies
        })
    
    # Basic validation
    filename = f"{file_id}.pdf"

    conn = get_cups_connection()
    printers = conn.getPrinters()
    if printer_id not in printers:
        return JSONResponse(status_code=400, content={
            "status": f"打印机未找到：打印机 '{printer_id}' 不存在",
            "printer": printer_id,
            "job_id": "",
            "continue_url": "",
            "expires_at": "",
            "copies": copies
        })

    tmp_file = None
    even_pages_file = None
    odd_pages_file = None
    
    try:
        # Save fetched file
        with tempfile.NamedTemporaryFile(delete=False, suffix=".pdf") as tmp:
            tmp_file = tmp.name
            tmp.write(file_content)

        if not manual_duplex:
            # Normal printing - print entire document
            try:
                # For sast-printer, don't reverse pages; for others, reverse
                if printer_id == "sast-printer":
                    # No reversal for sast-printer
                    try:
                        to_print_path = tmp_file
                        collated_tmp = None
                        if copies > 1:
                            collated_tmp = make_collated_pdf(to_print_path, copies)
                            to_print_path = collated_tmp
                            options = {"copies": "1"}
                        else:
                            options = {
                                "copies": "1",
                            }
                        job_id = conn.printFile(
                            printer_id,
                            to_print_path,
                            filename,
                            options,
                        )
                        # Cleanup collated temp if created
                        if collated_tmp and os.path.exists(collated_tmp):
                            try:
                                os.remove(collated_tmp)
                            except Exception:
                                pass
                    except RuntimeError as e:
                        return JSONResponse(status_code=500, content={
                            "status": f"打印任务提交失败：{e}",
                            "printer": printer_id,
                            "job_id": "",
                            "continue_url": "",
                            "expires_at": "",
                            "copies": copies
                        })
                else:
                    # Reverse the pages for other printers
                    reader = PdfReader(tmp_file)
                    reversed_writer = PdfWriter()
                    
                    # Add pages in reverse order
                    for i in range(len(reader.pages) - 1, -1, -1):
                        reversed_writer.add_page(reader.pages[i])
                    
                    # Write reversed PDF to a new temp file
                    reversed_tmp = tempfile.NamedTemporaryFile(delete=False, suffix="_reversed.pdf")
                    with open(reversed_tmp.name, "wb") as f:
                        reversed_writer.write(f)
                    
                    try:
                        to_print_path = reversed_tmp.name
                        collated_tmp = None
                        if copies > 1:
                            collated_tmp = make_collated_pdf(to_print_path, copies)
                            to_print_path = collated_tmp
                            options = {"copies": "1"}
                        else:
                            options = {
                                "copies": "1",
                            }
                        job_id = conn.printFile(
                            printer_id,
                            to_print_path,
                            filename,
                            options,
                        )
                        if collated_tmp and os.path.exists(collated_tmp):
                            try:
                                os.remove(collated_tmp)
                            except Exception:
                                pass
                    except RuntimeError as e:
                        return JSONResponse(status_code=500, content={
                            "status": f"打印任务提交失败：{e}",
                            "printer": printer_id,
                            "job_id": "",
                            "continue_url": "",
                            "expires_at": "",
                            "copies": copies
                        })
                    finally:
                        # Clean up reversed temp file
                        if os.path.exists(reversed_tmp.name):
                            try:
                                os.remove(reversed_tmp.name)
                            except Exception:
                                pass
            except Exception as e:
                if isinstance(e, HTTPException):
                    raise
                return JSONResponse(status_code=500, content={
                    "status": f"PDF 处理失败：{e}",
                    "printer": printer_id,
                    "job_id": "",
                    "continue_url": "",
                    "expires_at": "",
                    "copies": copies
                })

            return JSONResponse(status_code=202, content={
                "status": "已发送到打印机",
                "printer": printer_id,
                "job_id": str(job_id),
                "continue_url": "",
                "expires_at": "",
                "copies": copies
            })
        else:
            # Manual duplex - split and print even pages first
            try:
                even_pages_file, odd_pages_file = split_pdf_for_manual_duplex(tmp_file, printer_id)
            except Exception as e:
                return JSONResponse(status_code=500, content={
                    "status": f"PDF 分页失败：{e}",
                    "printer": printer_id,
                    "job_id": "",
                    "continue_url": "",
                    "expires_at": "",
                    "copies": copies
                })
            
            # Print even pages first
            try:
                even_to_print = even_pages_file
                collated_even_tmp = None
                collated_odd_tmp = None
                if copies > 1:
                    collated_even_tmp = make_collated_pdf(even_pages_file, copies)
                    even_to_print = collated_even_tmp
                # Prepare odd collated for continuation if needed
                if copies > 1:
                    collated_odd_tmp = make_collated_pdf(odd_pages_file, copies)
                    # replace odd_pages_file with collated one for continuation
                    odd_pages_file_to_store = collated_odd_tmp
                else:
                    odd_pages_file_to_store = odd_pages_file

                even_job_id = conn.printFile(
                    printer_id,
                    even_to_print,
                    f"{filename}_even",
                    {"copies": "1"},
                )
            except RuntimeError as e:
                return JSONResponse(status_code=500, content={
                    "status": f"偶数页打印任务提交失败：{e}",
                    "printer": printer_id,
                    "job_id": "",
                    "continue_url": "",
                    "expires_at": "",
                    "copies": copies
                })
            
            # Generate unique job ID for the continuation
            continuation_job_id = secrets.token_urlsafe(16)
            expires_at = datetime.now() + timedelta(minutes=30)
            
            # Store odd pages info for continuation
            with duplex_jobs_lock:
                duplex_jobs[continuation_job_id] = {
                    "odd_pages_path": odd_pages_file_to_store,
                    "printer_id": printer_id,
                    "filename": filename,
                    "expires_at": expires_at,
                    "copies": copies
                }
            
            # 不删除用于续打的奇数页文件；
            # 如果存储的是原始 odd_pages_file，则避免在 finally 中清理；
            # 如果存储的是拼接版，则允许 finally 清理原始 odd_pages_file。
            if 'odd_pages_file_to_store' in locals() and odd_pages_file_to_store == odd_pages_file:
                odd_pages_file = None
            # Cleanup temporary collated even/odd files that are not stored
            if 'collated_even_tmp' in locals() and collated_even_tmp and os.path.exists(collated_even_tmp):
                try:
                    os.remove(collated_even_tmp)
                except Exception:
                    pass
            # Do not remove collated_odd_tmp if it is stored for continuation
            
            return JSONResponse(status_code=202, content={
                "status": "偶数页已发送到打印机，请翻转纸张后继续",
                "printer": printer_id,
                "job_id": str(even_job_id),
                "continue_url": f"{continuation_job_id}",
                "expires_at": expires_at.isoformat(),
                "copies": copies
            })
            
    finally:
        # Cleanup temporary files
        for path in [tmp_file, even_pages_file, odd_pages_file]:
            if path and os.path.exists(path):
                try:
                    os.remove(path)
                except Exception:
                    pass


@app.post("/print/continue/{job_id}")
def continue_manual_duplex(job_id: str):
    """
    Continue manual duplex printing by printing odd pages.
    This endpoint should be called after the user has flipped the paper.
    """
    # Cleanup expired jobs
    cleanup_expired_jobs()
    
    # Retrieve job info
    with duplex_jobs_lock:
        if job_id not in duplex_jobs:
            return JSONResponse(status_code=404, content={
                "status": "任务未找到或已过期",
                "printer": "",
                "job_id": ""
            })
        
        job_data = duplex_jobs.pop(job_id)
    
    # Check if still valid
    if datetime.now() > job_data["expires_at"]:
        # Clean up file
        if os.path.exists(job_data["odd_pages_path"]):
            try:
                os.remove(job_data["odd_pages_path"])
            except Exception:
                pass
        return JSONResponse(status_code=410, content={
            "status": "任务已过期",
            "printer": job_data["printer_id"],
            "job_id": ""
        })
    
    # Print odd pages
    conn = get_cups_connection()
    odd_pages_path = job_data["odd_pages_path"]
    
    try:
        if not os.path.exists(odd_pages_path):
            return JSONResponse(status_code=500, content={
                "status": "奇数页文件丢失",
                "printer": job_data["printer_id"],
                "job_id": ""
            })
        
        try:
            # 当奇数页文件已按 copies 拼接时，这里始终用 1 份提交
            odd_job_id = conn.printFile(
                job_data["printer_id"],
                odd_pages_path,
                f"{job_data['filename']}_odd",
                {"copies": "1"},
            )
        except RuntimeError as e:
            return JSONResponse(status_code=500, content={
                "status": f"奇数页打印任务提交失败：{e}",
                "printer": job_data["printer_id"],
                "job_id": ""
            })
        
        return JSONResponse(status_code=202, content={
            "status": "奇数页已发送到打印机",
            "printer": job_data["printer_id"],
            "job_id": str(odd_job_id)
        })
    finally:
        # Cleanup odd pages file
        if os.path.exists(odd_pages_path):
            try:
                os.remove(odd_pages_path)
            except Exception:
                pass
