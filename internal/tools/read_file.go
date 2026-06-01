// 内置工具：ReadFile（文件读取工具）。
//
// 提供受限工作区（Sandboxed Workspace）内的安全文件读取能力，关键安全机制：
//  1. 沙箱边界（Sandbox Boundary）：所有路径通过 safePath 校验，
//     拒绝类似 "../../etc/passwd" 的路径遍历攻击（Path Traversal Attack）
//  2. 长度截断保护（Length-Cap Guard）：使用 io.LimitReader 限制单次读取量，
//     防止超大文件占满 LLM 的上下文窗口（Context Window）导致 Token 爆炸
//  3. 分页支持：offset（字节偏移）+ limit（读取字节数）可选参数，
//     配合 OffloadHook 实现超大 offload 文件的分段检索
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/harness9/internal/schema"
)

// maxReadLen 单次文件读取的最大字节数（Max Read Bytes）。
// 未指定 limit 参数时的默认上限。
const maxReadLen = 4096

// ReadFileTool 实现了 BaseTool 接口，提供受限工作区内的安全文件读取能力。
type ReadFileTool struct {
	workDir string
}

// NewReadFileTool 创建绑定到指定工作区的文件读取工具。
func NewReadFileTool(workDir string) *ReadFileTool {
	return &ReadFileTool{workDir: filepath.Clean(workDir)}
}

// Name 返回工具标识符 "read_file"。
func (t *ReadFileTool) Name() string { return "read_file" }

// Definition 返回工具元信息，包含描述和 JSON Schema 参数定义。
func (t *ReadFileTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        t.Name(),
		Description: "读取指定路径的文件内容。请提供相对工作区的相对路径。支持 offset/limit 参数分页读取大文件。",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "要读取的文件路径，如 cmd/harness9/main.go",
				},
				"offset": map[string]interface{}{
					"type":        "integer",
					"description": "起始字节偏移（默认 0，从文件开头读取）",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": fmt.Sprintf("读取字节数（默认 %d）", maxReadLen),
				},
			},
			"required": []string{"path"},
		},
	}
}

// readFileArgs 定义 read_file 工具的 JSON 参数结构。
// Offset 和 Limit 为可选参数；零值时使用默认行为（从头读取，maxReadLen 上限）。
type readFileArgs struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// Execute 执行文件读取操作，支持 offset/limit 分页。
func (t *ReadFileTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input readFileArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}

	fullPath, err := safePath(t.workDir, input.Path)
	if err != nil {
		return "", err
	}

	unlock := RLockPath(fullPath)
	defer unlock()

	file, err := os.Open(fullPath)
	if err != nil {
		return "", fmt.Errorf("打开文件失败: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("获取文件信息失败: %w", err)
	}
	totalSize := info.Size()

	offset := input.Offset
	if offset > 0 {
		if offset >= totalSize {
			return fmt.Sprintf("[offset=%d 超出文件大小（%d 字节），无内容可读。]", offset, totalSize), nil
		}
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return "", fmt.Errorf("定位文件偏移失败: %w", err)
		}
	}

	limit := input.Limit
	if limit <= 0 {
		limit = maxReadLen
	}

	// 多读 1 字节用于检测是否真的超出上限
	content, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return "", fmt.Errorf("读取文件内容失败: %w", err)
	}

	if len(content) > limit {
		nextOffset := offset + int64(limit)
		return string(content[:limit]) + fmt.Sprintf(
			"\n\n...[内容已截断，已读取 offset=%d 起的 %d 字节，文件总大小 %d 字节，如需继续读取请使用 offset=%d]...",
			offset, limit, totalSize, nextOffset,
		), nil
	}

	return string(content), nil
}
