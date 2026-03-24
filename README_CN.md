# vcmp - Video Static Scene Detection Tool

A command-line tool for detecting static scenes in videos. Analyzes video content frame-by-frame to automatically identify static segments and generates FCPXML marker files for Final Cut Pro X.

## Features

- 🎬 **Auto-detect static scenes**: Frame-by-frame analysis using frame difference method
- 📊 **Smart threshold suggestion**: Auto-calculate suggested threshold based on P95 percentile
- 💾 **Analysis data caching**: Generate `.pb.zst` file after first analysis, reuse without re-analysis
- 🎯 **FCPXML export**: Generate Final Cut Pro X compatible marker files
- ⚡ **High-performance processing**: Concurrent processing and object pool optimization
- 🎨 **Subtitle area exclusion**: Auto-crop bottom area to avoid hardcoded subtitle interference
- 🗜️ **Efficient compression**: Protocol Buffers + Zstd compression for smaller data files

## Installation

### Requirements

- Go 1.25.2+
- OpenCV (via gocv.io/x/gocv)
- Protocol Buffers compiler (protoc)

### Build

```bash
git clone <repository-url>
cd vcmp
go mod download
make build
```

### Install to System Path

```bash
make install    # Requires sudo
```

## Usage

### Basic Workflow

1. **First-time video analysis**: Place video in current directory, run `vcmp`
   ```bash
   vcmp
   ```

2. **View analysis statistics**: Run `vcmp` to view existing `.pb.zst` results
   ```bash
   vcmp
   ```

3. **Generate FCPXML markers**: Specify threshold
   ```bash
   vcmp <threshold>
   vcmp <threshold> <min_duration>
   ```

### Parameters

- `threshold`: Difference pixel threshold. Frames below this are considered static
- `min_duration`: Minimum duration in seconds (default: 20)

### Examples

```bash
vcmp            # Analyze video and generate .pb.zst
vcmp 1000       # Generate FCPXML with suggested threshold
vcmp 800 15     # Custom threshold and min duration
vcmp 1000 30    # Only mark segments longer than 30 seconds
```

## How It Works

### Detection Algorithm

1. **Frame difference**: Compare grayscale difference between adjacent frames
2. **Binarization**: Use threshold (default 25) to binarize difference map
3. **Morphological processing**: Use erosion to remove noise
4. **Segment identification**: Consecutive frames below threshold are static segments

### Threshold Calculation

Uses P95 (95th percentile) × 1.5 as default suggested threshold:
- Filters most normal frame jitter
- Preserves true static scenes
- Auto-adjusts based on video content

### Subtitle Area Processing

Auto-crops bottom 65/1080 of frame (~6%) to exclude:
- Hardcoded subtitles
- Watermarks
- Other fixed-position UI elements

## Output Files

### .pb.zst File

Analysis data file using Protocol Buffers + Zstd compression, containing:
- Video metadata (resolution, frame rate, total frames)
- Per-frame difference pixel counts
- Suggested threshold

### .fcpxml File

Final Cut Pro X marker file containing:
- Start markers (start1, start2, ...)
- Stop markers (stop1, stop2, ...)
- Video format information

**Import method:**
1. Open Final Cut Pro X
2. File → Import → XML...
3. Select generated `.fcpxml` file

## Makefile Commands

```bash
make proto      # Generate protobuf code
make build      # Build binary (auto-generates protobuf)
make install    # Build and install to system path
make uninstall  # Uninstall from system path
make clean      # Clean build files
make run        # Build and run
make help       # Show help
```

## License

GPL-3.0
