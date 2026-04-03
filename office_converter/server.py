import argparse
import logging
import os
import pathlib
import uuid
from concurrent import futures

import grpc

import office_converter_pb2
import office_converter_pb2_grpc

try:
    from pywpsrpc.rpcwppapi import createWppRpcInstance
    from pywpsrpc.rpcwpsapi import createWpsRpcInstance
except Exception as exc:  # pragma: no cover
    createWppRpcInstance = None
    createWpsRpcInstance = None
    _PYWPS_IMPORT_ERROR = exc
else:
    _PYWPS_IMPORT_ERROR = None


PDF_FORMAT_FOR_WPS = 17
PDF_FORMAT_FOR_WPP = 32


def parse_accepted_formats(raw: str) -> set[str]:
    out: set[str] = set()
    for item in raw.split(","):
        v = item.strip().lower().lstrip(".")
        if v:
            out.add(f".{v}")
    return out


def _convert_word_to_pdf(source_path: str, output_path: str) -> None:
    app = createWpsRpcInstance()
    doc = None
    try:
        app.Visible = False
        doc = app.Documents.Open(source_path)
        doc.SaveAs(output_path, PDF_FORMAT_FOR_WPS)
    finally:
        if doc is not None:
            doc.Close(False)
        app.Quit()


def _convert_ppt_to_pdf(source_path: str, output_path: str) -> None:
    app = createWppRpcInstance()
    presentation = None
    try:
        app.Visible = False
        presentation = app.Presentations.Open(source_path)
        presentation.SaveAs(output_path, PDF_FORMAT_FOR_WPP)
    finally:
        if presentation is not None:
            presentation.Close()
        app.Quit()


def convert_to_pdf(source_path: str, output_dir: str, accepted_formats: set[str]) -> str:
    src = pathlib.Path(source_path)
    if not src.exists() or not src.is_file():
        raise ValueError(f"source file not found: {source_path}")

    ext = src.suffix.lower()
    if ext not in accepted_formats:
        raise ValueError(f"unsupported office extension: {ext}")

    os.makedirs(output_dir, exist_ok=True)
    out_name = f"{src.stem}-{uuid.uuid4().hex}.pdf"
    out_path = os.path.join(output_dir, out_name)

    if ext in {".doc", ".docx"}:
        _convert_word_to_pdf(str(src), out_path)
    else:
        _convert_ppt_to_pdf(str(src), out_path)

    if not os.path.isfile(out_path):
        raise RuntimeError("converted pdf output not generated")

    return os.path.abspath(out_path)


class OfficeConverterService(office_converter_pb2_grpc.OfficeConverterServiceServicer):
    def __init__(self, output_dir: str, accepted_formats: set[str]):
        self._output_dir = output_dir
        self._accepted_formats = accepted_formats

    def ConvertToPdf(self, request, context):
        if _PYWPS_IMPORT_ERROR is not None:
            return office_converter_pb2.ConversionResponse(
                success=False,
                error_code="pywpsrpc_unavailable",
                error_message=f"failed to import pywpsrpc: {_PYWPS_IMPORT_ERROR}",
            )

        source_path = request.source_file_path.strip()
        if not source_path:
            return office_converter_pb2.ConversionResponse(
                success=False,
                error_code="invalid_argument",
                error_message="source_file_path is required",
            )

        target_format = request.target_format.strip().lower()
        if target_format != "pdf":
            return office_converter_pb2.ConversionResponse(
                success=False,
                error_code="unsupported_target_format",
                error_message=f"target_format '{target_format}' is not supported",
            )

        try:
            output_file_path = convert_to_pdf(source_path, self._output_dir, self._accepted_formats)
            return office_converter_pb2.ConversionResponse(
                success=True,
                output_file_path=output_file_path,
            )
        except Exception as exc:
            logging.exception("conversion failed")
            return office_converter_pb2.ConversionResponse(
                success=False,
                error_code="conversion_failed",
                error_message=str(exc),
            )


def main() -> None:
    parser = argparse.ArgumentParser(description="Office to PDF gRPC converter")
    parser.add_argument("--listen", default="127.0.0.1:50061", help="gRPC listen address")
    parser.add_argument("--output-dir", default="office_converter/output", help="converted file output directory")
    parser.add_argument("--max-workers", type=int, default=1, help="grpc worker count, keep 1 for serial processing")
    parser.add_argument("--accepted-formats", default="doc,docx,ppt,pptx", help="accepted source formats, comma separated")
    args = parser.parse_args()

    accepted_formats = parse_accepted_formats(args.accepted_formats)
    if not accepted_formats:
        raise ValueError("accepted formats must not be empty")

    logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(message)s")
    logging.info("starting office converter grpc server on %s", args.listen)
    logging.info("converted output directory: %s", args.output_dir)
    logging.info("accepted source formats: %s", ",".join(sorted(accepted_formats)))

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=max(1, args.max_workers)))
    office_converter_pb2_grpc.add_OfficeConverterServiceServicer_to_server(
        OfficeConverterService(args.output_dir, accepted_formats), server
    )
    server.add_insecure_port(args.listen)
    server.start()
    server.wait_for_termination()


if __name__ == "__main__":
    main()
