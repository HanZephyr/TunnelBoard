export function normalizeLogCursor(result) {
  const cursor = result?.nextCursor || result?.NextCursor
  if (!cursor) return null
  const generation = Number(cursor.generation ?? cursor.Generation)
  const offset = Number(cursor.offset ?? cursor.Offset)
  if (!Number.isFinite(generation) || generation < 1 || !Number.isFinite(offset) || offset < 0) return null
  return { generation, offset }
}

export function mergeLogTail(existingLines, result, maxLines) {
  const incoming = Array.isArray(result?.lines) ? result.lines : Array.isArray(result?.Lines) ? result.Lines : []
  const base = result?.rotated || result?.Rotated ? [] : existingLines
  return [...base, ...incoming].slice(-maxLines)
}
