# Makefile for vcmp - Video Comparison Tool

# 变量定义
BINARY_NAME := $(notdir $(CURDIR))
INSTALL_PATH=/opt/homebrew/bin
GO=go
GORELEASE=-trimpath
GOFLAGS=-ldflags="-s -w"
PROTOC=protoc
PROTOC_GEN_GO=$(shell go env GOPATH)/bin/protoc-gen-go

# 默认目标
.PHONY: all
all: build

# 生成 protobuf 代码
.PHONY: proto
proto:
	@echo ">> 生成 protobuf 代码..."
	@export PATH=$$PATH:$(shell go env GOPATH)/bin && \
	$(PROTOC) --go_out=. --go_opt=paths=source_relative analysis.proto
	@echo "✓ protobuf 代码生成完成"

# 构建二进制文件
.PHONY: build
build: proto
	@echo ">> 构建 $(BINARY_NAME)..."
	@$(GO) build $(GORELEASE) $(GOFLAGS) -o $(BINARY_NAME) .
	@echo "✓ 构建完成: $(BINARY_NAME)"

# 安装到系统路径
.PHONY: install
install: build
	@echo ">> 安装 $(BINARY_NAME) 到 $(INSTALL_PATH)..."
	@sudo mv $(BINARY_NAME) $(INSTALL_PATH)/$(BINARY_NAME)
	@echo "✓ 安装完成: $(INSTALL_PATH)/$(BINARY_NAME)"
	@echo "  现在可以在任何位置使用 '$(BINARY_NAME)' 命令"

# 卸载
.PHONY: uninstall
uninstall:
	@echo ">> 卸载 $(BINARY_NAME)..."
	@sudo rm -f $(INSTALL_PATH)/$(BINARY_NAME)
	@echo "✓ 卸载完成"

# 清理构建文件
.PHONY: clean
clean:
	@echo ">> 清理构建文件..."
	@rm -f $(BINARY_NAME)
	@rm -f analysis.pb.go
	@echo "✓ 清理完成"

# 构建并运行（用于测试）
.PHONY: run
run: build
	@echo ">> 运行 $(BINARY_NAME)..."
	@./$(BINARY_NAME)

# 显示帮助信息
.PHONY: help
help:
	@echo "vcmp - Video Comparison Tool"
	@echo ""
	@echo "可用命令:"
	@echo "  make proto      - 生成 protobuf 代码"
	@echo "  make build      - 构建二进制文件 (会自动生成 protobuf)"
	@echo "  make install    - 构建并安装到系统路径 (需要sudo)"
	@echo "  make uninstall  - 从系统路径卸载"
	@echo "  make clean      - 清理构建文件"
	@echo "  make run        - 构建并运行"
	@echo "  make help       - 显示此帮助信息"
	@echo ""
	@echo "示例:"
	@echo "  make install    # 构建并安装到 $(INSTALL_PATH)"
