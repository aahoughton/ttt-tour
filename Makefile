TTT_CONFIG_DIR ?= $(HOME)/.config/ttt

.PHONY: build test fmt install-plugin

build:
	go build -o bin/ttt-mcp ./cmd/ttt-mcp

test:
	go test ./...

fmt:
	gofmt -w cmd

# Copies the plugin into ttt's plugin folder (ttt does not follow symlinks
# there). Run it again after changing the plugin, then Plugins: Reload.
install-plugin:
	mkdir -p "$(TTT_CONFIG_DIR)/plugins"
	rm -rf "$(TTT_CONFIG_DIR)/plugins/tour"
	cp -R plugin/tour "$(TTT_CONFIG_DIR)/plugins/tour"
