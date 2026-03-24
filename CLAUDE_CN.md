# CLAUDE.md

Guidance for Claude Code working with this repository.

## Project Overview

vcmp is a video static scene detection tool. Analyzes videos frame-by-frame using frame difference method to identify static segments and generates FCPXML marker files for Final Cut Pro X.

**Core workflow:**
1. First run: Analyze video → Generate `.pb.zst` analysis data
2. View statistics: Run directly → Display results from existing `.pb.zst`
3. Export markers: Specify threshold → Generate FCPXML file

## Common Commands

### Build and Run

```bash
make proto      # Generate protobuf code (required after modifying .proto)
make build      # Build binary (auto-generates protobuf)
make run        # Build and run
make install    # Install to system path (requires sudo)
make clean      # Clean build files
```

## Code Architecture

### Single-File Structure

All code in `main.go`, organized by functional modules:

1. **Entry and routing** (`main()`, `routeCommand()`)
   - Parse command line arguments (threshold, min duration)
   - Auto-detect input file type (video or `.pb.zst`)
   - Route to three modes: video analysis, statistics view, FCPXML generation

2. **Video analysis core** (`analyzeVideo()`, `processFrames()`)
   - **Concurrency model**: Producer-consumer pattern
     - `frameProducer()`: Separate goroutine for frame decoding
     - `processFrames()`: Main coroutine for frame difference calculation
     - Communication via `frameChan` buffer (size `FrameBufferSize`)
   - **Frame difference algorithm**:
     - Convert to grayscale → Absolute difference → Binarization (threshold 25) → Morphological erosion → Count non-zero pixels
     - Auto-crop bottom 65/1080 to exclude hardcoded subtitle interference
   - **Performance optimization**: Mat object pool reduces memory allocation overhead

3. **Static segment detection** (`generateStaticSegments()`)
   - Scan `diffCounts` array for consecutive segments below threshold
   - Apply minimum duration filter (default 20 seconds)

4. **Data persistence** (`.pb.zst` file)
   - Protocol Buffers definition: `proto/analysis.proto`
   - Zstd compression: `github.com/klauspost/compress/zstd`
   - Stores: Video metadata, per-frame difference counts, suggested threshold
   - **Important**: Generated after first analysis, subsequent operations don't require re-analyzing video

5. **FCPXML generation** (`generateFCPXML()`)
   - Marker naming: `start1/stop1`, `start2/stop2`, ...
   - Properly handles NTSC frame rates (29.97, 23.976) rational time representation

### Core Constants

- `BinaryThreshold = 25`: Frame difference binarization threshold
- `DefaultMinDurationSec = 20.0`: Default minimum duration (seconds)
- `percentile = 95.0`: Percentile for calculating suggested threshold
- `DefaultThresholdFactor = 1.5`: Suggested threshold = P95 × 1.5
- `CropIgnoreNumerator/Denominator = 65/1080`: Bottom crop ratio (subtitle area)

### Protocol Buffers

**Location**: `proto/analysis.proto`

**Important rules:**
- After modifying `.proto` file, **must** run `make proto` to regenerate `proto/analysis.pb.go`
- `go_package` option set to `"vcmp/proto"`
- Generated Go file uses `paths=source_relative` (same directory as `.proto`)

### File Discovery Strategy

Program auto-searches current directory for files (by priority):
1. `.pb.zst` file (analysis data)
2. Video files (supports `.mp4`, `.mov`, `.avi`, `.mkv`, `.wmv`, `.flv`, `.m4v`, `.mpg`, `.mpeg`)

## Key Design Decisions

### Why use concurrent processing?

Video decoding is I/O intensive, frame processing is CPU intensive. Producer-consumer pattern allows:
- Parallel execution of decoding and computation
- Smooth out speed differences via buffered channel
- Avoid decoding blocking computation or vice versa

### Why is suggested threshold P95 × 1.5?

- P95 filters out 95% of normal frame jitter
- Multiplying by 1.5 provides safety margin, avoiding false positives
- Auto-adjusts based on actual video content, adapting to different video characteristics

### Why crop bottom 65/1080?

Hardcoded subtitles usually at bottom of frame, subtitle flickering can be mistaken for frame motion. Cropping this area avoids interference.
