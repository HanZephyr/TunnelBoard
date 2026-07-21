import test from 'node:test'
import assert from 'node:assert/strict'
import { mergeLogTail, normalizeLogCursor } from './logTailCursor.js'

test('日志轮转后替换旧 generation 内容而不是重复追加', () => {
  const result = {
    lines: ['generation-2'],
    nextCursor: { generation: 2, offset: 14 },
    rotated: true
  }
  assert.deepEqual(mergeLogTail(['generation-1'], result, 2000), ['generation-2'])
  assert.deepEqual(normalizeLogCursor(result), { generation: 2, offset: 14 })
})

test('同 generation 增量读取保留已有内容并限制行数', () => {
  const result = { lines: ['two', 'three'], nextCursor: { generation: 2, offset: 30 } }
  assert.deepEqual(mergeLogTail(['one'], result, 2), ['two', 'three'])
})
