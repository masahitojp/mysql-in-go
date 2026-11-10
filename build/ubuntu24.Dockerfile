FROM docker.io/library/ubuntu:24.04
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    build-essential cmake ninja-build bison m4 rpcsvc-proto libssl-dev pkg-config \
    python3 git curl ca-certificates zlib1g-dev libncurses-dev && rm -rf /var/lib/apt/lists/*
WORKDIR /src
