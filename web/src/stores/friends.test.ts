import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useFriendsStore } from './friends'

// Same shape as account.test.ts: stub fetch so these stay store-level tests,
// which is where every other frontend test in this project sits.
function stubFetch(responses: { ok?: boolean; body?: unknown }[]) {
  const calls: { path: string; init?: RequestInit }[] = []
  let i = 0
  vi.stubGlobal('fetch', vi.fn(async (path: string, init?: RequestInit) => {
    calls.push({ path, init })
    const r = responses[Math.min(i++, responses.length - 1)]
    return { ok: r.ok !== false, json: async () => r.body ?? {} } as Response
  }))
  return calls
}

// Tests run in Vitest's default node environment -- this project installs no
// jsdom -- so `location` has to be stubbed the same way `fetch` is. The store
// reads it directly because it only ever runs in a browser, which is what
// ResultsView already does when it passes location.origin into shareText.
const ORIGIN = 'https://trivial.test'

beforeEach(() => {
  setActivePinia(createPinia())
  vi.stubGlobal('location', { origin: ORIGIN })
})

describe('defaultNickname', () => {
  it('takes the local part of the address', () => {
    const store = useFriendsStore()
    expect(store.defaultNickname('jordan@example.com')).toBe('jordan')
  })

  it('falls back when there is no usable local part', () => {
    const store = useFriendsStore()
    expect(store.defaultNickname('')).toBe('A player')
    expect(store.defaultNickname('@example.com')).toBe('A player')
  })
})

describe('mint', () => {
  it('stores the link and exposes an absolute share url', async () => {
    stubFetch([{ body: { token: 'abc', url: '/f/abc', nickname: 'Sam' } }])
    const store = useFriendsStore()
    await store.mint('Sam')
    expect(store.invite?.token).toBe('abc')
    expect(store.shareUrl).toBe(`${ORIGIN}/f/abc`)
    expect(store.error).toBe('')
  })

  it('surfaces a failure instead of pretending it has a link', async () => {
    stubFetch([{ ok: false, body: { code: 'not_signed_in', message: 'Sign in first.' } }])
    const store = useFriendsStore()
    await store.mint('Sam')
    expect(store.invite).toBeNull()
    expect(store.error).toBe('Sign in first.')
  })
})

describe('load', () => {
  it('names the sender', async () => {
    stubFetch([{ body: { nickname: 'Sam' } }])
    const store = useFriendsStore()
    await store.load('abc')
    expect(store.senderName).toBe('Sam')
    expect(store.status).toBe('ready')
  })

  it('marks a dead link rather than showing an error box', async () => {
    stubFetch([{ ok: false, body: { code: 'no_such_invite', message: 'gone' } }])
    const store = useFriendsStore()
    await store.load('abc')
    expect(store.status).toBe('dead')
  })
})

describe('accept', () => {
  it('reports added and already_friends the same way', async () => {
    stubFetch([{ body: { nickname: 'Sam', status: 'added' } }])
    const store = useFriendsStore()
    await store.accept('abc')
    expect(store.status).toBe('accepted')
    expect(store.senderName).toBe('Sam')
  })

  it('marks your own link', async () => {
    stubFetch([{ ok: false, body: { code: 'self_invite', message: 'own link' } }])
    const store = useFriendsStore()
    await store.accept('abc')
    expect(store.status).toBe('self')
  })

  it('marks a dead link', async () => {
    stubFetch([{ ok: false, body: { code: 'no_such_invite', message: 'gone' } }])
    const store = useFriendsStore()
    await store.accept('abc')
    expect(store.status).toBe('dead')
  })
})
