# SwarmForge — build and install the single binary.
#
# Install location defaults to ~/.local/bin. Override with:
#   make install PREFIX=/usr/local        # -> /usr/local/bin
#   make install BINDIR=/somewhere/bin
#
# The binary is built into ./bin (the repo root already has a swarmforge/
# directory, so the binary can't live there).

BINARY := swarmforge
PKG    := ./cmd/swarmforge
BINDIR_LOCAL := bin
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin

.PHONY: all build install uninstall test vet clean

all: build

## build: compile the binary into ./bin/swarmforge
build:
	go build -o $(BINDIR_LOCAL)/$(BINARY) $(PKG)

## install: build straight into $(BINDIR) (default ~/.local/bin)
install:
	@mkdir -p $(BINDIR)
	go build -o $(BINDIR)/$(BINARY) $(PKG)
	@echo "Installed $(BINARY) -> $(BINDIR)/$(BINARY)"
	@case ":$$PATH:" in *":$(BINDIR):"*) ;; \
	  *) echo "Note: $(BINDIR) is not on your PATH; add it to use '$(BINARY)' directly." ;; \
	esac

## uninstall: remove the installed binary from $(BINDIR)
uninstall:
	rm -f $(BINDIR)/$(BINARY)
	@echo "Removed $(BINDIR)/$(BINARY)"

## test: run the full test suite
test:
	go test ./...

## vet: run go vet
vet:
	go vet ./...

## clean: remove the locally built binary
clean:
	rm -rf $(BINDIR_LOCAL)
