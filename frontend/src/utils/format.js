// 展示用格式化小工具（无后端依赖）。

/**
 * 延迟毫秒数 → 短文本：<=0 显示占位符，<1s 为 ms，<60s 为 s，否则为 m。
 * @param {number} latencyMs
 * @param {string} noneText 无有效值时的占位文本（一般为 i18n 的 app.common.none）
 */
export function formatLatency(latencyMs, noneText = '--') {
  const value = Number(latencyMs)
  if (!Number.isFinite(value) || value <= 0) return noneText
  if (value < 1000) return `${Math.round(value)}ms`
  if (value < 60000) return `${(value / 1000).toFixed(1)}s`
  return `${(value / 60000).toFixed(1)}m`
}
