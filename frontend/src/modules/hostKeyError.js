// 后端把可确认的 SSH host key 失败编码为带端点和指纹的错误消息。
// 此处只解析这两类明确的、用户可处理的错误；其他连接错误必须原样展示。
const HOSTKEY_UNKNOWN_RE = /ssh host key unknown: (.+):(\d+) fingerprint (\S+)/
const HOSTKEY_MISMATCH_RE = /ssh host key mismatch: (.+):(\d+) fingerprint changed \(stored (\S+), got (\S+)\)/

export function parseHostKeyError(message) {
  if (typeof message !== 'string') return null
  const mismatch = message.match(HOSTKEY_MISMATCH_RE)
  if (mismatch) {
    return {
      kind: 'mismatch',
      host: mismatch[1],
      port: Number(mismatch[2]),
      storedFingerprint: mismatch[3],
      fingerprint: mismatch[4]
    }
  }
  const unknown = message.match(HOSTKEY_UNKNOWN_RE)
  if (unknown) {
    return {
      kind: 'unknown',
      host: unknown[1],
      port: Number(unknown[2]),
      storedFingerprint: '',
      fingerprint: unknown[3]
    }
  }
  return null
}
