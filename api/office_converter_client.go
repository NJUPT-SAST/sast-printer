package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"goprint/api/pb"
	"goprint/config"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func fileExtLower(name string) string {
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

func isSupportedUploadFile(cfg *config.Config, name string) bool {
	ext := fileExtLower(name)
	if ext == ".pdf" {
		return true
	}
	return acceptedOfficeExtMap(cfg)[ext]
}

func isOfficeConvertible(cfg *config.Config, name string) bool {
	ext := fileExtLower(name)
	if ext == ".pdf" {
		return false
	}
	return acceptedOfficeExtMap(cfg)[ext]
}

func convertOfficeToPDF(ctx context.Context, cfg *config.Config, sourcePath string) (string, error) {
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

	return convertedPath, nil
}
