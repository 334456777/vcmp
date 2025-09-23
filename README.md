# srt2fcpxml
Convert srt subtitle file to final cut pro subtitle file(fcpxml)

This software uses final cut pro X 10.4.6 version fcpxml file as template development, if there is any problem, please upgrade to the corresponding version.

srt 字幕文件转为final cut pro 字幕文件(fcpxml)

本软件使用 final cut pro X 10.4.6 版本的 fcpxml 文件作为模版开发，如果有问题请升级到对应版本


## Compile (编译)
First, you need to have Go language development environment
Then execute `make` command in the project directory and generate `srt2fcpxml` executable file in `build` directory.

首先需要有 Go 语言开发环境
然后在项目目录下执行`make`命令后在`build`目录下生成`srt2fcpxml`执行文件。

## Download (下载)
Users who do not want to compile can download the [executable file](https://github.com/334456777/srt2fcpxml/releases) directly.

不想编译的用户可以直接下载[执行文件](https://github.com/334456777/srt2fcpxml/releases)。

### Supported Platforms (支持平台)
- Windows (AMD64, ARM64)
- macOS (Intel, Apple Silicon) 
- Linux (AMD64, ARM64)

## Use (使用)
First you need to give the program execute permission `chmod +x ./srt2fcpxml`

首先需要赋予程序执行权限 `chmod +x ./srt2fcpxml`

The program will automatically find SRT files in the current directory and convert them.

程序会自动在当前目录中查找SRT文件并进行转换。

### Usage Patterns (使用模式)

```bash
# Auto find SRT file with default settings (1920x1080@30fps)
# 自动查找SRT文件并使用默认设置 (1920x1080@30帧)
$ ./srt2fcpxml

# Auto find SRT file with specified frame rate (1920x1080@60fps)
# 自动查找SRT文件并使用指定帧率 (1920x1080@60帧)
$ ./srt2fcpxml 60

# Auto find SRT file with custom resolution and frame rate
# 自动查找SRT文件并使用自定义分辨率和帧率
$ ./srt2fcpxml 1920 1080 29.97
```

### Supported Frame Rates (支持的帧率)
23.98, 24, 25, 29.97, 30, 50, 59.94, 60

## Execution Examples (执行示例)

```bash
# Convert with default settings (默认设置转换)
$ ./srt2fcpxml

# Convert with 60fps (60帧转换)
$ ./srt2fcpxml 60

# Convert with custom settings (自定义设置转换)
$ ./srt2fcpxml 1920 1080 29.97
```

The `fcpxml` file named with srt file name will be generated automatically in the same directory as the srt file.

会在srt文件所在目录中自动生成以srt文件名命名的`fcpxml`文件。

## Development (开发)

### Local Build (本地构建)
```bash
# Build for current platform
go build -o srt2fcpxml cmd/main.go

# Build for all platforms using the build script
./build.sh v1.0.0
```

### Release Process (发布流程)
1. Create and push a version tag:
   ```bash
   git tag v1.0.0
   git push origin v1.0.0
   ```

2. GitHub Actions will automatically:
   - ✅ Run tests on Ubuntu
   - 🔨 Build binaries using native runners for better performance:
     - **Windows** builds on `windows-latest`
     - **macOS** builds on `macos-latest` 
     - **Linux** builds on `ubuntu-latest`
   - 🎯 Support for AMD64 and ARM64 architectures
   - 📦 Create GitHub release with all binaries
   - 🔐 Generate SHA256 checksums for security
   - ✨ Test native binaries for quality assurance

### Workflow Triggers (工作流触发条件)
- **Tag push** (`v*`) → Full build + release
- **Branch push** (main/master/develop) → Build only 
- **Pull Request** → Test only
- **Manual trigger** → Custom version build

### Manual Trigger (手动触发)
You can manually trigger builds through GitHub Actions interface with custom version numbers.

你可以通过GitHub Actions界面手动触发构建并指定自定义版本号。
