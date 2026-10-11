package adapters

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// Codex 的 local_thread_store_compression 会把冷的 rollout 文件从
// <name>.jsonl 改写为 <name>.jsonl.zst。所有 rollout 读取路径统一经
// openCodexRolloutFile 打开：压缩文件流式解压，调用方继续按明文逐行处理，
// 不得整文件载入内存（sessions 目录可达数十 GB）。
func isCodexRolloutZstPath(path string) bool {
	return strings.HasSuffix(strings.ToLower(filepath.Base(path)), ".jsonl.zst")
}

func isCodexRolloutFileName(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".jsonl") || strings.HasSuffix(lower, ".jsonl.zst")
}

// DiscoverFiles 按 扩展名 匹配，".zst" 会匹配任意压缩文件；codex 源里只有
// rollout（.jsonl / .jsonl.zst）是合法输入，其余一律过滤掉。
func filterCodexRolloutFiles(files []string) []string {
	filtered := make([]string, 0, len(files))
	for _, file := range files {
		if isCodexRolloutFileName(filepath.Base(file)) {
			filtered = append(filtered, file)
		}
	}
	return filtered
}

func trimCodexRolloutStem(name string) string {
	if strings.HasSuffix(strings.ToLower(name), ".jsonl.zst") {
		return name[:len(name)-len(".jsonl.zst")]
	}
	return strings.TrimSuffix(name, filepath.Ext(name))
}

func openCodexRolloutFile(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if !isCodexRolloutZstPath(path) {
		return f, nil
	}
	decoder, err := zstd.NewReader(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &codexRolloutZstReader{file: f, decoder: decoder}, nil
}

type codexRolloutZstReader struct {
	file    *os.File
	decoder *zstd.Decoder
}

func (r *codexRolloutZstReader) Read(p []byte) (int, error) { return r.decoder.Read(p) }

func (r *codexRolloutZstReader) Close() error {
	r.decoder.Close()
	return r.file.Close()
}
