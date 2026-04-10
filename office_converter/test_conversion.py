#!/usr/bin/env python3

import argparse
import os
import pathlib
import sys
import time

import grpc

import office_converter_pb2
import office_converter_pb2_grpc


SUPPORTED_EXTENSIONS = {".doc", ".docx", ".ppt", ".pptx"}


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Test the Office conversion gRPC service")
    parser.add_argument("source_file", help="Office source file path (.doc/.docx/.ppt/.pptx)")
    parser.add_argument("--grpc-address", default="127.0.0.1:50061", help="gRPC server address")
    parser.add_argument("--timeout", type=int, default=120, help="gRPC timeout in seconds")
    parser.add_argument("--expected-output-dir", default="/tmp/office-output", help="Expected PDF output directory")
    parser.add_argument("--keep-output", action="store_true", help="Keep converted PDF after test")
    return parser


def validate_source(path: pathlib.Path) -> None:
    if not path.exists():
        raise FileNotFoundError(f"source file not found: {path}")
    if not path.is_file():
        raise ValueError(f"source file is not a regular file: {path}")
    if path.suffix.lower() not in SUPPORTED_EXTENSIONS:
        raise ValueError(f"unsupported file extension: {path.suffix.lower()}")


def wait_for_file(path: pathlib.Path, timeout_seconds: int) -> None:
    deadline = time.time() + timeout_seconds
    while time.time() < deadline:
        if path.exists() and path.is_file() and path.stat().st_size > 0:
            return
        time.sleep(0.5)
    raise TimeoutError(f"converted file not ready after {timeout_seconds}s: {path}")


def main() -> int:
    parser = build_parser()
    args = parser.parse_args()

    source_path = pathlib.Path(args.source_file).expanduser().resolve()
    validate_source(source_path)

    expected_dir = pathlib.Path(args.expected_output_dir).expanduser().resolve()
    expected_dir.mkdir(parents=True, exist_ok=True)

    channel = grpc.insecure_channel(args.grpc_address)
    grpc.channel_ready_future(channel).result(timeout=args.timeout)
    client = office_converter_pb2_grpc.OfficeConverterServiceStub(channel)

    request = office_converter_pb2.ConversionRequest(
        source_file_path=str(source_path),
        target_format="pdf",
    )

    response = client.ConvertToPdf(request, timeout=args.timeout)
    if not response.success:
        print("conversion failed", file=sys.stderr)
        print(f"error_code: {response.error_code}", file=sys.stderr)
        print(f"error_message: {response.error_message}", file=sys.stderr)
        return 1

    output_path = pathlib.Path(response.output_file_path).expanduser().resolve()
    print(f"source_file: {source_path}")
    print(f"output_file: {output_path}")
    print(f"grpc_address: {args.grpc_address}")

    wait_for_file(output_path, args.timeout)

    if output_path.suffix.lower() != ".pdf":
        print(f"unexpected output extension: {output_path.suffix}", file=sys.stderr)
        return 1

    if output_path.stat().st_size <= 0:
        print(f"output file is empty: {output_path}", file=sys.stderr)
        return 1

    if not args.keep_output:
        try:
            output_path.unlink()
        except FileNotFoundError:
            pass

    print("conversion test passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())