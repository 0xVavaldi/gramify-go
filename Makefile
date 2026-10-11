BINARY := gramify
BIN_DIR := bin

.PHONY: all build win run test vet fmt clean docker docker-linux docker-windows

all: build

build:
	go build -o $(BIN_DIR)/$(BINARY) ./cmd/gramify

win:
	go build -o $(BIN_DIR)/$(BINARY).exe ./cmd/gramify

# Build both Linux and Windows binaries in containers (no Go toolchain on the
# host required) and export them to ./$(BIN_DIR) via docker-bake.hcl.
docker:
	docker buildx bake

docker-linux:
	docker buildx build -f Dockerfile.linux --output ./$(BIN_DIR) .

docker-windows:
	docker buildx build -f Dockerfile.windows --output ./$(BIN_DIR) .

run: build
	./$(BIN_DIR)/$(BINARY) $(ARGS)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w ./cmd ./internal

clean:
	rm -rf $(BIN_DIR)
