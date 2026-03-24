// vcmp is a video static scene detection tool.
// Automatically detects static frame segments in videos through frame-by-frame analysis,
// and generates FCPXML marker files for use with Final Cut Pro X.
//
// Workflow:
//
//  1. First run: Analyze video file, generate .pb.zst analysis data file
//  2. View statistics: Run directly to view analysis results from existing .pb.zst file
//  3. Export markers: Specify threshold to generate FCPXML file
//
// Usage:
//
//	vcmp                                # Analyze video to generate .pb.zst or display statistics
//	vcmp <threshold>                    # Use .pb.zst to generate FCPXML (threshold)
//	vcmp <threshold> <min_duration>     # Specify threshold and minimum duration (seconds)
//
// The program automatically detects video files (.mp4, .mov, etc.) or .pb.zst analysis files
// in the current directory and processes them.
package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"vcmp/proto"

	"github.com/klauspost/compress/zstd"
	"github.com/schollz/progressbar/v3"
	"gocv.io/x/gocv"
	pb "google.golang.org/protobuf/proto"
)

// ---------------------------------------------------------
// Constants Definition
// ---------------------------------------------------------

const (
	// MarkerStartPrefix is the prefix for start markers in FCPXML
	MarkerStartPrefix = "start"

	// MarkerStopPrefix is the prefix for stop markers in FCPXML
	MarkerStopPrefix = "stop"

	// CropIgnoreNumerator defines the numerator for cropping the bottom of the frame
	CropIgnoreNumerator = 65

	// CropIgnoreDenominator defines the denominator for cropping the bottom of the frame
	// Used to exclude hardcoded subtitle or watermark regions to avoid interfering with static detection
	CropIgnoreDenominator = 1080

	// ProgressBarWidth defines the character width of the progress bar
	ProgressBarWidth = 30

	// DefaultMinDurationSec is the default minimum duration (seconds) to be judged as a static segment
	DefaultMinDurationSec = 20.0

	// BinaryThreshold is the threshold for frame difference binarization
	// Pixel differences exceeding this value are considered motion
	BinaryThreshold = 25

	// FrameBufferSize defines the size of the frame buffer
	FrameBufferSize = 5

	// ProducerWorkingMat is the currently being read frame
	ProducerWorkingMat = 1

	//ConsumerWorkingMat is the retained previous frame
	ConsumerWorkingMat = 1

	// percentile defines the percentile used to calculate the suggested threshold
	percentile = 95.0

	// DefaultThresholdFactor 是百分位值的倍数系数
	// 建议阈值 = percentile * DefaultThresholdFactor
	DefaultThresholdFactor = 1.5
)

// ---------------------------------------------------------
// 核心领域类型
// ---------------------------------------------------------

// DecodedFrame 表示一个已解码的视频帧及其元数据
type DecodedFrame struct {
	Frame       gocv.Mat // 解码后的帧矩阵
	FrameNum    int      // 帧序号（从1开始）
	IsLastFrame bool     // 是否为最后一帧（用作哨兵值）
}

// StaticSegment 表示一个连续的静态片段
type StaticSegment struct {
	StartFrame     int // 起始帧号（从1开始）
	DurationFrames int // 片段持续帧数
}

// ---------------------------------------------------------
// 入口点
// ---------------------------------------------------------

func main() {
	flag.Parse()
	args := flag.Args()

	var diffCountThreshold float64 = -1
	minDurationSec := DefaultMinDurationSec

	if len(args) > 0 {
		if val, err := strconv.ParseFloat(args[0], 64); err == nil {
			diffCountThreshold = val
		}
	}

	if len(args) > 1 {
		if val, err := strconv.ParseFloat(args[1], 64); err == nil {
			minDurationSec = val
		}
	}

	finalInputPath, isGobInput := detectInputFile()
	if finalInputPath == "" {
		printUsageAndExit()
	}

	if err := routeCommand(finalInputPath, isGobInput, diffCountThreshold, minDurationSec); err != nil {
		fmt.Printf("错误: %v\n", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------
// 命令路由
// ---------------------------------------------------------

// routeCommand 根据输入类型和参数决定执行路径
// 支持三种模式：视频分析、pb统计查看、FCPXML生成
func routeCommand(inputPath string, isPbInput bool, threshold, minDuration float64) error {
	if isPbInput && threshold >= 0 {
		return handlePbToFCPXML(inputPath, threshold, minDuration)
	}

	if isPbInput {
		return handlePbAnalysis(inputPath)
	}

	return handleVideoAnalysis(inputPath)
}

// detectInputFile 在当前目录搜索 pb 文件或视频文件
// 优先查找 pb 文件，找不到则查找视频文件
// 返回文件路径和是否为 pb 文件的标识
func detectInputFile() (string, bool) {
	foundPb := findPbInCurrentDir()
	if foundPb != "" {
		return foundPb, true
	}

	foundVideo := findVideoInCurrentDir()
	if foundVideo != "" {
		return foundVideo, false
	}

	return "", false
}

// printUsageAndExit 打印使用说明并退出程序
func printUsageAndExit() {
	fmt.Println("错误: 当前目录未找到 .pb.zst 或视频文件")
	fmt.Println()
	fmt.Println("用法:")
	fmt.Println("  vcmp                                # 分析视频生成.pb.zst或显示统计")
	fmt.Println("  vcmp <threshold>                    # 使用.pb.zst生成FCPXML (阈值)")
	fmt.Println("  vcmp <threshold> <min_duration>     # 指定阈值和最小持续时间(秒)")
	fmt.Println()
	os.Exit(1)
}

// ---------------------------------------------------------
// 高层处理
// ---------------------------------------------------------

// handleVideoAnalysis 执行视频分析并将结果保存为 pb.zst 文件
// 这是首次处理视频时的入口点
func handleVideoAnalysis(videoPath string) error {
	fmt.Printf(">> 分析视频: %s\n", videoPath)

	result, err := analyzeVideo(videoPath)
	if err != nil {
		return fmt.Errorf("分析视频失败: %w", err)
	}

	outputPath := generatePbFilename(videoPath)
	if err := saveAnalysisToProto(result, outputPath); err != nil {
		return fmt.Errorf("保存文件失败 (%s): %w", outputPath, err)
	}

	printAnalysisResults(result)
	return nil
}

// handlePbToFCPXML 从 pb.zst 文件加载分析数据并生成 FCPXML 文件
// 这是生成最终标记文件的入口点
func handlePbToFCPXML(pbPath string, diffCountThreshold, minDurationSec float64) error {
	fmt.Printf(">> 加载分析数据: %s\n", pbPath)

	result, err := loadAnalysisFromProto(pbPath)
	if err != nil {
		return fmt.Errorf("加载文件失败 (%s): %w", pbPath, err)
	}

	segments := generateStaticSegments(result.DiffCounts, diffCountThreshold, minDurationSec, result.Fps)
	if len(segments) == 0 {
		return fmt.Errorf("未找到静态片段 (阈值: %.0f, 最小时长: %.0f秒)", diffCountThreshold, minDurationSec)
	}

	fmt.Printf("\n阈值 %.0f, 最小时长 %.0fs 的片段分布:\n", diffCountThreshold, minDurationSec)
	printSegmentDurationDistribution(segments, result.Fps)
	fmt.Println()

	outputPath := generateFCPXMLFilename(result.VideoFile, diffCountThreshold)

	if err := generateFCPXML(segments, result, outputPath); err != nil {
		return fmt.Errorf("生成FCPXML文件失败 (%s): %w", outputPath, err)
	}

	fmt.Printf("✓  FCPXML已生成 -> %s\n", outputPath)
	fmt.Printf("   检测到 %d 个静态片段\n", len(segments))

	return nil
}

// handlePbAnalysis 从 pb.zst 文件加载并显示分析统计结果
func handlePbAnalysis(pbPath string) error {
	fmt.Printf(">> 加载分析数据: %s\n", pbPath)

	result, err := loadAnalysisFromProto(pbPath)
	if err != nil {
		return fmt.Errorf("加载文件失败 (%s): %w", pbPath, err)
	}

	printAnalysisResults(result)
	return nil
}

// ---------------------------------------------------------
// 分析结果显示
// ---------------------------------------------------------

// printAnalysisResults 显示分析结果，包括建议阈值和片段时长分布
func printAnalysisResults(result *proto.AnalysisResult) {
	threshold := result.SuggestedThreshold
	segments := generateStaticSegments(result.DiffCounts, threshold, 0.0, result.Fps)

	fmt.Printf("\n阈值为 %.0f 时的连续静止区间分布:\n", threshold)
	printSegmentDurationDistribution(segments, result.Fps)
}

// calculateSuggestedThreshold 基于百分位数和系数计算建议阈值
// 使用 percentileValue * DefaultThresholdFactor 作为默认策略，可以过滤掉大部分正常的画面抖动
func calculateSuggestedThreshold(diffCounts []uint32) float64 {
	percentileValue := computePercentile(diffCounts, percentile)
	return math.Round(percentileValue * DefaultThresholdFactor)
}

// ---------------------------------------------------------
// 视频分析核心
// ---------------------------------------------------------

// analyzeVideo 对视频进行逐帧分析，检测画面运动
// 使用帧差法：比较相邻帧的灰度图差异，统计变化像素数量
// 返回包含每帧差异计数的分析结果
func analyzeVideo(videoPath string) (*proto.AnalysisResult, error) {
	video, err := gocv.VideoCaptureFileWithAPI(videoPath, gocv.VideoCaptureAVFoundation)
	if err != nil {
		return nil, fmt.Errorf("打开视频失败: %w", err)
	}
	defer func() {
		_ = video.Close()
	}()

	metadata := extractVideoMetadata(video, videoPath)
	cropHeight := calculateCropHeight(int(metadata.Height))

	matPool := createMatPool(ProducerWorkingMat + FrameBufferSize + ConsumerWorkingMat)
	defer func() {
		closeMatPool(matPool)
	}()

	frameChan := make(chan DecodedFrame, FrameBufferSize)
	go frameProducer(video, frameChan, matPool, int(metadata.Width), cropHeight)

	diffCounts, err := processFrames(frameChan, matPool, int(metadata.TotalFrames))
	if err != nil {
		return nil, fmt.Errorf("处理帧失败: %w", err)
	}

	suggestedThreshold := calculateSuggestedThreshold(diffCounts)
	metadata.DiffCounts = diffCounts
	metadata.SuggestedThreshold = suggestedThreshold

	return metadata, nil
}

// extractVideoMetadata 从已打开的视频对象中提取元数据
func extractVideoMetadata(video *gocv.VideoCapture, filePath string) *proto.AnalysisResult {
	return &proto.AnalysisResult{
		VideoFile:   filePath,
		Fps:         video.Get(gocv.VideoCaptureFPS),
		Width:       int32(video.Get(gocv.VideoCaptureFrameWidth)),
		Height:      int32(video.Get(gocv.VideoCaptureFrameHeight)),
		TotalFrames: int32(video.Get(gocv.VideoCaptureFrameCount)),
	}
}

// calculateCropHeight 根据忽略比例计算裁剪高度
// 裁剪掉画面底部区域（通常是字幕），避免字幕变化影响静态检测
// 如果裁剪后高度小于原高度的一半，则不进行裁剪
func calculateCropHeight(height int) int {
	bottomMaskHeight := height * CropIgnoreNumerator / CropIgnoreDenominator
	cropHeight := height - bottomMaskHeight
	if cropHeight < height/2 {
		return height
	}
	return cropHeight
}

// createMatPool 创建 Mat 对象池用于帧缓冲
// 预分配固定数量的 Mat 对象，减少运行时的内存分配开销
func createMatPool(size int) chan gocv.Mat {
	pool := make(chan gocv.Mat, size)
	for i := 0; i < size; i++ {
		pool <- gocv.NewMat()
	}
	return pool
}

// closeMatPool 关闭 Mat 对象池并释放所有资源
func closeMatPool(pool chan gocv.Mat) {
	close(pool)
	for m := range pool {
		_ = m.Close()
	}
}

// processFrames 处理解码后的帧序列，计算每帧的差异像素数
//
// 核心算法：
//  1. 将当前帧转为灰度图（帧已由生产者裁剪好）
//  2. 与前一帧做绝对差值
//  3. 二值化处理（阈值25）
//  4. 形态学腐蚀去噪
//  5. 统计非零像素数
func processFrames(frameChan <-chan DecodedFrame, matPool chan gocv.Mat, totalFrames int) ([]uint32, error) {
	diffCounts := make([]uint32, 0, totalFrames)

	currentGray, prevGray, workBuffer := gocv.NewMat(), gocv.NewMat(), gocv.NewMat()
	kernel := gocv.GetStructuringElement(gocv.MorphRect, image.Point{X: 3, Y: 3})

	defer func() {
		_ = currentGray.Close()
		_ = prevGray.Close()
		_ = workBuffer.Close()
		_ = kernel.Close()
	}()

	bar := createProgressBar(totalFrames, ">> 分析中")

	for decodedFrame := range frameChan {
		if decodedFrame.IsLastFrame {
			break
		}

		img := decodedFrame.Frame
		frameNum := decodedFrame.FrameNum

		// 帧已由生产者裁剪好，直接转灰度
		if err := gocv.CvtColor(img, &currentGray, gocv.ColorBGRToGray); err != nil {
			return nil, fmt.Errorf("frame %d CvtColor failed: %w", frameNum, err)
		}

		if !prevGray.Empty() {
			if err := gocv.AbsDiff(currentGray, prevGray, &workBuffer); err != nil {
				return nil, fmt.Errorf("frame %d AbsDiff failed: %w", frameNum, err)
			}
			gocv.Threshold(workBuffer, &workBuffer, BinaryThreshold, 255, gocv.ThresholdBinary)
			if err := gocv.Erode(workBuffer, &workBuffer, kernel); err != nil {
				return nil, fmt.Errorf("frame %d Erode failed: %w", frameNum, err)
			}

			diffCount := gocv.CountNonZero(workBuffer)
			diffCounts = append(diffCounts, uint32(diffCount))
		}

		if err := currentGray.CopyTo(&prevGray); err != nil {
			return nil, fmt.Errorf("frame %d CopyTo failed: %w", frameNum, err)
		}

		select {
		case matPool <- img:
		default:
			_ = img.Close()
		}

		_ = bar.Add(1)
	}

	_ = bar.Finish()

	return diffCounts, nil
}

// frameProducer 在独立 goroutine 中读取视频帧
// 从视频中解码帧，裁剪底部区域后通过 channel 发送给处理函数
// 裁剪操作在生产者阶段完成，减少消费者端的内存分配
func frameProducer(video *gocv.VideoCapture, frameChan chan<- DecodedFrame, matBuffer chan gocv.Mat, width, cropHeight int) {
	defer func() {
		close(frameChan)
	}()

	sentinelMat := gocv.NewMat()
	defer func() {
		_ = sentinelMat.Close()
	}()

	frameNum := 0

	for {
		var fullFrame gocv.Mat
		fromPool := false

		select {
		case m := <-matBuffer:
			fullFrame = m
			fromPool = true
		default:
			fullFrame = gocv.NewMat()
		}

		if ok := video.Read(&fullFrame); !ok || fullFrame.Empty() {
			if fromPool {
				select {
				case matBuffer <- fullFrame:
				default:
					_ = fullFrame.Close()
				}
			} else {
				_ = fullFrame.Close()
			}
			break
		}

		frameNum++

		// 裁剪底部区域（排除字幕），减少后续处理的数据量
		cropped := fullFrame.Region(image.Rect(0, 0, width, cropHeight))

		// 原始帧已处理完毕，归还到池或关闭
		if fromPool {
			select {
			case matBuffer <- fullFrame:
			default:
				_ = fullFrame.Close()
			}
		} else {
			_ = fullFrame.Close()
		}

		frameChan <- DecodedFrame{
			Frame:       cropped,
			FrameNum:    frameNum,
			IsLastFrame: false,
		}
	}

	frameChan <- DecodedFrame{
		Frame:       sentinelMat,
		FrameNum:    frameNum,
		IsLastFrame: true,
	}
}

// ---------------------------------------------------------
// 静态片段生成
// ---------------------------------------------------------

// generateStaticSegments 从差异计数数据中识别静态片段
// 当连续多帧的差异像素数低于阈值时，认为是静态片段
// 只返回持续时间达到最小要求的片段
func generateStaticSegments(diffCounts []uint32, diffCountThreshold float64, minDurationSec float64, fps float64) []StaticSegment {
	if len(diffCounts) == 0 {
		return nil
	}

	var segments []StaticSegment
	inStaticSegment := false
	segmentStartFrame := 0

	for frameNum, diffCount := range diffCounts {
		currentFrame := frameNum + 1

		if float64(diffCount) < diffCountThreshold {
			if !inStaticSegment {
				inStaticSegment = true
				segmentStartFrame = currentFrame
			}
		} else {
			if inStaticSegment {
				if seg := createSegmentIfValid(segmentStartFrame, currentFrame, fps, minDurationSec); seg != nil {
					segments = append(segments, *seg)
				}
				inStaticSegment = false
			}
		}
	}

	if inStaticSegment && len(diffCounts) > 0 {
		lastFrameNum := len(diffCounts)
		if seg := createSegmentIfValid(segmentStartFrame, lastFrameNum, fps, minDurationSec); seg != nil {
			segments = append(segments, *seg)
		}
	}

	return segments
}

// createSegmentIfValid 创建静态片段（如果满足最小时长要求）
// 返回 nil 表示片段时长不足，不应被记录
func createSegmentIfValid(startFrame, endFrame int, fps, minDurationSec float64) *StaticSegment {
	durationFrames := endFrame - startFrame
	durationSeconds := float64(durationFrames) / fps

	if durationSeconds >= minDurationSec {
		return &StaticSegment{
			StartFrame:     startFrame,
			DurationFrames: durationFrames,
		}
	}
	return nil
}

// ---------------------------------------------------------
// FCPXML 生成
// ---------------------------------------------------------

// generateFCPXML 根据检测到的静态片段生成 FCPXML 标记文件
// FCPXML 是 Final Cut Pro X 的项目文件格式
// 生成的文件包含在时间线上标记静态片段起止点的 marker
func generateFCPXML(segments []StaticSegment, meta *proto.AnalysisResult, outputPath string) error {
	formatID := "r1"
	frameDuration := getFrameDuration(meta.Fps)
	totalDuration := frameToRationalTime(int(meta.TotalFrames), meta.Fps)

	markers := createMarkers(segments, meta.Fps)

	fcpxml := FCPXML{
		Version: "1.11",
		Resources: Resources{
			Format: Format{
				ID:         formatID,
				Name:       fmt.Sprintf("%dx%d %gp", meta.Width, meta.Height, meta.Fps),
				FrameDur:   frameDuration,
				Width:      fmt.Sprintf("%d", meta.Width),
				Height:     fmt.Sprintf("%d", meta.Height),
				ColorSpace: "1-1-1 (Rec. 709)",
			},
		},
		Library: Library{
			Location: "file://localhost/Users/Shared/",
			Event: Event{
				Name: "Static Scene Detection",
				UID:  "event-1",
				Project: Project{
					Name: "Detected Static Scenes",
					UID:  "project-1",
					Sequence: Sequence{
						Duration:    totalDuration,
						Format:      formatID,
						TCStart:     "0s",
						TCFormat:    "NDF",
						AudioLayout: "stereo",
						AudioRate:   "48k",
						Spine: Spine{
							Gap: Gap{
								Name:     "Markers_Layer",
								Offset:   "0s",
								Duration: totalDuration,
								Start:    "0s",
								Markers:  markers,
							},
						},
					},
				},
			},
		},
	}

	return writeFCPXMLFile(outputPath, fcpxml)
}

// createMarkers 为每个静态片段创建开始和结束标记
// 标记命名格式：start1/stop1, start2/stop2, ...
func createMarkers(segments []StaticSegment, fps float64) []Marker {
	markers := make([]Marker, 0, len(segments)*2)

	for i, seg := range segments {
		startMarker := Marker{
			Start:    frameToRationalTime(seg.StartFrame, fps),
			Duration: frameToRationalTime(1, fps),
			Value:    fmt.Sprintf("%s%d", MarkerStartPrefix, i+1),
		}

		endFrame := seg.StartFrame + seg.DurationFrames
		stopMarker := Marker{
			Start:    frameToRationalTime(endFrame, fps),
			Duration: frameToRationalTime(1, fps),
			Value:    fmt.Sprintf("%s%d", MarkerStopPrefix, i+1),
		}

		markers = append(markers, startMarker, stopMarker)
	}

	return markers
}

// writeFCPXMLFile 将 FCPXML 结构体序列化为格式化的 XML 文件
func writeFCPXMLFile(outputPath string, fcpxml FCPXML) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("创建输出文件失败: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	if _, err := file.WriteString(xml.Header); err != nil {
		return fmt.Errorf("写入XML头失败: %w", err)
	}
	if _, err := file.WriteString(`<!DOCTYPE fcpxml>` + "\n"); err != nil {
		return fmt.Errorf("写入DOCTYPE失败: %w", err)
	}

	encoder := xml.NewEncoder(file)
	encoder.Indent("", "    ")

	if err := encoder.Encode(fcpxml); err != nil {
		return fmt.Errorf("编码XML失败: %w", err)
	}

	return nil
}

// ---------------------------------------------------------
// 文件命名
// ---------------------------------------------------------

// generatePbFilename 根据视频文件名生成 pb.zst 文件名
// 例如：video.mp4 -> video.pb.zst
func generatePbFilename(videoPath string) string {
	baseName := filepath.Base(videoPath)
	ext := filepath.Ext(baseName)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)
	return fmt.Sprintf("%s.pb.zst", nameWithoutExt)
}

// generateFCPXMLFilename 根据视频文件名和阈值生成 FCPXML 文件名
// 例如：video.mp4, threshold=1000 -> video_threshold_1000.fcpxml
func generateFCPXMLFilename(videoPath string, threshold float64) string {
	baseName := filepath.Base(videoPath)
	ext := filepath.Ext(baseName)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)
	return fmt.Sprintf("%s_threshold_%.0f.fcpxml", nameWithoutExt, threshold)
}

// ---------------------------------------------------------
// 数据持久化
// ---------------------------------------------------------

// saveAnalysisToProto 将分析结果序列化并保存为 Zstd 压缩的 protobuf 文件
func saveAnalysisToProto(result *proto.AnalysisResult, outputPath string) error {
	// 序列化为 protobuf
	data, err := pb.Marshal(result)
	if err != nil {
		return fmt.Errorf("protobuf 序列化失败: %w", err)
	}

	// 创建输出文件
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()

	// 创建 zstd 压缩器
	encoder, err := zstd.NewWriter(file)
	if err != nil {
		return fmt.Errorf("创建 zstd 编码器失败: %w", err)
	}
	defer func() {
		_ = encoder.Close()
	}()

	// 写入压缩数据
	if _, err := encoder.Write(data); err != nil {
		return fmt.Errorf("写入压缩数据失败: %w", err)
	}

	return encoder.Close()
}

// loadAnalysisFromProto 从 Zstd 压缩的 protobuf 文件加载分析结果
func loadAnalysisFromProto(filePath string) (*proto.AnalysisResult, error) {
	// 读取文件内容
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	// 创建 zstd 解压器
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("创建 zstd 解码器失败: %w", err)
	}
	defer decoder.Close()

	// 解压数据
	decompressed, err := decoder.DecodeAll(data, nil)
	if err != nil {
		return nil, fmt.Errorf("zstd 解压失败: %w", err)
	}

	// 反序列化 protobuf
	var result proto.AnalysisResult
	if err := pb.Unmarshal(decompressed, &result); err != nil {
		return nil, fmt.Errorf("protobuf 反序列化失败: %w", err)
	}

	return &result, nil
}

// ---------------------------------------------------------
// 统计与显示
// ---------------------------------------------------------

// printSegmentDurationDistribution 打印静态片段的时长分布统计表
// 将片段按时长分组（0-1s, 1-3s, 3-7s 等），显示各区间的数量和百分比
func printSegmentDurationDistribution(segments []StaticSegment, fps float64) {
	if len(segments) == 0 {
		fmt.Println("未检测到静态片段")
		return
	}

	ranges := []struct {
		min, max float64
		name     string
	}{
		{0, 1, "0-1s"},
		{1, 3, "1-3s"},
		{3, 7, "3-7s"},
		{7, 10, "7-10s"},
		{10, 20, "10-20s"},
		{20, 40, "20-40s"},
		{40, 60, "40-60s"},
		{60, -1, "60s+"},
	}

	counts := countSegmentsByDuration(segments, ranges, fps)
	printDistributionTable(ranges, counts, len(segments))
}

// countSegmentsByDuration 统计各时长区间内的片段数量
func countSegmentsByDuration(segments []StaticSegment, ranges []struct {
	min, max float64
	name     string
}, fps float64) []int {
	counts := make([]int, len(ranges))

	for _, seg := range segments {
		durationSec := float64(seg.DurationFrames) / fps

		for i, r := range ranges {
			if (r.max == -1 && durationSec >= r.min) ||
				(r.max != -1 && durationSec >= r.min && durationSec < r.max) {
				counts[i]++
				break
			}
		}
	}

	return counts
}

// printDistributionTable 打印格式化的分布统计表格
func printDistributionTable(ranges []struct {
	min, max float64
	name     string
}, counts []int, totalSegments int) {
	fmt.Println("┌────────────────────────────────────────┐")

	for i, r := range ranges {
		percentage := 0.0
		if totalSegments > 0 {
			percentage = float64(counts[i]) / float64(totalSegments) * 100
		}
		fmt.Printf("  %-18s %4d 个 (%.1f%%)\n", r.name, counts[i], percentage)
	}

	fmt.Println("└────────────────────────────────────────┘")
}

// computePercentile 计算数据的百分位数
// 使用线性插值方法：当百分位数落在两个数据点之间时进行插值计算
// 例如 P95 表示有 95% 的数据小于等于该值
func computePercentile(values []uint32, percent float64) float64 {
	if len(values) == 0 {
		return 0
	}

	// 复制一份避免修改原数据
	sorted := make([]uint32, len(values))
	copy(sorted, values)

	// 使用 sort.Slice 直接排序 uint32
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i] < sorted[j]
	})

	n := len(sorted)
	if n == 1 {
		return float64(sorted[0])
	}

	if percent <= 0 {
		return float64(sorted[0])
	}
	if percent >= 100 {
		return float64(sorted[n-1])
	}

	rank := percent / 100.0 * (float64(n) - 1.0)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))

	if lower == upper {
		return float64(sorted[lower])
	}

	frac := rank - float64(lower)
	return float64(sorted[lower])*(1.0-frac) + float64(sorted[upper])*frac
}

// createProgressBar 创建并配置进度条
func createProgressBar(total int, description string) *progressbar.ProgressBar {
	return progressbar.NewOptions(total,
		progressbar.OptionSetDescription(description),
		progressbar.OptionSetWidth(ProgressBarWidth),
		progressbar.OptionThrottle(65*time.Millisecond),
		progressbar.OptionShowIts(),
		progressbar.OptionSetItsString("帧"),
		progressbar.OptionSetPredictTime(true),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "━",
			SaucerHead:    "╸",
			SaucerPadding: " ",
			BarStart:      "",
			BarEnd:        "",
		}),
		progressbar.OptionOnCompletion(func() { fmt.Println() }),
	)
}

// ---------------------------------------------------------
// 文件发现
// ---------------------------------------------------------

// findFileWithExtensions 在当前目录查找具有指定扩展名的文件
// 返回按文件名字母排序的第一个匹配文件
// 扩展名匹配不区分大小写
func findFileWithExtensions(extensions []string) string {
	files, err := os.ReadDir(".")
	if err != nil {
		return ""
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		fileName := file.Name()
		for _, ext := range extensions {
			if strings.HasSuffix(strings.ToLower(fileName), ext) {
				return fileName
			}
		}
	}

	return ""
}

// findVideoInCurrentDir 在当前目录查找常见格式的视频文件
func findVideoInCurrentDir() string {
	return findFileWithExtensions([]string{".mp4", ".mov", ".avi", ".mkv", ".wmv", ".flv", ".m4v", ".mpg", ".mpeg"})
}

// findPbInCurrentDir 在当前目录查找 .pb.zst 分析数据文件
func findPbInCurrentDir() string {
	return findFileWithExtensions([]string{".pb.zst"})
}

// ---------------------------------------------------------
// 时间工具
// ---------------------------------------------------------

// frameToRationalTime 将帧号转换为 FCPXML 的有理数时间格式
// 正确处理整数帧率和 NTSC 帧率（29.97、23.976 等）
// NTSC 帧率使用 1001/30000 的倍数表示，以保持精确同步
func frameToRationalTime(frameNum int, fps float64) string {
	fpsInt := int(math.Round(fps))

	if isNTSCRate(fps) {
		if math.Abs(fps-29.97) < 0.01 || math.Abs(fps-59.94) < 0.01 {
			return fmt.Sprintf("%d/30000s", frameNum*1001)
		} else if math.Abs(fps-23.976) < 0.01 {
			return fmt.Sprintf("%d/24000s", frameNum*1001)
		}
	}

	return fmt.Sprintf("%d/%ds", frameNum, fpsInt)
}

// getFrameDuration 返回单帧持续时间的有理数表示
// 用于 FCPXML 的 frameDuration 属性
func getFrameDuration(fps float64) string {
	switch {
	case math.Abs(fps-29.97) < 0.01:
		return "1001/30000s"
	case math.Abs(fps-23.976) < 0.01:
		return "1001/24000s"
	case fps == 30:
		return "100/3000s"
	case fps == 24:
		return "100/2400s"
	case fps == 25:
		return "100/2500s"
	default:
		return fmt.Sprintf("1/%ds", int(fps))
	}
}

// isNTSCRate 判断是否为 NTSC 制式的帧率
// NTSC 帧率是美国和日本等地区使用的标准，采用非整数帧率
func isNTSCRate(fps float64) bool {
	const epsilon = 0.01
	return math.Abs(fps-29.97) < epsilon ||
		math.Abs(fps-23.976) < epsilon ||
		math.Abs(fps-59.94) < epsilon
}

// ---------------------------------------------------------
// FCPXML 数据结构
// ---------------------------------------------------------

// FCPXML 表示 FCPXML 文档的根元素
type FCPXML struct {
	XMLName   xml.Name  `xml:"fcpxml"`
	Version   string    `xml:"version,attr"`
	Resources Resources `xml:"resources"`
	Library   Library   `xml:"library"`
}

// Resources 包含 FCPXML 文档使用的资源定义
type Resources struct {
	Format Format `xml:"format"`
}

// Format 定义视频格式属性（分辨率、帧率、色彩空间等）
type Format struct {
	ID         string `xml:"id,attr"`
	Name       string `xml:"name,attr"`
	FrameDur   string `xml:"frameDuration,attr"`
	Width      string `xml:"width,attr"`
	Height     string `xml:"height,attr"`
	ColorSpace string `xml:"colorSpace,attr"`
}

// Library 表示 FCPXML 资源库，包含事件集合
type Library struct {
	Location string `xml:"location,attr"`
	Event    Event  `xml:"event"`
}

// Event 表示 FCPXML 事件，包含项目集合
type Event struct {
	Name    string  `xml:"name,attr"`
	UID     string  `xml:"uid,attr"`
	Project Project `xml:"project"`
}

// Project 表示 FCPXML 项目，包含时间线序列
type Project struct {
	Name     string   `xml:"name,attr"`
	UID      string   `xml:"uid,attr"`
	Sequence Sequence `xml:"sequence"`
}

// Sequence 表示 FCPXML 时间线序列，包含主故事线
type Sequence struct {
	Duration    string `xml:"duration,attr"`
	Format      string `xml:"format,attr"`
	TCStart     string `xml:"tcStart,attr"`
	TCFormat    string `xml:"tcFormat,attr"`
	AudioLayout string `xml:"audioLayout,attr"`
	AudioRate   string `xml:"audioRate,attr"`
	Spine       Spine  `xml:"spine"`
}

// Spine 表示主故事线（Primary Storyline）
type Spine struct {
	Gap Gap `xml:"gap"`
}

// Gap 表示间隙片段，可以包含标记点
type Gap struct {
	Name     string   `xml:"name,attr"`
	Offset   string   `xml:"offset,attr"`
	Duration string   `xml:"duration,attr"`
	Start    string   `xml:"start,attr"`
	Markers  []Marker `xml:"marker"`
}

// Marker 表示时间线上的标记点
type Marker struct {
	Start    string `xml:"start,attr"`
	Duration string `xml:"duration,attr"`
	Value    string `xml:"value,attr"`
}
