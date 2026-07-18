package helper

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/HanZephyr/TunnelBoard/internal/route"
)

// 受托管区块标记：helper 只触碰这两个标记之间的行，区块外内容保持原样。
const (
	BlockBegin = "# >>> TunnelBoard Managed (do not edit) >>>"
	BlockEnd   = "# <<< TunnelBoard Managed <<<"
)

// 写入算法的伴生文件后缀：.bak 为写前备份（回滚来源），.tmp 为原子替换的临时文件。
const (
	backupSuffix = ".tunnelboard.bak"
	tempSuffix   = ".tunnelboard.tmp"
)

// RenderManagedHosts 把 content 中首个标记区块替换为 entries 的渲染结果：
// 无区块时在文末追加；entries 为空时移除整个区块（含标记）。
// 行尾统一输出 CRLF（Windows hosts 惯例；输入为 LF 时也归一为 CRLF），
// 区块外的行内容与顺序保持不变，文件结尾保证恰好一个换行。
func RenderManagedHosts(content string, entries []route.HostEntry) string {
	lines := splitLines(content)
	begin, end := findBlock(lines)
	block := renderBlock(entries)

	var out []string
	switch {
	case begin < 0:
		// 无区块：保留全部内容并在文末追加新区块（entries 为空则原样返回）。
		out = append(out, lines...)
		out = append(out, block...)
	case end < 0:
		// 只有 Begin 没有 End 视为区块延续到文件尾，整体替换，避免区块内容无限膨胀。
		out = append(out, lines[:begin]...)
		out = append(out, block...)
	default:
		out = append(out, lines[:begin]...)
		out = append(out, block...)
		out = append(out, lines[end+1:]...)
	}
	return joinCRLF(out)
}

// WriteManagedHosts 原子地重写 path 的受托管区块：
// 先把当前内容备份到 <path>.tunnelboard.bak，再写 <path>.tunnelboard.tmp 并 rename 替换；
// 备份之后任一步失败都会尝试从 .bak 恢复原文件并返回错误（回滚为纯文件操作）。
// path 不存在时按空内容处理并创建。
func WriteManagedHosts(path string, entries []route.HostEntry) error {
	original, err := readHostsOrEmpty(path)
	if err != nil {
		return err
	}
	rendered := RenderManagedHosts(string(original), entries)

	bak := path + backupSuffix
	tmp := path + tempSuffix
	if err := os.WriteFile(bak, original, 0o644); err != nil {
		return fmt.Errorf("helper: backup hosts file: %w", err)
	}
	if err := os.WriteFile(tmp, []byte(rendered), 0o644); err != nil {
		return rollback(path, bak, fmt.Errorf("helper: write temp hosts file: %w", err))
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return rollback(path, bak, fmt.Errorf("helper: replace hosts file: %w", err))
	}
	slog.Info("managed hosts written", "entries", len(entries), "path", path)
	return nil
}

// readHostsOrEmpty 读取 hosts 文件；文件不存在视为空内容。
func readHostsOrEmpty(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("helper: read hosts file: %w", err)
	}
	return data, nil
}

// rollback 从 .bak 恢复原文件；恢复失败时把回滚错误一并报告。
func rollback(path, bak string, cause error) error {
	data, err := os.ReadFile(bak)
	if err == nil {
		err = os.WriteFile(path, data, 0o644)
	}
	if err != nil {
		return fmt.Errorf("%w (additionally rollback failed: %v)", cause, err)
	}
	return cause
}

// ParseManagedHosts 从 hosts 文件内容解析受托管区块内的条目（回读用于快照与回滚）。
// 无区块或区块为空返回 nil；忽略空行与注释行。
func ParseManagedHosts(content string) []route.HostEntry {
	lines := splitLines(content)
	begin, end := findBlock(lines)
	if begin < 0 || end < 0 {
		return nil
	}
	var entries []route.HostEntry
	for _, line := range lines[begin+1 : end] {
		fields := strings.Fields(line)
		if len(fields) == 2 && !strings.HasPrefix(fields[0], "#") {
			entries = append(entries, route.HostEntry{IP: fields[0], Domain: fields[1]})
		}
	}
	return entries
}

// splitLines 按 \n 切分并剥掉行尾 \r，兼容 LF 与 CRLF 输入；
// 末尾恰好一个换行不产生空行（即 "a\n" 与 "a" 得到相同的行集合）。
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

// findBlock 定位首个标记区块：begin 为 BlockBegin 行号，end 为其后首个 BlockEnd 行号；未找到返回 -1。
func findBlock(lines []string) (begin, end int) {
	begin = -1
	for i, line := range lines {
		if line == BlockBegin {
			begin = i
			break
		}
	}
	if begin < 0 {
		return -1, -1
	}
	for i := begin + 1; i < len(lines); i++ {
		if lines[i] == BlockEnd {
			return begin, i
		}
	}
	return begin, -1
}

// renderBlock 生成区块行（含标记），每行 "<ip> <domain>"；
// 条目按传入顺序输出（上游 route.PlanHosts 已排序）。entries 为空返回 nil（表示移除区块）。
func renderBlock(entries []route.HostEntry) []string {
	if len(entries) == 0 {
		return nil
	}
	lines := make([]string, 0, len(entries)+2)
	lines = append(lines, BlockBegin)
	for _, e := range entries {
		lines = append(lines, e.IP+" "+e.Domain)
	}
	lines = append(lines, BlockEnd)
	return lines
}

// joinCRLF 用 CRLF 连接所有行，并在末尾补恰好一个 CRLF；无行时返回空串。
func joinCRLF(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}
