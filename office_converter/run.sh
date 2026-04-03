#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
VENV_DIR="$ROOT_DIR/office_converter/.venv"

if [ ! -d "$VENV_DIR" ]; then
  uv venv --python 3.11 "$VENV_DIR"
fi

uv pip install --python "$VENV_DIR/bin/python" -r "$ROOT_DIR/office_converter/requirements.txt"

if [ "$#" -gt 0 ]; then
  EXTRA_ARGS=("$@")
else
  EXTRA_ARGS=(
    --listen "127.0.0.1:50061"
    --output-dir "$ROOT_DIR/office_converter/output"
    --max-workers 1
    --accepted-formats "doc,docx,ppt,pptx"
  )
fi

uv run --python "$VENV_DIR/bin/python" "$ROOT_DIR/office_converter/server.py" "${EXTRA_ARGS[@]}"
