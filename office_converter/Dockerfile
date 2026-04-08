# syntax=docker/dockerfile:1

FROM python:3.11-slim

WORKDIR /app

# pywpsrpc may require compiler toolchain in some environments.
RUN apt-get update \
    && apt-get install -y --no-install-recommends build-essential \
    && rm -rf /var/lib/apt/lists/*

COPY office_converter/requirements.txt ./requirements.txt
RUN pip install --no-cache-dir -r requirements.txt

COPY office_converter/server.py ./server.py
COPY office_converter/office_converter_pb2.py ./office_converter_pb2.py
COPY office_converter/office_converter_pb2_grpc.py ./office_converter_pb2_grpc.py

EXPOSE 50061

CMD ["python", "server.py", "--listen", "0.0.0.0:50061", "--output-dir", "/shared/office-output", "--max-workers", "1", "--accepted-formats", "doc,docx,ppt,pptx"]
