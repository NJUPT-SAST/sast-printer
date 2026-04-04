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


def _unwrap_rpc_client(factory_result, kind: str):
    if isinstance(factory_result, tuple):
        if len(factory_result) >= 2:
            ret_code, client = factory_result[0], factory_result[1]
        elif len(factory_result) == 1:
            ret_code, client = factory_result[0], None
        else:
            ret_code, client = -1, None
        if ret_code not in (0, None):
            raise RuntimeError(f"failed to create {kind} rpc client, ret_code={ret_code}")
        if client is None:
            raise RuntimeError(f"failed to create {kind} rpc client, client is None")
        return client

    return factory_result


def _unwrap_wps_application(client, kind: str):
    method_name = "getWpsApplication" if kind == "wps" else "getWppApplication"
    getter = getattr(client, method_name, None)
    if getter is None:
        raise RuntimeError(f"rpc client does not expose {method_name}")

    app_result = getter()
    if isinstance(app_result, tuple):
        if len(app_result) >= 2:
            ret_code, app = app_result[0], app_result[1]
        elif len(app_result) == 1:
            ret_code, app = app_result[0], None
        else:
            ret_code, app = -1, None
        if ret_code not in (0, None):
            raise RuntimeError(f"failed to create {kind} application, ret_code={ret_code}")
        if app is None:
            raise RuntimeError(f"failed to create {kind} application, application is None")
        return app

    return app_result


def _unwrap_rpc_result(result, action: str):
    if isinstance(result, tuple):
        if len(result) >= 2:
            ret_code, payload = result[0], result[1]
        elif len(result) == 1:
            ret_code, payload = result[0], None
        else:
            ret_code, payload = -1, None
        if ret_code not in (0, None):
            raise RuntimeError(f"{action} failed, ret_code={ret_code}")
        return payload

    return result


def _convert_word_to_pdf(source_path: str, output_path: str) -> None:
    client = _unwrap_rpc_client(createWpsRpcInstance(), "wps")
    app = _unwrap_wps_application(client, "wps")
    doc = None
    try:
        app.Visible = False
        doc = _unwrap_rpc_result(app.Documents.Open(source_path), "open wps document")
        _unwrap_rpc_result(doc.SaveAs(output_path, PDF_FORMAT_FOR_WPS), "save wps document as pdf")
    finally:
        if doc is not None:
            try:
                _unwrap_rpc_result(doc.Close(False), "close wps document")
            except Exception:
                logging.exception("failed to close wps document")
        try:
            _unwrap_rpc_result(app.Quit(), "quit wps application")
        except Exception:
            logging.exception("failed to quit wps application")


def _convert_ppt_to_pdf(source_path: str, output_path: str) -> None:
    client = _unwrap_rpc_client(createWppRpcInstance(), "wpp")
    app = _unwrap_wps_application(client, "wpp")
    presentation = None
    try:
        app.Visible = False
        presentation = _unwrap_rpc_result(app.Presentations.Open(source_path), "open wpp presentation")
        _unwrap_rpc_result(presentation.SaveAs(output_path, PDF_FORMAT_FOR_WPP), "save wpp presentation as pdf")
    finally:
        if presentation is not None:
            try:
                _unwrap_rpc_result(presentation.Close(), "close wpp presentation")
            except Exception:
                logging.exception("failed to close wpp presentation")
        try:
            _unwrap_rpc_result(app.Quit(), "quit wpp application")
        except Exception:
            logging.exception("failed to quit wpp application")


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
