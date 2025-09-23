package main

import (
	"fmt"
	"os"
	"path/filepath"
	"srt2fcpxml/core"
	"strconv"
	"strings"

	"github.com/asticode/go-astisub"
)

func main() {
	args := os.Args[1:] // 获取除程序名外的所有参数

	// 默认参数
	var srtFile string
	var frameDuration interface{} = 30
	width := 1920
	height := 1080

	// 根据参数数量解析不同的模式
	switch len(args) {
	case 0:
		// ./srt2fcpxml - 自动寻找同目录的srt文件，使用默认参数
		var err error
		srtFile, err = findSrtFileInCurrentDir()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			printUsage()
			os.Exit(1)
		}
		frameDuration = 30

	case 1:
		// ./srt2fcpxml 60 - 使用指定帧率
		frameRate, err := parseFrameRate(args[0])
		if err != nil {
			fmt.Printf("Error parsing frame rate: %v\n", err)
			printUsage()
			os.Exit(1)
		}
		frameDuration = frameRate

		srtFile, err = findSrtFileInCurrentDir()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			printUsage()
			os.Exit(1)
		}

	case 3:
		// ./srt2fcpxml 1920 1080 30 - 使用指定分辨率和帧率
		var err error
		width, err = strconv.Atoi(args[0])
		if err != nil {
			fmt.Printf("Error parsing width: %v\n", err)
			printUsage()
			os.Exit(1)
		}

		height, err = strconv.Atoi(args[1])
		if err != nil {
			fmt.Printf("Error parsing height: %v\n", err)
			printUsage()
			os.Exit(1)
		}

		frameDuration, err = parseFrameRate(args[2])
		if err != nil {
			fmt.Printf("Error parsing frame rate: %v\n", err)
			printUsage()
			os.Exit(1)
		}

		srtFile, err = findSrtFileInCurrentDir()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			printUsage()
			os.Exit(1)
		}

	default:
		fmt.Println("Error: Invalid number of arguments")
		printUsage()
		os.Exit(1)
	}

	// 打开SRT文件
	f, err := astisub.OpenFile(srtFile)
	if err != nil {
		fmt.Printf("Error opening SRT file %s: %v\n", srtFile, err)
		os.Exit(1)
	}

	// 生成XML输出
	out := `<?xml version="1.0" encoding="UTF-8" ?>
	<!DOCTYPE fcpxml>
	
	`

	project, path := getPath(srtFile)
	result, err := core.Srt2FcpXmlExport(project, frameDuration, f, width, height)
	if err != nil {
		fmt.Printf("Error generating FCPXML: %v\n", err)
		os.Exit(1)
	}

	out += string(result)
	targetFile := fmt.Sprintf("%s/%s.fcpxml", path, project)

	fd, err := os.Create(targetFile)
	if err != nil {
		fmt.Printf("Error creating output file: %v\n", err)
		os.Exit(1)
	}
	defer fd.Close()

	_, err = fd.Write([]byte(out))
	if err != nil {
		fmt.Printf("Error writing output file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully converted %s to %s\n", srtFile, targetFile)
	fmt.Printf("Settings: %dx%d resolution, %v fps\n", width, height, frameDuration)
}

func getPath(filePath string) (projectName, targetPath string) {
	path, _ := filepath.Abs(filePath)
	parts := strings.Split(path, "/")
	projectName = func(file string) string {
		parts := strings.Split(file, ".")
		return strings.Join(parts[0:len(parts)-1], ".")
	}(parts[len(parts)-1])
	targetPath = func(parts []string) string {
		return strings.Join(parts, "/")
	}(parts[0 : len(parts)-1])
	return
}

// findSrtFileInCurrentDir 在当前目录查找SRT文件
func findSrtFileInCurrentDir() (string, error) {
	currentDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %v", err)
	}

	files, err := os.ReadDir(currentDir)
	if err != nil {
		return "", fmt.Errorf("failed to read current directory: %v", err)
	}

	var srtFiles []string
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(strings.ToLower(file.Name()), ".srt") {
			srtFiles = append(srtFiles, file.Name())
		}
	}

	if len(srtFiles) == 0 {
		return "", fmt.Errorf("no SRT files found in current directory")
	}

	if len(srtFiles) == 1 {
		return filepath.Join(currentDir, srtFiles[0]), nil
	}

	// 如果有多个SRT文件，返回第一个
	fmt.Printf("Found multiple SRT files, using: %s\n", srtFiles[0])
	return filepath.Join(currentDir, srtFiles[0]), nil
}

// parseFrameRate 解析帧率参数
func parseFrameRate(frameRateStr string) (interface{}, error) {
	// 支持的帧率：23.98、24、25、29.97、30、50、59.94、60
	supportedRates := map[string]interface{}{
		"23.98": 23.98,
		"24":    24,
		"25":    25,
		"29.97": 29.97,
		"30":    30,
		"50":    50,
		"59.94": 59.94,
		"60":    60,
	}

	if rate, exists := supportedRates[frameRateStr]; exists {
		return rate, nil
	}

	// 尝试解析为浮点数
	if strings.Contains(frameRateStr, ".") {
		rate, err := strconv.ParseFloat(frameRateStr, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid frame rate format: %s", frameRateStr)
		}
		return rate, nil
	}

	// 尝试解析为整数
	rate, err := strconv.Atoi(frameRateStr)
	if err != nil {
		return nil, fmt.Errorf("invalid frame rate format: %s", frameRateStr)
	}

	return rate, nil
}

// printUsage 打印使用说明
func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  ./srt2fcpxml                    - Auto find SRT file in current directory, use 1920x1080@30fps")
	fmt.Println("  ./srt2fcpxml <framerate>        - Auto find SRT file, use specified framerate with 1920x1080")
	fmt.Println("  ./srt2fcpxml <width> <height> <framerate> - Auto find SRT file, use specified resolution and framerate")
	fmt.Println("")
	fmt.Println("Supported frame rates: 23.98, 24, 25, 29.97, 30, 50, 59.94, 60")
	fmt.Println("")
	fmt.Println("Examples:")
	fmt.Println("  ./srt2fcpxml                    - Convert with 1920x1080@30fps")
	fmt.Println("  ./srt2fcpxml 60                 - Convert with 1920x1080@60fps")
	fmt.Println("  ./srt2fcpxml 1920 1080 29.97    - Convert with 1920x1080@29.97fps")
}
