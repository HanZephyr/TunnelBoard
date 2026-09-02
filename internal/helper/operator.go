package helper

import "context"

// Operator 是特权操作调用面（biz.HelperClient 的结构等价物）：
// Windows 经命名管道访问 helper 服务；macOS/Linux 用每次操作单独提权的本地直连客户端。
type Operator interface {
	Call(req Request) (Response, error)
	Ping() (string, error)
	EnsureInstalled() error
	EnsureLoopbackHTTPSRedirect(ctx context.Context) error
}
