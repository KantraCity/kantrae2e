// Profile photos, loaded on demand and cached by (user, photo id): a new
// photo has a new id, so a changed avatar is fetched again.
import { Messenger } from './api'

class Photos {
  urls = $state<Record<string, string>>({})
  private loading = new Set<string>()

  url(userId: string | undefined, avatarId: string | undefined): string | null {
    if (!userId || !avatarId) return null
    const key = userId + ':' + avatarId
    const u = this.urls[key]
    if (u !== undefined) return u || null
    if (!this.loading.has(key)) {
      this.loading.add(key)
      Messenger.Avatar(userId)
        .then((d) => (this.urls[key] = d ?? ''))
        .catch(() => (this.urls[key] = ''))
        .finally(() => this.loading.delete(key))
    }
    return null
  }

  /** Our own new photo: show it immediately. */
  put(userId: string, avatarId: string, dataUrl: string) {
    this.urls[userId + ':' + avatarId] = dataUrl
  }
}

export const photos = new Photos()
