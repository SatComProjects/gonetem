
VERSION := $(shell cat VERSION)
INSTALLDIR := /usr/local/bin
CONFDIR := /etc/gonetem
DEB_OPTIONS := --input-type dir --name gonetem --version $(VERSION) \
		--description "gonetem is a network emulator written in go" \
		--maintainer "<mickael.royer@enac.fr>" \
		--config-files /etc/gonetem/config.yaml \
		--deb-systemd conf/gonetem.service\
		--deb-systemd-auto-start --deb-systemd-enable \
		--deb-recommends xterm --deb-recommends wireshark \
		--deb-recommends "docker-ce | docker.io"

proto: internal/proto/netem.proto
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		internal/proto/netem.proto

build-console-amd64:
	env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ./bin/gonetem-console_amd64 \
		-ldflags "-X github.com/satcomprojects/gonetem/internal/options.VERSION=$(VERSION)" \
		cmd/gonetem-console/main.go

build-server-amd64:
	env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ./bin/gonetem-server_amd64 \
		-ldflags "-X github.com/satcomprojects/gonetem/internal/options.VERSION=$(VERSION)" \
		cmd/gonetem-server/main.go

build-amd64: build-console-amd64 build-server-amd64

build-console-armv7:
	env CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o ./bin/gonetem-console_armv7 \
		-ldflags "-X github.com/satcomprojects/gonetem/internal/options.VERSION=$(VERSION)" \
		cmd/gonetem-console/main.go

build-server-armv7:
	env CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o ./bin/gonetem-server_armv7 \
		-ldflags "-X github.com/satcomprojects/gonetem/internal/options.VERSION=$(VERSION)" \
		cmd/gonetem-server/main.go

build-armv7: build-console-armv7 build-server-armv7

build-console-arm64:
	env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o ./bin/gonetem-console_arm64 \
		-ldflags "-X github.com/satcomprojects/gonetem/internal/options.VERSION=$(VERSION)" \
		cmd/gonetem-console/main.go

build-server-arm64:
	env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o ./bin/gonetem-server_arm64 \
		-ldflags "-X github.com/satcomprojects/gonetem/internal/options.VERSION=$(VERSION)" \
		cmd/gonetem-server/main.go

build-arm64: build-console-arm64 build-server-arm64

build-console-darwin-arm64:
	env CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o ./bin/gonetem-console_darwin_arm64 \
		-ldflags "-X github.com/satcomprojects/gonetem/internal/options.VERSION=$(VERSION)" \
		cmd/gonetem-console/main.go

build-deb-amd64: clean build-amd64
	docker run -v $(shell pwd):/src --rm gonetem-build fpm --output-type deb \
		--architecture=amd64 \
		${DEB_OPTIONS} \
		bin/gonetem-server_amd64=/usr/sbin/gonetem-server \
		bin/gonetem-console_amd64=/usr/bin/gonetem-console \
		conf/config.yaml=/etc/gonetem/ \

build-deb-armv7: clean build-armv7
	docker run -v $(shell pwd):/src --rm gonetem-build fpm --output-type deb \
		--architecture=armhf \
		${DEB_OPTIONS} \
		bin/gonetem-server_armv7=/usr/sbin/gonetem-server \
		bin/gonetem-console_armv7=/usr/bin/gonetem-console \
		conf/config.yaml=/etc/gonetem/ \

build-deb-arm64: clean build-arm64
	docker run -v $(shell pwd):/src --rm gonetem-build fpm --output-type deb \
		--architecture=arm64 \
		${DEB_OPTIONS} \
		bin/gonetem-server_arm64=/usr/sbin/gonetem-server \
		bin/gonetem-console_arm64=/usr/bin/gonetem-console \
		conf/config.yaml=/etc/gonetem/ \

clean:
	rm -rf bin

install-amd64: clean build-amd64
	@echo "installing gonetem-console/gonetem-server in '${INSTALLDIR}' directory"
	systemctl stop gonetem.service || true
	cp bin/gonetem-server_amd64 ${INSTALLDIR}/gonetem-server
	cp bin/gonetem-console_amd64 ${INSTALLDIR}/gonetem-console
	mkdir -p ${CONFDIR}
	cp conf/config.yaml ${CONFDIR}
	cp conf/gonetem.service /etc/systemd/system/
	
	systemctl daemon-reload
	systemctl enable gonetem.service
	systemctl start gonetem.service
	cp conf/99-custom-network.conf /etc/sysctl.d/
	sysctl --system

install-arm64: clean build-arm64
	@echo "installing gonetem-console/gonetem-server in '${INSTALLDIR}' directory"
	systemctl stop gonetem.service || true
	cp bin/gonetem-server_arm64 ${INSTALLDIR}/gonetem-server
	cp bin/gonetem-console_arm64 ${INSTALLDIR}/gonetem-console
	mkdir -p ${CONFDIR}
	cp conf/config.yaml ${CONFDIR}
	cp conf/gonetem.service /etc/systemd/system/
	systemctl daemon-reload
	systemctl enable gonetem.service
	systemctl start gonetem.service
	cp conf/99-custom-network.conf /etc/sysctl.d/
	sysctl --system

uninstall:
	@echo "delete gonetem-console/gonetem-server in '${INSTALLDIR}' directory"
	rm ${INSTALLDIR}/gonetem-console
	rm ${INSTALLDIR}/gonetem-server
	rm ${CONFDIR}/config.yaml
	rmdir ${CONFDIR}
	rm /etc/sysctl.d/99-custom-network.conf
	sysctl --system
	rmdir ${CONFDIR}



# ---------------------------------------------------------------------------
# Quality gates
#
# `make check` is the fast, hermetic gate used by contributors and CI.
# Kernel/Docker integration tests are deliberately separate: they need root,
# Docker images and a privileged host.
# ---------------------------------------------------------------------------

GO ?= go
GOLANGCI_LINT ?= golangci-lint
COVERAGE_FILE ?= coverage.out
COVERAGE_THRESHOLDS ?= .coverage-thresholds
COVER_PKGS := $(shell $(GO) list ./... 2>/dev/null | grep -v -e /internal/proto -e /internal/testutil)

fmt:
	gofmt -w cmd internal
	$(GOLANGCI_LINT) fmt

fmt-check:
	@files="$$(gofmt -l cmd internal)"; \
	if [ -n "$$files" ]; then \
		echo "gofmt required for:"; echo "$$files"; exit 1; \
	fi

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run ./...

test-unit:
	$(GO) test -short -shuffle=on -count=1 ./...

test-race:
	$(GO) test -short -race -shuffle=on -count=1 ./...

test-integration:
	sudo env "PATH=$$PATH" GONETEM_REQUIRE_INTEGRATION=1 $(GO) test -count=1 ./...

# Kept for backwards compatibility: the full suite needs root.
test: test-integration

cover:
	$(GO) test -short -count=1 -covermode=atomic -coverprofile=$(COVERAGE_FILE) $(COVER_PKGS)
	$(GO) tool cover -func=$(COVERAGE_FILE) | tail -1

cover-check: cover
	./scripts/check-coverage.sh $(COVERAGE_FILE) $(COVERAGE_THRESHOLDS)

docs-check:
	python3 scripts/check-docs.py

govulncheck:
	./scripts/check-vulnerabilities.sh

build:
	$(GO) build ./...

check: fmt-check vet lint docs-check test-unit test-race build

.PHONY: clean proto build-amd64 build-amrv7 build-arm64 build-deb-amd64 build-deb-arm64 install-amd64 install-arm64 \
	fmt fmt-check vet lint docs-check test test-unit test-race test-integration cover cover-check govulncheck build check
