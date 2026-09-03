import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '../api'
import type { AcceptResult, APIError, FriendInvite, PublicInvite } from '../types'

// The landing page's five states. 'loading' is the initial one so the page
// renders nothing decisive before the server has answered.
export type InviteStatus = 'loading' | 'ready' | 'accepted' | 'self' | 'dead'

// Mirrors friends.DefaultNickname in Go. Two implementations of one rule is a
// smell, but the alternative is a round trip before the input can be prefilled,
// and both are covered by tests that assert the same examples.
const FALLBACK_NICKNAME = 'A player'
const MAX_NICKNAME = 40

export const useFriendsStore = defineStore('friends', () => {
  // Sender side.
  const invite = ref<FriendInvite | null>(null)
  const nickname = ref('')
  // Recipient side.
  const senderName = ref('')
  const status = ref<InviteStatus>('loading')

  const loading = ref(false)
  const error = ref('')

  // Relative from the server, absolute for the share sheet -- which is why no
  // BASE_URL setting has to exist server-side.
  const shareUrl = computed(() => (invite.value ? `${location.origin}${invite.value.url}` : ''))

  function fail(e: unknown) {
    error.value = (e as APIError)?.message || 'Something went wrong.'
  }
  function codeOf(e: unknown) {
    return (e as APIError)?.code ?? ''
  }

  function defaultNickname(email: string) {
    const local = (email ?? '').trim().split('@')[0]?.trim() ?? ''
    if (!local) return FALLBACK_NICKNAME
    return [...local].slice(0, MAX_NICKNAME).join('')
  }

  // mint is called when the panel opens, not when Share is pressed. The token
  // is stable and independent of the nickname, so having the URL in hand early
  // is what lets the Share button call navigator.share() synchronously --
  // an awaited fetch first would consume the transient user activation Safari
  // requires, and the share sheet would silently fail to open.
  async function mint(name: string) {
    error.value = ''
    loading.value = true
    try {
      const got = await api<FriendInvite>('/api/friends/invite', {
        method: 'POST',
        body: JSON.stringify({ nickname: name }),
      })
      invite.value = got
      nickname.value = got.nickname
    } catch (e) {
      fail(e)
    } finally {
      loading.value = false
    }
  }

  // saveNickname is deliberately separate from mint and is never awaited by the
  // share handler. The link is already valid; a failed rename means it carries
  // the previous name, which is the right failure for a button whose job was to
  // produce a link.
  async function saveNickname(name: string) {
    if (!invite.value || name.trim() === invite.value.nickname) return
    try {
      const got = await api<FriendInvite>('/api/friends/invite', {
        method: 'POST',
        body: JSON.stringify({ nickname: name }),
      })
      invite.value = got
      nickname.value = got.nickname
    } catch (e) {
      fail(e)
    }
  }

  async function load(token: string) {
    error.value = ''
    status.value = 'loading'
    try {
      const got = await api<PublicInvite>(`/api/friends/invite/${encodeURIComponent(token)}`)
      senderName.value = got.nickname
      status.value = 'ready'
    } catch (e) {
      // Every failure to read an invite is a dead link from the visitor's point
      // of view -- a 404, a 503, a dropped connection -- so they collapse into
      // one state the page renders, rather than an error box over nothing they
      // can act on or retry.
      status.value = 'dead'
      fail(e)
    }
  }

  async function accept(token: string) {
    error.value = ''
    loading.value = true
    try {
      const got = await api<AcceptResult>(`/api/friends/invite/${encodeURIComponent(token)}/accept`, {
        method: 'POST',
      })
      // added and already_friends render identically. Telling the visitor which
      // one it was would report on a friendship they may not remember making.
      senderName.value = got.nickname
      status.value = 'accepted'
    } catch (e) {
      switch (codeOf(e)) {
        case 'self_invite':
          status.value = 'self'
          break
        case 'no_such_invite':
          status.value = 'dead'
          break
        default:
          fail(e)
      }
    } finally {
      loading.value = false
    }
  }

  return {
    invite, nickname, senderName, status, loading, error,
    shareUrl, defaultNickname, mint, saveNickname, load, accept,
  }
})
