package api

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// LayoutResult N-up layout calculation result
type LayoutResult struct {
	Cols   int
	Rows   int
	Rotate bool
}

const a4W = 595.0 // A4 width in PDF points
const a4H = 842.0 // A4 height in PDF points

var nupGrids = map[int]LayoutResult{
	2: {Cols: 2, Rows: 1},
	4: {Cols: 2, Rows: 2},
	6: {Cols: 3, Rows: 2},
}

func validNup(nup int) bool {
	_, ok := nupGrids[nup]
	return ok
}

// getOptimalLayout calculates optimal N-up layout.
// Tests portrait and landscape A4 orientations × column/row combinations,
// selects the one with highest page coverage.
func getOptimalLayout(pageWidth, pageHeight float64, nup int) LayoutResult {
	if pageWidth <= 0 || pageHeight <= 0 {
		if g, ok := nupGrids[nup]; ok {
			return g
		}
		return LayoutResult{Cols: 1, Rows: 1}
	}

	bestCoverage := -1.0
	bestAspectMatch := 0.0
	best := nupGrids[nup]
	if best.Cols == 0 {
		best = LayoutResult{Cols: 1, Rows: 1}
	}
	bestRotate := false

	srcAspect := pageWidth / pageHeight
	sheets := []struct{ w, h float64 }{{a4W, a4H}, {a4H, a4W}}

	for _, sheet := range sheets {
		for cols := nup; cols >= 1; cols-- {
			if nup%cols != 0 {
				continue
			}
			rows := nup / cols
			cellW := sheet.w / float64(cols)
			cellH := sheet.h / float64(rows)
			cellAspect := cellW / cellH

			scale := math.Min(cellW/pageWidth, cellH/pageHeight)
			coverage := (pageWidth * scale * pageHeight * scale * float64(nup)) / (sheet.w * sheet.h)
			aspectMatch := -math.Abs(cellAspect - srcAspect)

			if coverage > bestCoverage+1e-6 ||
				(math.Abs(coverage-bestCoverage) < 1e-6 && aspectMatch > bestAspectMatch) {
				bestCoverage = coverage
				bestAspectMatch = aspectMatch
				best = LayoutResult{Cols: cols, Rows: rows}
				bestRotate = sheet.w == a4H
			}
		}
	}

	best.Rotate = bestRotate
	return best
}

// createNupPDF creates an N-up imposition PDF using pdfcpu.
// sourcePath: source PDF
// nup: 2/4/6
// direction: "horizontal" | "vertical"
// selectedPages: nil=all pages; non-nil=specific page numbers (1-based)
// Returns output path, cleanup function, and error.
func createNupPDF(sourcePath string, nup int, direction string, selectedPages []int) (string, func(), error) {
	if !validNup(nup) {
		return "", nil, fmt.Errorf("unsupported nup: %d", nup)
	}

	pageDims, err := api.PageDimsFile(sourcePath)
	if err != nil {
		return "", nil, fmt.Errorf("read page dims: %w", err)
	}
	if len(pageDims) == 0 {
		return "", nil, fmt.Errorf("pdf has no pages")
	}

	totalPages := len(pageDims)
	indices := selectedPages
	if len(indices) == 0 {
		indices = make([]int, totalPages)
		for i := range indices {
			indices[i] = i + 1
		}
	}
	if len(indices) == 0 {
		return "", nil, fmt.Errorf("no pages selected")
	}

	refDim := pageDims[indices[0]-1]
	layout := getOptimalLayout(refDim.Width, refDim.Height, nup)

	workDir, err := os.MkdirTemp("", "goprint-nup-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(workDir) }

	sheetW := a4W
	sheetH := a4H
	if layout.Rotate {
		sheetW, sheetH = sheetH, sheetW
	}
	cellW := sheetW / float64(layout.Cols)
	cellH := sheetH / float64(layout.Rows)

	// Step 1: Scale each page to cell size
	scaledPaths := make([]string, 0, len(indices))
	for i, pageNum := range indices {
		scaledPath := filepath.Join(workDir, fmt.Sprintf("scaled-%04d.pdf", i))
		dim := pageDims[pageNum-1]
		scaleX := cellW / dim.Width * 0.98
		scaleY := cellH / dim.Height * 0.98
		scale := math.Min(scaleX, scaleY)
		pageStr := strconv.Itoa(pageNum)
		resize := &model.Resize{Scale: scale}
		if err := api.ResizeFile(sourcePath, scaledPath, []string{pageStr}, resize, nil); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("scale page %d: %w", pageNum, err)
		}
		scaledPaths = append(scaledPaths, scaledPath)
	}

	// Step 2: Reorder for vertical direction
	if direction == "vertical" && layout.Rows > 1 && layout.Cols > 1 {
		reorderForVertical(scaledPaths, layout.Cols, layout.Rows)
	}

	// Step 3: N-up merge
	outFile, err := os.CreateTemp(workDir, "nup-output-*.pdf")
	if err != nil {
		cleanup()
		return "", nil, err
	}
	outPath := outFile.Name()
	_ = outFile.Close()

	nupConf := &model.NUp{
		Grid:    &types.Dim{Width: float64(layout.Cols), Height: float64(layout.Rows)},
		PageDim: &types.Dim{Width: sheetW, Height: sheetH},
	}

	if err := api.NUpFile(scaledPaths, outPath, nil, nupConf, model.NewDefaultConfiguration()); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("nup merge: %w", err)
	}

	return outPath, func() { _ = os.RemoveAll(workDir) }, nil
}

// reorderForVertical reorders pages for vertical (column-major) fill
func reorderForVertical(paths []string, cols, rows int) {
	reordered := make([]string, len(paths))
	idx := 0
	for col := 0; col < cols; col++ {
		for row := 0; row < rows; row++ {
			srcIdx := row*cols + col
			if srcIdx < len(paths) {
				reordered[idx] = paths[srcIdx]
				idx++
			}
		}
	}
	copy(paths, reordered)
}
