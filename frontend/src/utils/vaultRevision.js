import { errorMessage } from './backend.js'

export const vaultRefreshKey = Symbol('refreshVault')

// 冲突后只刷新快照，保留草稿并由用户重新确认，不重放旧命令。
export async function recoverRevisionConflict(error, refresh, t) {
  const message = errorMessage(error)
  if (!message.includes('application: revision conflict')) return message
  try {
    if (await refresh()) return t('hosts.errors.revisionConflict')
  } catch (_) {
    // 刷新失败时不能提示用户直接重试保存。
  }
  return t('hosts.errors.revisionRefreshFailed')
}
