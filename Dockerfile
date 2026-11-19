# Build stage
FROM golang:1.27-trixie AS builder

ARG VERSION=dev

WORKDIR /src
COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /downloadonce ./cmd/server

# Runtime stage
FROM debian:trixie-slim

# System tools only — no python3-opencv from apt (it drags in Qt5, Mesa,
# GPU drivers, OpenMPI, GDAL, and 350+ packages we don't need).
# apt-get upgrade installs security updates that the base image does not have yet.
RUN apt-get update && apt-get upgrade -y && apt-get install -y --no-install-recommends \
    ffmpeg \
    imagemagick \
    fonts-dejavu-core \
    ca-certificates \
    python3 \
    python3-pip \
    python3-venv \
    && rm -rf /var/lib/apt/lists/*

# Install Python watermarking stack.
# We install numpy, opencv-python-headless and PyWavelets first so that
# invisible-watermark's --no-deps install can't accidentally pull in the
# heavier opencv-python (GUI) variant or torch.
# invisible-watermark bundles a RivaGAN backend that does a hard `import torch`
# at module load time; we only use the DWT-DCT-SVD method so we stub it out.
# pip is removed from the venv at the end: nothing uses it at runtime.
RUN python3 -m venv /opt/venv \
    && /opt/venv/bin/pip install --no-cache-dir \
       numpy \
       opencv-python-headless \
       PyWavelets \
    && /opt/venv/bin/pip install --no-cache-dir --no-deps invisible-watermark \
    && /opt/venv/bin/python3 -c "\
import glob; \
[open(f,'w').write('class RivaWatermark:\n    def __init__(self,*a,**k): raise RuntimeError(\"RivaGAN requires torch\")\n') \
 for f in glob.glob('/opt/venv/lib/python*/site-packages/imwatermark/rivaGan.py')]" \
    && /opt/venv/bin/pip uninstall -y pip \
    && find /opt/venv -name "*.pyc" -delete \
    && find /opt/venv -type d -name "__pycache__" -exec rm -rf {} + 2>/dev/null; true

COPY --from=builder /downloadonce /usr/local/bin/downloadonce

RUN mkdir -p /data
ENV DATA_DIR=/data
ENV FONT_PATH=/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf
ENV VENV_PATH=/opt/venv
ENV LISTEN_ADDR=:8080

EXPOSE 8080
VOLUME /data

ENTRYPOINT ["downloadonce"]
