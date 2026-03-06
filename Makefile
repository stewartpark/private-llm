.PHONY: build install clean download-sparkle download-pulumi

PULUMI_VERSION := 3.161.0

download-sparkle:
	@if [ ! -d "app/Resources/Sparkle.framework" ]; then \
		echo "Downloading Sparkle framework..."; \
		curl -sL https://github.com/sparkle-project/Sparkle/releases/download/2.9.0/Sparkle-for-Swift-Package-Manager.zip -o /tmp/sparkle-spm.zip; \
		unzip -q /tmp/sparkle-spm.zip -d /tmp/sparkle_extracted; \
        cp -r /tmp/sparkle_extracted/Sparkle.xcframework/macos-arm64_x86_64/Sparkle.framework "app/Resources/"; \
		rm -rf /tmp/sparkle_spm.zip /tmp/sparkle_extracted; \
		echo "Downloaded Sparkle to app/Resources/"; \
	fi

download-pulumi:
	@if [ ! -f "bin/pulumi" ]; then \
		echo "Downloading Pulumi CLI v$(PULUMI_VERSION)..."; \
		mkdir -p bin; \
		ARCH=$$(uname -m); \
		if [ "$$ARCH" = "arm64" ]; then ARCH="arm64"; else ARCH="x64"; fi; \
		curl -sL "https://get.pulumi.com/releases/sdk/pulumi-v$(PULUMI_VERSION)-darwin-$$ARCH.tar.gz" -o /tmp/pulumi.tar.gz; \
		tar -xzf /tmp/pulumi.tar.gz -C /tmp pulumi/pulumi; \
		mv /tmp/pulumi/pulumi bin/pulumi; \
		rm -rf /tmp/pulumi /tmp/pulumi.tar.gz; \
		echo "Downloaded Pulumi CLI to bin/pulumi"; \
	fi

build: download-sparkle download-pulumi
	cd cli && go build -o ../bin/private-llm .
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
	cp -r app/Resources/Sparkle.framework "bin/Private LLM.app/Contents/Frameworks/"
	ln -sf ../Frameworks/Sparkle.framework/Versions/B/Autoupdate "bin/Private LLM.app/Contents/MacOS/Autoupdate" 2>/dev/null || true
	@echo "Built: bin/private-llm and bin/Private LLM.app"

install: build
	@mkdir -p ~/.local/bin
	cp bin/private-llm ~/.local/bin/private-llm
	@rm -rf "/Applications/Private LLM.app"
	cp -R "bin/Private LLM.app" "/Applications/Private LLM.app"
	@echo "Installed CLI to ~/.local/bin/private-llm"
	@echo "Installed app to /Applications/Private LLM.app"

clean:
	rm -rf bin/
	rm -rf app/.build/
