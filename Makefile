.PHONY: all build frontend test vet run clean

# 全量构建：前端产物 → embed → Go 二进制
all: frontend build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/nexus-server ./cmd/server

frontend:
	cd web && npm install && npm run build

test:
	go test ./...

vet:
	go vet ./...

run:
	go run ./cmd/server

clean:
	rm -rf bin
