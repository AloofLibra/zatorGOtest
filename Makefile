.PHONY: test vet build cross launcher clean

VERSION ?= $(shell cat VERSION 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/AloofLibra/zator.versionOverride=$(VERSION)

test:
	go test ./...

vet:
	go vet ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/zator ./cmd/zator

cross:
	OUT=dist VER=$(VERSION) bash scripts/crossbuild.sh

launcher:
	install -m 0755 scripts/zator-launcher.sh dist/zator-launcher.sh

clean:
	rm -rf bin dist
