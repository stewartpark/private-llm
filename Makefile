.PHONY: build install clean

build:
	cd cli && go build -o ../bin/private-llm .

install: build
	@mkdir -p ~/.local/bin
	cp bin/private-llm ~/.local/bin/private-llm
	@echo "Installed to ~/.local/bin/private-llm"

clean:
	rm -rf bin/
