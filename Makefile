TTT_CONFIG_DIR ?= $(HOME)/.config/ttt

.PHONY: build test fmt install-plugin

build:
	go build -o bin/ttt-mcp ./cmd/ttt-mcp

test:
	go test ./...

fmt:
	gofmt -w cmd

# Copies the ttt plugin into ttt's plugin folder, for working on it (ttt does
# not follow symlinks there). Run it again after changing the plugin, then
# Plugins: Reload.
install-plugin:
	mkdir -p "$(TTT_CONFIG_DIR)/plugins/ttt-tour"
	cp init.lua plugin.ttt.json "$(TTT_CONFIG_DIR)/plugins/ttt-tour/"
