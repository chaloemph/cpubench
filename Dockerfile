# Cross-compiles on the build host (no QEMU needed for the Go build), then
# packages for each target platform.
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /out/cpubench ./cmd/cpubench \
    && mkdir -p /out/work/results

# Small image for K8s/Docker benchmark runs.
FROM gcr.io/distroless/static-debian12:nonroot AS slim
COPY --from=build /out/cpubench /usr/local/bin/cpubench
# distroless has no shell: bring a results dir owned by the nonroot user (65532)
COPY --from=build --chown=65532:65532 /out/work /work
WORKDIR /work
ENV CPUBENCH_RESULTS=/work/results
ENTRYPOINT ["/usr/local/bin/cpubench"]
CMD ["--help"]

# Full image: adds tools for DIMM details (dmidecode), in-band IPMI power and NUMA inspection.
FROM ubuntu:24.04 AS full
RUN apt-get update && apt-get install -y --no-install-recommends ipmitool dmidecode numactl ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/cpubench /usr/local/bin/cpubench
WORKDIR /work
ENV CPUBENCH_RESULTS=/work/results
ENTRYPOINT ["/usr/local/bin/cpubench"]
CMD ["--help"]
