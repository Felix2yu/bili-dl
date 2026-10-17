# bili-dl —— 目标约定见 Felix2yu/.github/docs/makefile.md
#
# 七个必备目标：build / dev / test / lint / fmt / docker / clean
# 其余目标按仓库自身需要添加。
.PHONY: build dev test lint lint-strict fmt docker clean

BIN  := bin/bili-dl
MAIN := .

## 产出 $(BIN)。CLI 工具，无镜像（发布走 Release 二进制）
build:
	mkdir -p bin
	go build -trimpath -ldflags "-s -w" -o $(BIN) $(MAIN)

## CLI 没有常驻进程，dev 等同于构建后打帮助
dev: build
	./$(BIN) -h || true

test:
	go test ./...

lint:
	go vet ./...

lint-strict: lint
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run; else echo "  （未安装 golangci-lint，跳过）"; fi

fmt:
	gofmt -w $$(git ls-files '*.go')

clean:
	rm -rf bin
