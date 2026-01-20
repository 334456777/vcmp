# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

vcmp 是一个用于检测视频中静态场景的命令行工具。它通过逐帧分析视频内容，使用帧差法识别画面静止的片段，并生成 FCPXML 标记文件供 Final Cut Pro X 使用。

**核心工作流程：**
1. 首次运行：分析视频文件 → 生成 `.pb.zst` 分析数据文件
2. 查看统计：直接运行 → 显示已有 `.pb.zst` 文件的分析结果和建议阈值
3. 导出标记：指定阈值 → 生成 FCPXML 文件

## 常用命令

### 构建和运行

```bash
# 生成 protobuf 代码（在修改 .proto 文件后必须执行）
make proto

# 构建二进制文件（会自动生成 protobuf）
make build

# 构建并运行
make run

# 安装到系统路径（需要 sudo）
make install

# 清理构建文件
make clean
```

### 开发工作流

```bash
# 1. 修改代码后重新构建
make build

# 2. 测试运行（假设当前目录有视频文件或 .pb.zst 文件）
./vcmp                    # 分析视频或显示统计
./vcmp 1000               # 使用阈值 1000 生成 FCPXML
./vcmp 1000 30            # 阈值 1000，最小时长 30 秒

# 3. 如果修改了 proto 文件，必须先运行
make proto
```

## 代码架构

### 单体结构

项目采用单一可执行文件架构，所有代码在 `main.go` 中，按功能模块组织：

1. **入口与路由** (`main()`, `routeCommand()`)
   - 解析命令行参数（阈值、最小时长）
   - 自动检测输入文件类型（视频文件或 `.pb.zst` 文件）
   - 路由到三种处理模式：视频分析、统计查看、FCPXML 生成

2. **视频分析核心** (`analyzeVideo()`, `processFrames()`)
   - **并发模型**：生产者-消费者模式
     - `frameProducer()`: 独立 goroutine 从视频解码帧
     - `processFrames()`: 主协程处理帧差计算
     - 通过 `frameChan` 缓冲通道（大小 `FrameBufferSize`）通信
   - **帧差算法**：
     - 转灰度图 → 绝对差值 → 二值化（阈值 25）→ 形态学腐蚀 → 统计非零像素
     - 自动裁剪底部 65/1080 区域以排除硬编码字幕干扰
   - **性能优化**：Mat 对象池减少内存分配开销

3. **静态片段检测** (`generateStaticSegments()`)
   - 扫描 `diffCounts` 数组，识别连续低于阈值的片段
   - 应用最小时长过滤（默认 20 秒）

4. **数据持久化** (`.pb.zst` 文件)
   - Protocol Buffers 定义：`proto/analysis.proto`
   - Zstd 压缩：使用 `github.com/klauspost/compress/zstd`
   - 存储内容：视频元数据、每帧差异计数、建议阈值
   - **重要**：首次分析后生成，后续操作无需重新分析视频

5. **FCPXML 生成** (`generateFCPXML()`)
   - 标记命名：`start1/stop1`, `start2/stop2`, ...
   - 正确处理 NTSC 帧率（29.97, 23.976）的有理数时间表示

### 核心常量

- `BinaryThreshold = 25`: 帧差二值化阈值
- `DefaultMinDurationSec = 20.0`: 默认最小持续时间（秒）
- `percentile = 95.0`: 用于计算建议阈值的百分位数
- `DefaultThresholdFactor = 1.5`: 建议阈值 = P95 × 1.5
- `CropIgnoreNumerator/Denominator = 65/1080`: 底部裁剪比例（字幕区域）

### Protocol Buffers

**位置**：`proto/analysis.proto`

**重要规则**：
- 修改 `.proto` 文件后**必须**执行 `make proto` 重新生成 `proto/analysis.pb.go`
- `go_package` 选项设置为 `"vcmp/proto"`
- 生成的 Go 文件使用 `paths=source_relative`（与 `.proto` 同目录）

### 文件发现策略

程序自动在当前目录搜索文件（按优先级）：
1. `.pb.zst` 文件（分析数据）
2. 视频文件（支持 `.mp4`, `.mov`, `.avi`, `.mkv`, `.wmv`, `.flv`, `.m4v`, `.mpg`, `.mpeg`）

## 关键设计决策

### 为什么使用并发处理？

视频解码是 I/O 密集型，帧处理是 CPU 密集型。生产者-消费者模式允许：
- 解码和计算并行执行
- 通过缓冲通道平滑两者速度差异
- 避免解码阻塞计算或反之

### 为什么建议阈值是 P95 × 1.5？

- P95 能过滤掉 95% 的正常画面抖动
- 乘以 1.5 提供安全余量，避免误判
- 根据实际视频内容自动调整，适应不同视频特征

### 为什么裁剪底部 65/1080？

硬编码字幕通常在画面底部，字幕闪烁会被误认为画面运动。裁剪掉这部分区域可避免干扰。

## 测试提示

- 测试不同阈值时，直接使用已有的 `.pb.zst` 文件，无需重新分析视频
- 使用 `printSegmentDurationDistribution()` 的输出来评估阈值效果
- NTSC 帧率视频（29.97, 23.976）的时间转换有特殊处理，测试时需验证
