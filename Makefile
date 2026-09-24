# Variables
BINARY_NAME=bleprox
GO?=go
PREFIX?=/usr/local
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-X main.Version=$(VERSION)"

.PHONY: all build clean install run monitor test help

## help ~ Display available make targets
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s '~'


all: build strip

## build ~ Build the bleprox binary
build:
	@echo "Building $(BINARY_NAME) $(VERSION)..."
	$(GO) build $(LDFLAGS) -o $(BINARY_NAME) main.go

## run ~ Run daemon mode (requires MAC argument, e.g., make run MAC=AA:BB:CC:DD:EE:FF)
run:
	@if [ -z "$(MAC)" ]; then \
		echo "Error: MAC argument is required. Usage: make run MAC=AA:BB:CC:DD:EE:FF"; \
		exit 1; \
	fi
	$(GO) run main.go -mac $(MAC)

## monitor ~ Run BLE monitor tool (optional MAC, e.g., make monitor or make monitor MAC=AA:BB:CC:DD:EE:FF)
monitor:
	@if [ -n "$(MAC)" ]; then \
		$(GO) run main.go -monitor -mac $(MAC); \
	else \
		$(GO) run main.go -monitor; \
	fi

## test ~ Run tests
test:
	$(GO) test -v ./...

## clean ~ Remove built binaries
clean:
	@echo "Cleaning..."
	rm -f $(BINARY_NAME)

## strip ~ Remove debug info
strip:
	@echo "Stripping..."
	strip $(BINARY_NAME)

## install ~ Install binary to PREFIX/bin (default /usr/local/bin)
install: build strip
	@echo "Installing $(BINARY_NAME) to $(PREFIX)/bin..."
	install -d $(DESTDIR)$(PREFIX)/bin
	install -m 755 $(BINARY_NAME) $(DESTDIR)$(PREFIX)/bin/

## install-service ~ Install systemd user service file (e.g. make install-service MAC=AA:BB:CC:DD:EE:FF)
install-service: build
	@echo "Installing systemd user service..."
	mkdir -p $(HOME)/.config/systemd/user
	@if [ -n "$(MAC)" ]; then \
		sed "s/AA:BB:CC:DD:EE:FF/$(MAC)/g" bleprox.service > $(HOME)/.config/systemd/user/bleprox.service; \
	else \
		cp bleprox.service $(HOME)/.config/systemd/user/bleprox.service; \
	fi
	systemctl --user daemon-reload
	@echo "Service installed to  ~/.config/systemd/user/bleprox.service"
	@echo "To enable and start: systemctl --user enable --now bleprox"

