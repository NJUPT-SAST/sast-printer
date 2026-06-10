package conversion

import (
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"goprint/api/pb"
	"goprint/config"

	"github.com/go-pdf/fpdf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var supportedImageExt = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
}

func FileExtLower(name string) string {
	return strings.ToLower(filepath.Ext(strings.TrimSpace(name)))
}

func acceptedOfficeExtMap(cfg *config.Config) map[string]bool {
	out := map[string]bool{}
	if cfg == nil {
		return out
	}
	for _, f := range cfg.OfficeConversion.AcceptedFormats {
		nf := strings.TrimPrefix(strings.TrimSpace(strings.ToLower(f)), ".")
		if nf != "" {
			out["."+nf] = true
		}
	}
	return out
}

func IsSupportedUploadFile(cfg *config.Config, name string) bool {
	ext := FileExtLower(name)
	if ext == ".pdf" {
		return true
	}
	if acceptedOfficeExtMap(cfg)[ext] {
		return true
	}
	return supportedImageExt[ext]
}

func IsOfficeConvertible(cfg *config.Config, name string) bool {
	ext := FileExtLower(name)
	if ext == ".pdf" {
		return false
	}
	return acceptedOfficeExtMap(cfg)[ext]
}

func IsImageConvertible(name string) bool {
	return supportedImageExt[FileExtLower(name)]
}

func SupportedUploadExtensions(cfg *config.Config) []string {
	seen := map[string]struct{}{"pdf": {}}
	fileTypes := []string{"pdf"}

	for ext := range acceptedOfficeExtMap(cfg) {
		normalized := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		fileTypes = append(fileTypes, normalized)
	}

	for ext := range supportedImageExt {
		normalized := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		fileTypes = append(fileTypes, normalized)
	}

	if len(fileTypes) > 1 {
		sort.Strings(fileTypes[1:])
	}
	return fileTypes
}

func pathIsUnderDir(path string, dir string) bool {
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(resolvedDir, resolvedPath)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func ConvertImageToPDF(cfg *config.Config, sourcePath string) (string, error) {
	if err := os.MkdirAll(cfg.OfficeConversion.OutputDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create conversion output dir: %w", err)
	}

	f, err := os.Open(sourcePath)
	if err != nil {
		return "", fmt.Errorf("failed to open image source: %w", err)
	}
	defer f.Close()

	imgCfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return "", fmt.Errorf("failed to decode image metadata: %w", err)
	}
	if imgCfg.Width <= 0 || imgCfg.Height <= 0 {
		return "", fmt.Errorf("invalid image dimensions")
	}
	pixels := int64(imgCfg.Width) * int64(imgCfg.Height)
	if cfg.Printing.MaxImagePixels > 0 && pixels > int64(cfg.Printing.MaxImagePixels) {
		return "", fmt.Errorf("image has %d pixels, exceeding configured limit of %d", pixels, cfg.Printing.MaxImagePixels)
	}

	orientation := "P"
	pageW := 210.0
	pageH := 297.0
	if imgCfg.Width > imgCfg.Height {
		orientation = "L"
		pageW = 297.0
		pageH = 210.0
	}

	pdf := fpdf.New(orientation, "mm", "A4", "")
	pdf.AddPage()

	imgW := float64(imgCfg.Width)
	imgH := float64(imgCfg.Height)
	maxW := pageW * 0.9
	maxH := pageH * 0.9

	drawW := maxW
	drawH := drawW * (imgH / imgW)
	if drawH > maxH {
		drawH = maxH
		drawW = drawH * (imgW / imgH)
	}

	x := (pageW - drawW) / 2
	y := (pageH - drawH) / 2

	imgType := strings.TrimPrefix(FileExtLower(sourcePath), ".")
	if imgType == "jpg" {
		imgType = "jpeg"
	}

	imgOpts := fpdf.ImageOptions{ImageType: imgType, ReadDpi: true}
	pdf.ImageOptions(sourcePath, x, y, drawW, drawH, false, imgOpts, 0, "")

	outFile, err := os.CreateTemp(cfg.OfficeConversion.OutputDir, "goprint-image-*.pdf")
	if err != nil {
		return "", fmt.Errorf("failed to create image pdf output file: %w", err)
	}
	outPath := outFile.Name()
	_ = outFile.Close()

	if err := pdf.OutputFileAndClose(outPath); err != nil {
		_ = os.Remove(outPath)
		return "", fmt.Errorf("failed to render image pdf: %w", err)
	}

	return outPath, nil
}

func ConvertOfficeToPDF(ctx context.Context, cfg *config.Config, sourcePath string) (string, error) {
	if !cfg.OfficeConversion.Enabled {
		return "", fmt.Errorf("office conversion is disabled")
	}

	timeout, err := time.ParseDuration(strings.TrimSpace(cfg.OfficeConversion.RequestTimeout))
	if err != nil || timeout <= 0 {
		return "", fmt.Errorf("invalid office conversion request timeout: %s", cfg.OfficeConversion.RequestTimeout)
	}

	if err := os.MkdirAll(cfg.OfficeConversion.OutputDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create conversion output dir: %w", err)
	}

	rpcCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := grpc.DialContext(rpcCtx, cfg.OfficeConversion.GRPCAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return "", fmt.Errorf("failed to connect office converter grpc server: %w", err)
	}
	defer conn.Close()

	client := pb.NewOfficeConverterServiceClient(conn)
	resp, err := client.ConvertToPdf(rpcCtx, &pb.ConversionRequest{
		SourceFilePath: sourcePath,
		TargetFormat:   "pdf",
	})
	if err != nil {
		return "", fmt.Errorf("office conversion rpc call failed: %w", err)
	}

	if !resp.Success {
		if strings.TrimSpace(resp.ErrorMessage) == "" {
			return "", fmt.Errorf("office conversion failed with unknown error")
		}
		if strings.TrimSpace(resp.ErrorCode) != "" {
			return "", fmt.Errorf("office conversion failed (%s): %s", resp.ErrorCode, resp.ErrorMessage)
		}
		return "", fmt.Errorf("office conversion failed: %s", resp.ErrorMessage)
	}

	convertedPath := strings.TrimSpace(resp.OutputFilePath)
	if convertedPath == "" {
		return "", fmt.Errorf("office conversion returned empty output file path")
	}

	info, err := os.Stat(convertedPath)
	if err != nil {
		return "", fmt.Errorf("converted file not accessible: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("converted output path is a directory")
	}
	if !pathIsUnderDir(convertedPath, cfg.OfficeConversion.OutputDir) {
		return "", fmt.Errorf("converted output path is outside office_conversion.output_dir")
	}

	return convertedPath, nil
}
