.PHONY: help build run test clean

help:
	@echo "GoPrint - Golang打印后端程序"
	@echo "可用命令:"
	@echo "  make build      - 构建项目"
	@echo "  make run        - 运行项目"
	@echo "  make test       - 运行测试"
	@echo "  make clean      - 清空构建文件"
	@echo "  make deps       - 下载依赖"

deps:
	go mod download
	go mod tidy

build: deps
	go build -o bin/goprint main.go

run: deps
	go run main.go

test: deps
	go test ./...

clean:
	rm -rf bin/
	go clean
