VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
IMAGE   ?= cpubench
PLATFORMS ?= linux/amd64,linux/arm64

.PHONY: build build-all test vet lint image image-push image-tar clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/cpubench ./cmd/cpubench

# Static binaries for both architectures; copy the matching one to each server.
build-all:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/cpubench-linux-amd64 ./cmd/cpubench
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/cpubench-linux-arm64 ./cmd/cpubench
	cd dist && sha256sum cpubench-linux-* > SHA256SUMS

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run

# Multi-arch images (slim = distroless, full = Ubuntu with ipmitool/dmidecode/numactl).
image:
	docker buildx build --platform $(PLATFORMS) --target slim -t $(IMAGE):$(VERSION) -t $(IMAGE):latest --build-arg VERSION=$(VERSION) .
	docker buildx build --platform $(PLATFORMS) --target full -t $(IMAGE):$(VERSION)-full -t $(IMAGE):full --build-arg VERSION=$(VERSION) .

image-push:
	docker buildx build --platform $(PLATFORMS) --target slim -t $(IMAGE):$(VERSION) -t $(IMAGE):latest --build-arg VERSION=$(VERSION) --push .
	docker buildx build --platform $(PLATFORMS) --target full -t $(IMAGE):$(VERSION)-full -t $(IMAGE):full --build-arg VERSION=$(VERSION) --push .

# Per-arch image tarballs for servers without a registry (no QEMU needed):
#   scp dist/cpubench-image-arm64.tar arm-01: && ssh arm-01 docker load -i cpubench-image-arm64.tar
image-tar:
	docker build --platform linux/amd64 --target slim --build-arg VERSION=$(VERSION) -t cpubench:latest-amd64 .
	docker save cpubench:latest-amd64 -o dist/cpubench-image-amd64.tar
	docker build --platform linux/arm64 --target slim --build-arg VERSION=$(VERSION) -t cpubench:latest-arm64 .
	docker save cpubench:latest-arm64 -o dist/cpubench-image-arm64.tar

clean:
	rm -rf dist
