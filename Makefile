.PHONY: all build-all build-go build-mac-app download-pulumi install clean

PULUMI_VERSION := 3.161.0

UNAME_S := $(shell uname -s)

# Cross-platform: download Pulumi for current OS/arch
download-pulumi:
	@if [ ! -f "bin/pulumi" ]; then \
		echo "Downloading Pulumi CLI v$(PULUMI_VERSION)..."; \
		mkdir -p bin; \
		OS=$$(uname -s | tr '[:upper:]' '[:lower:]'); \
		ARCH=$$(uname -m); \
		if [ "$$ARCH" = "arm64" ]; then PULUMI_ARCH="arm64"; else if [ "$$ARCH" = "x86_64" ]; then PULUMI_ARCH="amd64"; else PULUMI_ARCH="amd64"; fi; fi; \
		curl -sL "https://get.pulumi.com/releases/sdk/pulumi-v$(PULUMI_VERSION)-$$OS-$$PULUMI_ARCH.tar.gz" -o /tmp/pulumi.tar.gz; \
		tar -xzf /tmp/pulumi.tar.gz -C /tmp pulumi/pulumi; \
		mv /tmp/pulumi/pulumi bin/pulumi; \
		rm -rf /tmp/pulumi /tmp/pulumi.tar.gz; \
		echo "Downloaded Pulumi CLI to bin/pulumi"; \
	fi

# Cross-platform: build Go binary
build-go: download-pulumi
	cd cli && go build -o ../bin/private-llm .
	@echo "Built: bin/private-llm"

# macOS-specific: build Swift app and bundle
ifeq ($(UNAME_S),Darwin)
build-mac-app: build-go
	cd app && swift build -c release
	@rm -rf "bin/Private LLM.app"
	@mkdir -p "bin/Private LLM.app/Contents/MacOS"
	@mkdir -p "bin/Private LLM.app/Contents/Resources"
	@mkdir -p "bin/Private LLM.app/Contents/Frameworks"
	cp app/.build/release/PrivateLLM "bin/Private LLM.app/Contents/MacOS/PrivateLLM"
	install_name_tool -delete_rpath @loader_path "bin/Private LLM.app/Contents/MacOS/PrivateLLM" 2>/dev/null || true
	install_name_tool -add_rpath @executable_path/../Frameworks "bin/Private LLM.app/Contents/MacOS/PrivateLLM"
	cp app/Resources/Info.plist "bin/Private LLM.app/Contents/Info.plist"
	cp bin/private-llm "bin/Private LLM.app/Contents/Resources/private-llm"
	cp bin/pulumi "bin/Private LLM.app/Contents/Resources/pulumi"
	cp app/Resources/AppIcon.icns "bin/Private LLM.app/Contents/Resources/AppIcon.icns"
	cp app/Resources/MenuBarIcon.png "bin/Private LLM.app/Contents/Resources/MenuBarIcon.png"
	cp app/Resources/MenuBarIcon@2x.png "bin/Private LLM.app/Contents/Resources/MenuBarIcon@2x.png"
	@echo "Copying Sparkle framework..."
	ditto app/.build/release/Sparkle.framework "bin/Private LLM.app/Contents/Frameworks/Sparkle.framework"
	ln -sf ../Frameworks/Sparkle.framework/Versions/B/Autoupdate "bin/Private LLM.app/Contents/MacOS/Autoupdate" 2>/dev/null || true
	@echo "Built: bin/Private LLM.app"

build-all: build-mac-app
else
build-mac-app:
	@echo "macOS app building only available on Darwin"

build-all: build-go
endif

# Default target: conditional based on platform
all: build-all

# Install target (conditional)
ifeq ($(UNAME_S),Darwin)
install: build-all
	@mkdir -p ~/.local/bin
	cp bin/private-llm ~/.local/bin/private-llm
	@rm -rf "/Applications/Private LLM.app"
	cp -R "bin/Private LLM.app" "/Applications/Private LLM.app"
	@echo "Installed CLI to ~/.local/bin/private-llm"
	@echo "Installed app to /Applications/Private LLM.app"
else
install: build-all
	@mkdir -p ~/.local/bin
	cp bin/private-llm ~/.local/bin/private-llm
	@echo "Installed CLI to ~/.local/bin/private-llm"
endif

clean:
	rm -rf bin/
	rm -rf app/.build/
