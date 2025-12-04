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
	Threshold    float64
	CropHeight   int
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

// ---------------------------------------------------------
// Entry Point
// ---------------------------------------------------------

func main() {
	// 1. 解析命令行参数
	flag.Parse()
	args := flag.Args()

	var diffCountThreshold float64 = -1
	var minDurationSec float64 = DefaultMinDurationSec

	// 尝试解析第一个位置参数: 判定静止的阈值 (Threshold)
	if len(args) > 0 {
		if val, err := strconv.ParseFloat(args[0], 64); err == nil {
			diffCountThreshold = val
		}
	}

	// 尝试解析第二个位置参数: 最小静止持续时间 (Min Duration)
	if len(args) > 1 {
		if val, err := strconv.ParseFloat(args[1], 64); err == nil {
			minDurationSec = val
		}
	}

	var finalInputPath string
	var isGobInput bool

	// 2. 自动检测输入文件 (优先级: Gob > 视频)
	foundGob := findGobInCurrentDir()
	foundVideo := findVideoInCurrentDir()

	if foundGob != "" {
		finalInputPath = foundGob
		isGobInput = true
	} else if foundVideo != "" {
		finalInputPath = foundVideo
		isGobInput = false
	} else {
		fmt.Println("错误: 当前目录未找到 Gob 或视频文件")
		fmt.Println()
		fmt.Println("用法:")
		fmt.Println("  vcmp                                # 分析视频生成gob或显示gob统计")
		fmt.Println("  vcmp <threshold>                    # 使用gob生成FCPXML (阈值)")
		fmt.Println("  vcmp <threshold> <min_duration>     # 指定阈值和最小持续时间(秒)")
		fmt.Println()
		os.Exit(1)
	}

	// 3. 路由逻辑分发

	// 场景 A: 输入是 Gob 且用户指定了阈值 -> 生成 FCPXML 切割清单
	if isGobInput && diffCountThreshold >= 0 {
		if err := handleGobToFCPXML(finalInputPath, diffCountThreshold, minDurationSec); err != nil {
			fmt.Printf("错误: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 场景 B: 输入是 Gob 但用户未指定阈值 -> 仅显示统计数据 (直方图/百分位数)
	if isGobInput && diffCountThreshold < 0 {
		if err := handleGobAnalysis(finalInputPath); err != nil {
			fmt.Printf("错误: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 场景 C: 输入是视频 -> 执行耗时的视频分析任务并保存 Gob 文件
	if err := handleVideoAnalysis(finalInputPath); err != nil {
		fmt.Printf("错误: %v\n", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------
// High-Level Handlers
// ---------------------------------------------------------

func handleVideoAnalysis(videoPath string) error {
	baseName := filepath.Base(videoPath)
	ext := filepath.Ext(baseName)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)
	timestamp := time.Now().Format("20060102_150405")

	finalOutputPath := fmt.Sprintf("%s_%s.gob", nameWithoutExt, timestamp)

	fmt.Printf(">> 分析视频: %s\n", videoPath)

	video, err := gocv.VideoCaptureFileWithAPI(videoPath, gocv.VideoCaptureAVFoundation)
	if err != nil {
		return fmt.Errorf("打开视频失败: %w", err)
	}
	defer video.Close()

	fps := video.Get(gocv.VideoCaptureFPS)
	width := int(video.Get(gocv.VideoCaptureFrameWidth))
	height := int(video.Get(gocv.VideoCaptureFrameHeight))
	totalFrames := int(video.Get(gocv.VideoCaptureFrameCount))

	bottomMaskHeight := int(float64(height) * CropIgnoreRatio)
	cropHeight := height - bottomMaskHeight
	if cropHeight < height/2 {
		cropHeight = height
	}

	poolSize := FrameBufferSize + 2
	matBuffer := make(chan gocv.Mat, poolSize)
	for i := 0; i < poolSize; i++ {
		matBuffer <- gocv.NewMat()
	}
	defer func() {
		close(matBuffer)
		for m := range matBuffer {
			m.Close()
		}
	}()

	frameChan := make(chan DecodedFrame, FrameBufferSize)
	go frameProducer(video, frameChan, matBuffer)

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
		case matBuffer <- img:
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

	result := AnalysisResult{
		VideoFile:    videoPath,
		AnalysisTime: time.Now().Format("2006-01-02 15:04:05"),
		FPS:          fps,
		Width:        width,
		Height:       height,
		TotalFrames:  totalFrames,
		CropHeight:   cropHeight,
		DiffCounts:   diffCounts,
	}

	if err := result.SaveToGob(finalOutputPath); err != nil {
		return fmt.Errorf("保存Gob失败: %w", err)
	}

	fmt.Printf("✓  分析完成 -> %s\n", finalOutputPath)
	result.PrintDistribution()
	result.PrintPercentiles(1.5)
	fmt.Printf("\n生成FCPXML请使用: vcmp <threshold> [min_duration]\n")

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
		return fmt.Errorf("未找到静态片段 (阈值: %.0f)", diffCountThreshold)
	}

	baseName := filepath.Base(result.VideoFile)
	ext := filepath.Ext(baseName)
	nameWithoutExt := strings.TrimSuffix(baseName, ext)

	finalOutputPath := fmt.Sprintf("%s_threshold_%.0f.fcpxml", nameWithoutExt, diffCountThreshold)

	meta := VideoMetadata{
		FPS:         result.FPS,
		Width:       result.Width,
		Height:      result.Height,
		TotalFrames: result.TotalFrames,
		FilePath:    result.VideoFile,
	}

	if err := generateFCPXML(segments, meta, finalOutputPath); err != nil {
		return fmt.Errorf("生成FCPXML失败: %w", err)
	}

	fmt.Printf("✓  FCPXML已生成 -> %s\n", finalOutputPath)
	fmt.Printf("   检测到 %d 个静态片段 (阈值: %.0f, 最小时长: %.0f秒)\n", len(segments), diffCountThreshold, minDurationSec)

	return nil
}

func handleGobAnalysis(gobPath string) error {
	fmt.Printf(">> 加载分析数据: %s\n", gobPath)
	result, err := loadAnalysisFromGob(gobPath)
	if err != nil {
		return fmt.Errorf("加载Gob失败: %w", err)
	}

	result.PrintDistribution()
	result.PrintPercentiles(1.5)
	fmt.Printf("\n生成FCPXML请使用: vcmp <threshold> [min_duration]\n")

	return nil
}

// ---------------------------------------------------------
// Core Logic & Algorithms
// ---------------------------------------------------------

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
			fromPool = false
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
				durationFrames := currentFrame - segmentStartFrame
				durationSeconds := float64(durationFrames) / fps

				if durationSeconds >= minDurationSec {
					segments = append(segments, StaticSegment{
						StartFrame:     segmentStartFrame,
						DurationFrames: durationFrames,
					})
				}
				inStaticSegment = false
			}
		}
	}

	if inStaticSegment && len(diffCounts) > 0 {
		lastFrameNum := len(diffCounts)
		durationFrames := lastFrameNum - segmentStartFrame
		durationSeconds := float64(durationFrames) / fps

		if durationSeconds >= minDurationSec {
			segments = append(segments, StaticSegment{
				StartFrame:     segmentStartFrame,
				DurationFrames: durationFrames,
			})
		}
	}

	return segments
}

func generateFCPXML(segments []StaticSegment, meta VideoMetadata, outputPath string) error {
	formatID := "r1"
	frameDuration := getFrameDuration(meta.FPS)
	totalDuration := frameToRationalTime(meta.TotalFrames, meta.FPS)

	markers := make([]Marker, 0, len(segments)*2)

	for i, seg := range segments {
		startMarker := Marker{
			Start:    frameToRationalTime(seg.StartFrame, meta.FPS),
			Duration: frameToRationalTime(1, meta.FPS),
			Value:    fmt.Sprintf("%s%d", MarkerStartPrefix, i+1),
		}

		endFrame := seg.StartFrame + seg.DurationFrames
		stopMarker := Marker{
			Start:    frameToRationalTime(endFrame, meta.FPS),
			Duration: frameToRationalTime(1, meta.FPS),
			Value:    fmt.Sprintf("%s%d", MarkerStopPrefix, i+1),
		}

		markers = append(markers, startMarker, stopMarker)
	}

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
// Data Persistence
// ---------------------------------------------------------

func (r *AnalysisResult) SaveToGob(outputPath string) error {
	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	gw := gzip.NewWriter(file)
	encoder := gob.NewEncoder(gw)

	if err := encoder.Encode(r); err != nil {
		_ = gw.Close()
		return err
	}

	if err := gw.Close(); err != nil {
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
// Statistics & UI
// ---------------------------------------------------------

func (r *AnalysisResult) PrintDistribution() {
	diffCounts := r.DiffCounts
	if len(diffCounts) == 0 {
		fmt.Println("警告: 无帧数据可分析")
		return
	}

	ranges := []struct {
		min, max int32
		name     string
	}{
		{0, 100, "0-100"},
		{101, 1000, "101-1,000"},
		{1001, 10000, "1,001-10,000"},
		{10001, 50000, "10,001-50,000"},
		{50001, 100000, "50,001-100,000"},
		{100001, 500000, "100,001-500,000"},
		{500001, -1, "500,001+"},
	}

	counts := make([]int, len(ranges))
	for _, diffCount := range diffCounts {
		for i, r := range ranges {
			if r.max == -1 {
				if diffCount >= r.min {
					counts[i]++
				}
			} else if diffCount >= r.min && diffCount <= r.max {
				counts[i]++
			}
		}
	}

	fmt.Println("\nDiff Count 分布:")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	totalFrames := len(diffCounts)
	for i, r := range ranges {
		percentage := float64(counts[i]) / float64(totalFrames) * 100
		fmt.Printf("  %-18s %6d 帧 (%.1f%%)\n", r.name, counts[i], percentage)
	}

	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
}

func (r *AnalysisResult) PrintPercentiles(factor float64) {
	diffCounts := r.DiffCounts
	if len(diffCounts) == 0 {
		fmt.Println("警告: 无帧数据可分析")
		return
	}

	p50 := computePercentile(diffCounts, 50)
	p90 := computePercentile(diffCounts, 90)
	p95 := computePercentile(diffCounts, 95)
	p99 := computePercentile(diffCounts, 99)
	suggested := math.Round(p95 * factor)

	fmt.Printf("P50 %.0f | P90 %.0f | P95 %.0f | P99 %.0f | 建议阈值 %.0f", p50, p90, p95, p99, suggested)
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
// Environment Discovery
// ---------------------------------------------------------

func findVideoInCurrentDir() string {
	videoExtensions := []string{".mp4", ".mov", ".avi", ".mkv", ".wmv", ".flv", ".m4v", ".mpg", ".mpeg"}

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
		for _, ext := range videoExtensions {
			if strings.HasSuffix(strings.ToLower(fileName), ext) {
				return fileName
			}
		}
	}

	return ""
}

func findGobInCurrentDir() string {
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
		if strings.HasSuffix(strings.ToLower(fileName), ".gob") {
			return fileName
		}
	}

	return ""
}

// ---------------------------------------------------------
// Low-Level Utilities
// ---------------------------------------------------------

func frameToRationalTime(frameNum int, fps float64) string {
	var timescale int
	isNTSC := isNTSCRate(fps)

	if isNTSC {
		timescale = 30000
	} else if fps == 24 || fps == 25 || fps == 30 || fps == 50 || fps == 60 {
		timescale = int(fps) * 1000
	} else {
		timescale = 30000
	}

	timeValue := int64(float64(frameNum) / fps * float64(timescale))
	return fmt.Sprintf("%d/%ds", timeValue, timescale)
}

func getFrameDuration(fps float64) string {
	if math.Abs(fps-29.97) < 0.01 {
		return "1001/30000s"
	} else if math.Abs(fps-23.976) < 0.01 {
		return "1001/24000s"
	} else if fps == 30 {
		return "100/3000s"
	} else if fps == 24 {
		return "100/2400s"
	} else if fps == 25 {
		return "100/2500s"
	}

	return fmt.Sprintf("1/%ds", int(fps))
}

func isNTSCRate(fps float64) bool {
	const epsilon = 0.01
	return math.Abs(fps-29.97) < epsilon || math.Abs(fps-23.976) < epsilon || math.Abs(fps-59.94) < epsilon
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
