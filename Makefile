BINARY  := ir-hub
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
DIST_DIR := dist

# macOS Developer ID signing / notarization (see nlink-jp/.github
# CONVENTIONS.md §Code Signing). Defaults match any Developer ID
# Application cert in the keychain and the org-standard notary
# profile. Builds without these fall back to ad-hoc / un-notarized
# with a one-line warning — see scripts/codesign-darwin.sh.
CODESIGN_IDENTITY ?= Developer ID Application
NOTARY_PROFILE    ?= nlink-jp-notary

.PHONY: build build-all package test clean

build:
	@mkdir -p $(DIST_DIR)
	go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY) .
	@scripts/codesign-darwin.sh $(DIST_DIR)/$(BINARY) "$(CODESIGN_IDENTITY)"

build-all:
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-linux-amd64   .
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-linux-arm64   .
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-darwin-amd64  .
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-darwin-arm64  .
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-windows-amd64.exe .
	@scripts/codesign-darwin.sh $(DIST_DIR)/$(BINARY)-darwin-amd64 "$(CODESIGN_IDENTITY)"
	@scripts/codesign-darwin.sh $(DIST_DIR)/$(BINARY)-darwin-arm64 "$(CODESIGN_IDENTITY)"

## package: Build all platforms, zip with version suffix, and
## notarize darwin builds. Zip contents carry the canonical binary
## name (arch suffix lives only in the zip filename).
package: build-all
	@cd $(DIST_DIR) && for f in $(BINARY)-*; do \
		case "$$f" in *.zip) continue ;; esac; \
		suffix=$${f#$(BINARY)-}; \
		suffix=$${suffix%%.exe}; \
		ext=""; case "$$f" in *.exe) ext=".exe" ;; esac; \
		cp ../README.md .; \
		stage="$$(dirname "$$f")/_pkg"; rm -rf "$$stage"; mkdir -p "$$stage"; \
		cp "$$f" "$$stage/$(BINARY)$$ext"; \
		zip -j "$(BINARY)-$(VERSION)-$${suffix}.zip" "$$stage/$(BINARY)$$ext" README.md; \
		rm -rf "$$stage"; \
		rm -f README.md; \
	done
	@scripts/notarize-darwin.sh $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-amd64.zip "$(NOTARY_PROFILE)"
	@scripts/notarize-darwin.sh $(DIST_DIR)/$(BINARY)-$(VERSION)-darwin-arm64.zip "$(NOTARY_PROFILE)"

test:
	go test ./...

clean:
	rm -rf $(DIST_DIR)
