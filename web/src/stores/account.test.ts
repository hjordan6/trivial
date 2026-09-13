import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAccountStore } from './account'

// The store talks to the server through api.ts, which uses fetch and throws the
// parsed error body. Stubbing fetch keeps these tests at the store level, which
// is where every other frontend test in this project sits.
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

beforeEach(() => { setActivePinia(createPinia()) })

describe('probe', () => {
  it('reads the signed-in address off the server', async () => {
    stubFetch([{ body: { available: true, signed_in: true, email: 'a@b.test' } }])
    const store = useAccountStore()
    await store.probe()
    expect(store.available).toBe(true)
    expect(store.signedIn).toBe(true)
    expect(store.email).toBe('a@b.test')
    expect(store.step).toBe('done')
  })

  it('reports a server with no sign-in, so the prompt can be hidden', async () => {
    stubFetch([{ body: { available: false, signed_in: false } }])
    const store = useAccountStore()
    await store.probe()
    expect(store.available).toBe(false)
    expect(store.signedIn).toBe(false)
  })

  it('treats an unanswerable probe as no sign-in rather than an error', async () => {
    stubFetch([{ ok: false, body: { code: 'boom', message: 'nope' } }])
    const store = useAccountStore()
    await store.probe()
    expect(store.available).toBe(false)
    expect(store.error).toBe('')
  })
})

describe('requestCode', () => {
  it('advances to the code step and remembers the address', async () => {
    stubFetch([{ body: { status: 'sent', message: 'On its way.' } }])
    const store = useAccountStore()
    expect(await store.requestCode('  A@B.test ')).toBe(true)
    expect(store.step).toBe('code')
    expect(store.pendingEmail).toBe('A@B.test')
    expect(store.notice).toBe('On its way.')
  })

  it('rejects an obvious typo without calling the server', async () => {
    const calls = stubFetch([{ body: {} }])
    const store = useAccountStore()
    expect(await store.requestCode('not-an-email')).toBe(false)
    expect(calls).toHaveLength(0)
    expect(store.error).toBeTruthy()
    expect(store.step).toBe('email')
  })

  it('surfaces a rate limit and stays put', async () => {
    stubFetch([{ ok: false, body: { code: 'rate_limited', message: 'Too many requests.' } }])
    const store = useAccountStore()
    expect(await store.requestCode('a@b.test')).toBe(false)
    expect(store.error).toBe('Too many requests.')
    expect(store.step).toBe('email')
  })

  it('carries a development code through so staging sign-in needs no inbox', async () => {
    stubFetch([{ body: { status: 'sent', message: 'Sent.', dev_code: '048221' } }])
    const store = useAccountStore()
    await store.requestCode('a@b.test')
    expect(store.devCode).toBe('048221')
  })

  // The production shape: the field is absent, and the UI must render nothing
  // rather than "your code is undefined".
  it('leaves the development code empty when the server sends none', async () => {
    stubFetch([{ body: { status: 'sent', message: 'Sent.' } }])
    const store = useAccountStore()
    await store.requestCode('a@b.test')
    expect(store.devCode).toBe('')
  })
})

describe('verify', () => {
  it('signs in', async () => {
    stubFetch([
      { body: { status: 'sent', message: 'Sent.' } },
      { body: { available: true, signed_in: true, email: 'a@b.test' } },
    ])
    const store = useAccountStore()
    await store.requestCode('a@b.test')
    expect(await store.verify('048221')).toBe(true)
    expect(store.signedIn).toBe(true)
    expect(store.email).toBe('a@b.test')
    expect(store.step).toBe('done')
  })

  it('submits the address the code was sent to, not whatever is typed later', async () => {
    const calls = stubFetch([
      { body: { status: 'sent', message: 'Sent.' } },
      { body: { available: true, signed_in: true, email: 'a@b.test' } },
    ])
    const store = useAccountStore()
    await store.requestCode('a@b.test')
    await store.verify(' 048221 ')
    expect(JSON.parse(calls[1].init!.body as string)).toEqual({ email: 'a@b.test', code: '048221' })
  })

  it('holds on the code step after a bad code so the player can retype', async () => {
    stubFetch([
      { body: { status: 'sent', message: 'Sent.' } },
      { ok: false, body: { code: 'bad_code', message: 'That code is wrong or has expired.' } },
    ])
    const store = useAccountStore()
    await store.requestCode('a@b.test')
    expect(await store.verify('000000')).toBe(false)
    expect(store.step).toBe('code')
    expect(store.signedIn).toBe(false)
    expect(store.error).toBe('That code is wrong or has expired.')
  })
})

describe('signOut', () => {
  it('clears the address and returns to the start', async () => {
    stubFetch([
      { body: { status: 'sent', message: 'Sent.' } },
      { body: { available: true, signed_in: true, email: 'a@b.test' } },
      { body: {} },
    ])
    const store = useAccountStore()
    await store.requestCode('a@b.test')
    await store.verify('048221')
    await store.signOut()
    expect(store.signedIn).toBe(false)
    expect(store.email).toBe('')
    expect(store.step).toBe('email')
  })
})

describe('changeEmail', () => {
  it('goes back a step for a mistyped address', async () => {
    stubFetch([{ body: { status: 'sent', message: 'Sent.', dev_code: '1' } }])
    const store = useAccountStore()
    await store.requestCode('a@b.test')
    store.changeEmail()
    expect(store.step).toBe('email')
    expect(store.devCode).toBe('')
  })
})

// The dead end this recovers from: a browser whose session cookie is gone but
// whose player row still belongs to the previous account. The app shows the
// sign-in form, the server refuses with "sign out first", and the sign-out
// button is not rendered because the app believes it is signed out.
describe('signing in when the browser still belongs to someone else', () => {
  it('signs the stale account out and replays the same code', async () => {
    const calls = stubFetch([
      { ok: false, body: { code: 'already_signed_in', message: 'This browser is already signed in as old@b.test. Sign out first.' } },
      { body: {} },
      { body: { available: true, signed_in: true, email: 'new@b.test' } },
    ])
    const store = useAccountStore()
    store.pendingEmail = 'new@b.test'

    expect(await store.verify('123456')).toBe(true)

    expect(store.email).toBe('new@b.test')
    expect(store.step).toBe('done')
    expect(store.error).toBe('')
    // Sign out, then the same six digits again -- never a fresh code request,
    // which would spend the per-address rate limit recovering from our own
    // dead end.
    expect(calls.map(c => `${c.init?.method} ${c.path}`)).toEqual([
      'POST /api/auth/session',
      'DELETE /api/auth/session',
      'POST /api/auth/session',
    ])
    expect(JSON.parse(String(calls[2].init?.body))).toEqual({ email: 'new@b.test', code: '123456' })
  })

  it('reports the failure if the retry also fails', async () => {
    stubFetch([
      { ok: false, body: { code: 'already_signed_in', message: 'Sign out first.' } },
      { body: {} },
      { ok: false, body: { code: 'invalid_code', message: 'That code is wrong.' } },
    ])
    const store = useAccountStore()
    store.pendingEmail = 'new@b.test'

    expect(await store.verify('123456')).toBe(false)
    expect(store.error).toBe('That code is wrong.')
    expect(store.signedIn).toBe(false)
  })

  it('leaves every other failure alone', async () => {
    const calls = stubFetch([{ ok: false, body: { code: 'invalid_code', message: 'That code is wrong.' } }])
    const store = useAccountStore()
    store.pendingEmail = 'new@b.test'

    expect(await store.verify('123456')).toBe(false)
    expect(store.error).toBe('That code is wrong.')
    // One attempt, and no sign-out: a mistyped code must not shed the browser's
    // identity.
    expect(calls).toHaveLength(1)
  })
})
