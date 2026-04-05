BINARIES := loom-server loom-agent loomctl
BUILD_DIR := bin

.PHONY: all build test clean lint

all: build

build:
	@mkdir -p $(BUILD_DIR)
	@for bin in $(BINARIES); do \
		echo "Building $$bin..."; \
		go build -o $(BUILD_DIR)/$$bin ./cmd/$$bin; \
	done

test:
	go test ./...

lint:
	go vet ./...

clean:
	rm -rf $(BUILD_DIR)
