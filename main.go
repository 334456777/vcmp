package main

import (
	"compress/gzip"
	"encoding/gob"
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

	"gocv.io/x/gocv"
)

// ---------------------------------------------------------
// Constants
// ---------------------------------------------------------

const (
	MarkerStartPrefix      = "start"
	MarkerStopPrefix       = "stop"
	CropIgnoreRatio        = 65.0 / 1080.0
	ProgressBarWidth       = 30
	ProgressUpdateInterval = 30
	DefaultMinDurationSec  = 20.0
	BinaryThreshold        = 25
	FrameBufferSize        = 10
	DefaultThresholdFactor = 1.5
)

// ---------------------------------------------------------
// Core Domain Types
// ---------------------------------------------------------

type DecodedFrame struct {
	Frame       gocv.Mat
	FrameNum    int
	IsLastFrame bool
}

type AnalysisResult struct {
	VideoFile    string
	AnalysisTime string
	FPS          float64
	Width        int
	Height       int
	TotalFrames  int
	DiffCounts   []int32
}

type StaticSegment struct {
	StartFrame     int
	DurationFrames int
}

type VideoMetadata struct {
	FPS         float64
	Width       int
	Height      int
	TotalFrames int
	FilePath    string
}

type ThresholdConfig struct {
	Factor         float64
	Percentile     float64
	MinDurationSec float64
}

// ---------------------------------------------------------
// Entry Point
// ---------------------------------------------------------

func main() {
	flag.Parse()
	args := flag.Args()

	var diffCountThreshold float64 = -1
	var minDurationSec float64 = DefaultMinDurationSec

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
// Command Routing
// ---------------------------------------------------------

func routeCommand(inputPath string, isGobInput bool, threshold, minDuration float64) error {
	if isGobInput && threshold >= 0 {
		return handleGobToFCPXML(inputPath, threshold, minDuration)
	}

	if isGobInput {
		return handleGobAnalysis(inputPath)
	}

	return handleVideoAnalysis(inputPath)
}

func detectInputFile() (string, bool) {
	foundGob := findGobInCurrentDir()
	if foundGob != "" {
		return foundGob, true
	}

	foundVideo := findVideoInCurrentDir()
	if foundVideo != "" {
		return foundVideo, false
	}

	return "", false
}

func printUsageAndExit() {
	fmt.Println("错误: 当前目录未找到 Gob 或视频文件")
	fmt.Println()
	fmt.Println("用法:")
	fmt.Println("  vcmp                                # 分析视频生成gob或显示gob统计")
	fmt.Println("  vcmp <threshold>                    # 使用gob生成FCPXML (阈值)")
	fmt.Println("  vcmp <threshold> <min_duration>     # 指定阈值和最小持续时间(秒)")
	fmt.Println()
	os.Exit(1)
}

// ---------------------------------------------------------
// High-Level Handlers
// ---------------------------------------------------------

func handleVideoAnalysis(videoPath string) error {
	fmt.Printf(">> 分析视频: %s\n", videoPath)

	result, err := analyzeVideo(videoPath)
	if err != nil {
		return fmt.Errorf("分析视频失败: %w", err)
	}

	outputPath := generateGobFilename(videoPath)
	if err := result.SaveToGob(outputPath); err != nil {
		return fmt.Errorf("保存Gob失败: %w", err)
	}

	printAnalysisResults(result, DefaultThresholdFactor)
	return nil
}

func handleGobToFCPXML(gobPath string, diffCountThreshold, minDurationSec float64) error {
	fmt.Printf(">> 加载分析数据: %s\n", gobPath)

	result, err := loadAnalysisFromGob(gobPath)
	if err != nil {
		return fmt.Errorf("加载Gob失败: %w", err)
	}

	segments := generateStaticSegments(result.DiffCounts, diffCountThreshold, minDurationSec, result.FPS)
	if len(segments) == 0 {
		return fmt.Errorf("未找到静态片段 (阈值: %.0f, 最小时长: %.0f秒)", diffCountThreshold, minDurationSec)
	}

	fmt.Printf("\n阈值 %.0f, 最小时长 %.0fs 的片段分布:\n", diffCountThreshold, minDurationSec)
	printSegmentDurationDistribution(segments, result.FPS)
	fmt.Println()

	outputPath := generateFCPXMLFilename(result.VideoFile, diffCountThreshold)
	meta := VideoMetadata{
		FPS:         result.FPS,
		Width:       result.Width,
		Height:      result.Height,
		TotalFrames: result.TotalFrames,
		FilePath:    result.VideoFile,
	}

	if err := generateFCPXML(segments, meta, outputPath); err != nil {
		return fmt.Errorf("生成FCPXML失败: %w", err)
	}

	fmt.Printf("✓  FCPXML已生成 -> %s\n", outputPath)
	fmt.Printf("   检测到 %d 个静态片段\n", len(segments))

	return nil
}

func handleGobAnalysis(gobPath string) error {
	fmt.Printf(">> 加载分析数据: %s\n", gobPath)

	result, err := loadAnalysisFromGob(gobPath)
	if err != nil {
		return fmt.Errorf("加载Gob失败: %w", err)
	}

	printAnalysisResults(result, DefaultThresholdFactor)
	return nil
}

// ---------------------------------------------------------
// Analysis Results Display (提取的公共函数)
// ---------------------------------------------------------

func printAnalysisResults(result *AnalysisResult, factor float64) {
	config := ThresholdConfig{
		Factor:         factor,
		Percentile:     95,
		MinDurationSec: 0.0,
	}

	threshold := calculateSuggestedThreshold(result.DiffCounts, config)
	segments := generateStaticSegments(result.DiffCounts, threshold, config.MinDurationSec, result.FPS)

	fmt.Printf("\n阈值为 P%.0f * %.1f = %.0f 时的连续静止时间分布:\n",
		config.Percentile, config.Factor, threshold)
	printSegmentDurationDistribution(segments, result.FPS)
	// fmt.Printf("生成FCPXML请使用: vcmp <threshold> [min_duration]\n")
}

func calculateSuggestedThreshold(diffCounts []int32, config ThresholdConfig) float64 {
	percentileValue := computePercentile(diffCounts, config.Percentile)
	return math.Round(percentileValue * config.Factor)
}

// ---------------------------------------------------------
// Video Analysis Core
// ---------------------------------------------------------

func analyzeVideo(videoPath string) (*AnalysisResult, error) {
	video, err := gocv.VideoCaptureFileWithAPI(videoPath, gocv.VideoCaptureAVFoundation)
	if err != nil {
		return nil, fmt.Errorf("打开视频失败: %w", err)
	}
	defer video.Close()

	metadata := extractVideoMetadata(video, videoPath)
	cropHeight := calculateCropHeight(metadata.Height)

	matPool := createMatPool(FrameBufferSize + 2)
	defer closeMatPool(matPool)

	frameChan := make(chan DecodedFrame, FrameBufferSize)
	go frameProducer(video, frameChan, matPool)

	diffCounts := processFrames(frameChan, matPool, metadata.Width, cropHeight, metadata.TotalFrames)

	return &AnalysisResult{
		VideoFile:    videoPath,
		AnalysisTime: time.Now().Format("2006-01-02 15:04:05"),
		FPS:          metadata.FPS,
		Width:        metadata.Width,
		Height:       metadata.Height,
		TotalFrames:  metadata.TotalFrames,
		DiffCounts:   diffCounts,
	}, nil
}

func extractVideoMetadata(video *gocv.VideoCapture, filePath string) VideoMetadata {
	return VideoMetadata{
		FPS:         video.Get(gocv.VideoCaptureFPS),
		Width:       int(video.Get(gocv.VideoCaptureFrameWidth)),
		Height:      int(video.Get(gocv.VideoCaptureFrameHeight)),
		TotalFrames: int(video.Get(gocv.VideoCaptureFrameCount)),
		FilePath:    filePath,
	}
}

func calculateCropHeight(height int) int {
	bottomMaskHeight := int(float64(height) * CropIgnoreRatio)
	cropHeight := height - bottomMaskHeight
	if cropHeight < height/2 {
		return height
	}
	return cropHeight
}

func createMatPool(size int) chan gocv.Mat {
	pool := make(chan gocv.Mat, size)
	for i := 0; i < size; i++ {
		pool <- gocv.NewMat()
	}
	return pool
}

func closeMatPool(pool chan gocv.Mat) {
	close(pool)
	for m := range pool {
		m.Close()
	}
}

func processFrames(frameChan <-chan DecodedFrame, matPool chan gocv.Mat, width, cropHeight, totalFrames int) []int32 {
	diffCounts := make([]int32, 0, totalFrames)

	currentGray, frameDelta, prevGray, eroded := gocv.NewMat(), gocv.NewMat(), gocv.NewMat(), gocv.NewMat()
	kernel := gocv.GetStructuringElement(gocv.MorphRect, image.Point{X: 3, Y: 3})

	defer func() {
		currentGray.Close()
		frameDelta.Close()
		prevGray.Close()
		eroded.Close()
		kernel.Close()
	}()

	for decodedFrame := range frameChan {
		if decodedFrame.IsLastFrame {
			decodedFrame.Frame.Close()
			break
		}

		img := decodedFrame.Frame
		frameNum := decodedFrame.FrameNum

		currentROI_BGR := img.Region(image.Rect(0, 0, width, cropHeight))
		gocv.CvtColor(currentROI_BGR, &currentGray, gocv.ColorBGRToGray)
		currentROI_BGR.Close()

		if !prevGray.Empty() {
			gocv.AbsDiff(currentGray, prevGray, &frameDelta)
			gocv.Threshold(frameDelta, &frameDelta, BinaryThreshold, 255, gocv.ThresholdBinary)
			gocv.Erode(frameDelta, &eroded, kernel)

			diffCount := gocv.CountNonZero(eroded)
			diffCounts = append(diffCounts, int32(diffCount))
		}

		currentGray.CopyTo(&prevGray)

		select {
		case matPool <- img:
		default:
			img.Close()
		}

		if frameNum%ProgressUpdateInterval == 0 {
			updateProgressBar(frameNum, totalFrames, ">> 分析中")
		}
	}

	if len(diffCounts) > 0 {
		updateProgressBar(totalFrames, totalFrames, ">> 分析中")
	}

	return diffCounts
}

func frameProducer(video *gocv.VideoCapture, frameChan chan<- DecodedFrame, matBuffer chan gocv.Mat) {
	defer close(frameChan)

	frameNum := 0

	for {
		var matToSend gocv.Mat
		fromPool := false

		select {
		case m := <-matBuffer:
			matToSend = m
			fromPool = true
		default:
			matToSend = gocv.NewMat()
		}

		if ok := video.Read(&matToSend); !ok || matToSend.Empty() {
			if fromPool {
				select {
				case matBuffer <- matToSend:
				default:
					matToSend.Close()
				}
			} else {
				matToSend.Close()
			}
			break
		}

		frameNum++

		frameChan <- DecodedFrame{
			Frame:       matToSend,
			FrameNum:    frameNum,
			IsLastFrame: false,
		}
	}

	frameChan <- DecodedFrame{
		Frame:       gocv.NewMat(),
		FrameNum:    frameNum,
		IsLastFrame: true,
	}
}

// ---------------------------------------------------------
// Segment Generation
// ---------------------------------------------------------

func generateStaticSegments(diffCounts []int32, diffCountThreshold float64, minDurationSec float64, fps float64) []StaticSegment {
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
// FCPXML Generation
// ---------------------------------------------------------

func generateFCPXML(segments []StaticSegment, meta VideoMetadata, outputPath string) error {
	formatID := "r1"
	frameDuration := getFrameDuration(meta.FPS)
	totalDuration := frameToRationalTime(meta.TotalFrames, meta.FPS)

	markers := createMarkers(segments, meta.FPS)

	fcpxml := FCPXML{
		Version: "1.11",
		Resources: Resources{
			Format: Format{
				ID:         formatID,
				Name:       fmt.Sprintf("%dx%d %gp", meta.Width, meta.Height, meta.FPS),
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

func writeFCPXMLFile(outputPath string, fcpxml FCPXML) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("创建输出文件失败: %w", err)
	}
	defer file.Close()

	file.WriteString(xml.Header)
	file.WriteString(`<!DOCTYPE fcpxml>` + "\n")

	encoder := xml.NewEncoder(file)
	encoder.Indent("", "    ")

	if err := encoder.Encode(fcpxml); err != nil {
		return fmt.Errorf("编码XML失败: %w", err)
	}

	return nil
}

// ---------------------------------------------------------
// File Naming
// ---------------------------------------------------------

func generateGobFilename(videoPath string) string {
	baseName := filepath.Base(videoPath)
	ext := filepath.Ext(baseName)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)
	timestamp := time.Now().Format("20060102_150405")
	return fmt.Sprintf("%s_%s.gob", nameWithoutExt, timestamp)
}

func generateFCPXMLFilename(videoPath string, threshold float64) string {
	baseName := filepath.Base(videoPath)
	ext := filepath.Ext(baseName)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)
	return fmt.Sprintf("%s_threshold_%.0f.fcpxml", nameWithoutExt, threshold)
}

// ---------------------------------------------------------
// Data Persistence
// ---------------------------------------------------------

func (r *AnalysisResult) SaveToGob(outputPath string) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	gw := gzip.NewWriter(file)
	defer gw.Close()

	encoder := gob.NewEncoder(gw)
	if err := encoder.Encode(r); err != nil {
		return err
	}

	return nil
}

func loadAnalysisFromGob(filePath string) (*AnalysisResult, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	gr, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("gzip 读取失败: %w", err)
	}
	defer gr.Close()

	var result AnalysisResult
	decoder := gob.NewDecoder(gr)
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// ---------------------------------------------------------
// Statistics & Display
// ---------------------------------------------------------

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

func computePercentile(values []int32, percent float64) float64 {
	if len(values) == 0 {
		return 0
	}

	ints := make([]int, len(values))
	for i, v := range values {
		ints[i] = int(v)
	}
	sort.Ints(ints)

	n := len(ints)
	if n == 1 {
		return float64(ints[0])
	}

	if percent <= 0 {
		return float64(ints[0])
	}
	if percent >= 100 {
		return float64(ints[n-1])
	}

	rank := percent / 100.0 * (float64(n) - 1.0)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))

	if lower == upper {
		return float64(ints[lower])
	}

	frac := rank - float64(lower)
	return float64(ints[lower])*(1.0-frac) + float64(ints[upper])*frac
}

func updateProgressBar(current, total int, prefix string) {
	percentage := float64(current) / float64(total) * 100
	filled := int(float64(ProgressBarWidth) * float64(current) / float64(total))

	bar := strings.Repeat("=", filled)
	empty := strings.Repeat(".", ProgressBarWidth-filled)

	fmt.Printf("\r%s [%s>%s] %.1f%%", prefix, bar, empty, percentage)

	if current >= total {
		fmt.Println()
	}
}

// ---------------------------------------------------------
// File Discovery
// ---------------------------------------------------------

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

func findVideoInCurrentDir() string {
	return findFileWithExtensions([]string{".mp4", ".mov", ".avi", ".mkv", ".wmv", ".flv", ".m4v", ".mpg", ".mpeg"})
}

func findGobInCurrentDir() string {
	return findFileWithExtensions([]string{".gob"})
}

// ---------------------------------------------------------
// Time Utilities
// ---------------------------------------------------------

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

func isNTSCRate(fps float64) bool {
	const epsilon = 0.01
	return math.Abs(fps-29.97) < epsilon ||
		math.Abs(fps-23.976) < epsilon ||
		math.Abs(fps-59.94) < epsilon
}

// ---------------------------------------------------------
// FCPXML Data Structures
// ---------------------------------------------------------

type FCPXML struct {
	XMLName   xml.Name  `xml:"fcpxml"`
	Version   string    `xml:"version,attr"`
	Resources Resources `xml:"resources"`
	Library   Library   `xml:"library"`
}

type Resources struct {
	Format Format `xml:"format"`
}

type Format struct {
	ID         string `xml:"id,attr"`
	Name       string `xml:"name,attr"`
	FrameDur   string `xml:"frameDuration,attr"`
	Width      string `xml:"width,attr"`
	Height     string `xml:"height,attr"`
	ColorSpace string `xml:"colorSpace,attr"`
}

type Library struct {
	Location string `xml:"location,attr"`
	Event    Event  `xml:"event"`
}

type Event struct {
	Name    string  `xml:"name,attr"`
	UID     string  `xml:"uid,attr"`
	Project Project `xml:"project"`
}

type Project struct {
	Name     string   `xml:"name,attr"`
	UID      string   `xml:"uid,attr"`
	Sequence Sequence `xml:"sequence"`
}

type Sequence struct {
	Duration    string `xml:"duration,attr"`
	Format      string `xml:"format,attr"`
	TCStart     string `xml:"tcStart,attr"`
	TCFormat    string `xml:"tcFormat,attr"`
	AudioLayout string `xml:"audioLayout,attr"`
	AudioRate   string `xml:"audioRate,attr"`
	Spine       Spine  `xml:"spine"`
}

type Spine struct {
	Gap Gap `xml:"gap"`
}

type Gap struct {
	Name     string   `xml:"name,attr"`
	Offset   string   `xml:"offset,attr"`
	Duration string   `xml:"duration,attr"`
	Start    string   `xml:"start,attr"`
	Markers  []Marker `xml:"marker"`
}

type Marker struct {
	Start    string `xml:"start,attr"`
	Duration string `xml:"duration,attr"`
	Value    string `xml:"value,attr"`
}
