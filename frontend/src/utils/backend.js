// Wails 运行时会注入 window.go.main.App；在纯浏览器 dev 环境下不存在。
// 这里统一兜底，保证界面在无后端时仍可渲染（只读、不崩溃）。

export function isBackendAvailable() {
  return (
    typeof window !== 'undefined' &&
    !!(window.go && window.go.main && window.go.main.App)
  )
}

/**
 * 调用一个 wailsjs 包装函数；无后端时抛出可读错误，由调用方捕获展示。
 * @param {Function} fn wailsjs/go/main/App 里的包装函数
 * @param  {...any} args 透传参数
 */
export async function callBackend(fn, ...args) {
  if (typeof fn !== 'function' || !isBackendAvailable()) {
    throw new Error('Backend is unavailable in this environment.')
  }
  return fn(...args)
}

export function errorMessage(err, fallback = 'Operation failed.') {
  if (!err) return fallback
  if (typeof err === 'string') return err
  if (typeof err.message === 'string' && err.message) return err.message
  return String(err)
}

export function isValidPort(value) {
  const port = Number(value)
  return Number.isInteger(port) && port >= 1 && port <= 65535
}
