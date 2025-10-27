# BACnet Server Makefile

# 默认目标
all: build

# 构建项目
build:
	@echo "Building BACnet Server..."
	@go build -o bacnet-tool ./cmd/tool

# 运行项目
run:
	@echo "Starting BACnet Server..."
	@go run cmd/tool/main.go

# 清理构建文件
clean:
	@echo "Cleaning build files..."
	@rm -f bacnet-tool

# 更新依赖
update:
	@echo "Updating dependencies..."
	@go mod tidy

# 验证代码
check:
	@echo "Checking code..."
	@go vet ./...

# 帮助信息
help:
	@echo "BACnet Server Build Targets:"
	@echo "  make          - Build the project"
	@echo "  make build    - Build the project"
	@echo "  make run      - Run the server"
	@echo "  make clean    - Clean build files"
	@echo "  make update   - Update dependencies"
	@echo "  make check    - Check code with go vet"
	@echo "  make help     - Show this help message"