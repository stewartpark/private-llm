.PHONY: build install clean

build:
	cd cli && go build -o ../bin/private-llm .
	cd app && swift build -c release
	@rm -rf "bin/Private LLM.app"
	@mkdir -p "bin/Private LLM.app/Contents/MacOS"
	@mkdir -p "bin/Private LLM.app/Contents/Resources"
	cp app/.build/release/PrivateLLM "bin/Private LLM.app/Contents/MacOS/PrivateLLM"
	cp app/Resources/Info.plist "bin/Private LLM.app/Contents/Info.plist"
	cp bin/private-llm "bin/Private LLM.app/Contents/Resources/private-llm"
	cp app/Resources/AppIcon.icns "bin/Private LLM.app/Contents/Resources/AppIcon.icns"
	cp app/Resources/MenuBarIcon.png "bin/Private LLM.app/Contents/Resources/MenuBarIcon.png"
	cp app/Resources/MenuBarIcon@2x.png "bin/Private LLM.app/Contents/Resources/MenuBarIcon@2x.png"
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
