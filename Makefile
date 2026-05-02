BINARY    := mini-forwarder
VERSION   := $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
BUILD_DATE:= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS   := -w -s \
             -X main.Version=$(VERSION) \
             -X main.GitCommit=$(VERSION) \
             -X main.BuildDate=$(BUILD_DATE)

.PHONY: build clean test lint docker install fmt tidy help

build: ## 编译二进制
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/mini-forwarder

clean: ## 清理构建产物
	rm -f $(BINARY)

test: ## 运行测试
	go test -race -covermode=atomic -coverprofile=coverage.out ./internal/...

lint: ## 代码检查
	go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	fi

docker: ## 构建 Docker 镜像
	docker build \
		-f deployments/Dockerfile \
		--build-arg VERSION=$(VERSION) \
		--build-arg GIT_COMMIT=$(VERSION) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t mini-forwarder:$(VERSION) \
		-t mini-forwarder:latest \
		..

install: build ## 安装到 /usr/local/bin
	install -m 755 $(BINARY) /usr/local/bin/$(BINARY)

fmt: ## 格式化代码
	go fmt ./...

tidy: ## 整理依赖
	go mod tidy

help: ## 显示帮助
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'
