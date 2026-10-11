BINARY := gramify
BIN_DIR := bin

.PHONY: all build win run test vet fmt clean

all: build

build:
	go build -o $(BIN_DIR)/$(BINARY) ./cmd/gramify

win:
	go build -o $(BIN_DIR)/$(BINARY).exe ./cmd/gramify

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
