# cursor-rebind — local build helpers
PREFIX  ?= $(HOME)/.local
BINDIR  ?= $(PREFIX)/bin
VERSION ?= dev
LDFLAGS := -s -w -X github.com/Interittus13/cursor-rebind/internal/cli.Version=$(VERSION)

.PHONY: build install uninstall test clean help

help:
	@echo "Targets:"
	@echo "  make build      Build ./bin/cursor-rebind"
	@echo "  make install    Build and install to $(BINDIR)"
	@echo "  make uninstall  Remove $(BINDIR)/cursor-rebind"
	@echo "  make test       Run tests"
	@echo ""
	@echo "Override install location: make install PREFIX=\$$HOME/.local"

build:
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/cursor-rebind ./cmd/cursor-rebind

install: build
	@mkdir -p "$(BINDIR)"
	install -m 755 bin/cursor-rebind "$(BINDIR)/cursor-rebind"
	@echo "Installed $(BINDIR)/cursor-rebind"
	@case ":$$PATH:" in *"$(BINDIR):"*) ;; *) \
	  echo ""; \
	  echo "Add to PATH if needed:"; \
	  echo "  export PATH=\"$(BINDIR):\$$PATH\""; \
	  esac

uninstall:
	rm -f "$(BINDIR)/cursor-rebind"
	@echo "Removed $(BINDIR)/cursor-rebind"

test:
	go test ./...

clean:
	rm -rf bin dist cursor-rebind
