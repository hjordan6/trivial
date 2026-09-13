import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '../api'
import type { APIError, AuthSession, CodeRequested } from '../types'

// A shape check only, to save a round trip on an obvious typo. The server
// validates independently and is the authority; this never widens what is
// accepted.
const LOOKS_LIKE_EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export type Step = 'email' | 'code' | 'done'

export const useAccountStore = defineStore('account', () => {
  // null means "not asked yet", the same convention as admin.authed. The
  // session cookie is HttpOnly, so only the server can answer.
  const available = ref<boolean | null>(null)
  const email = ref('')
  const step = ref<Step>('email')
  // pendingEmail is the address a code was sent to, which is what the code step
  // shows and submits. It is deliberately not `email`: that one means "the
  // account you are signed in as".
  const pendingEmail = ref('')
  // The six digits themselves, when the server chose to disclose them. Only a
  // development server with this address in DEV_CODE_EMAILS ever does.
  const devCode = ref('')
  const loading = ref(false)
  const error = ref('')
  const notice = ref('')

  const signedIn = computed(() => !!email.value)

  function fail(e: unknown) {
    error.value = (e as APIError)?.message || 'Something went wrong.'
  }
  function codeOf(e: unknown) {
    return (e as APIError)?.code ?? ''
  }
  function reset() {
    error.value = ''
    notice.value = ''
  }

  async function probe() {
    try {
      const session = await api<AuthSession>('/api/auth/session')
      available.value = session.available
      email.value = session.email ?? ''
      step.value = session.signed_in ? 'done' : 'email'
    } catch {
      // A probe that cannot be answered is treated as "no sign-in here", so a
      // failure hides the prompt rather than showing one that cannot work.
      available.value = false
    }
  }

  async function requestCode(address: string) {
    reset()
    const trimmed = address.trim()
    if (!LOOKS_LIKE_EMAIL.test(trimmed)) {
      error.value = 'That does not look like an email address.'
      return false
    }
    loading.value = true
    try {
      const sent = await api<CodeRequested>('/api/auth/code', {
        method: 'POST',
        body: JSON.stringify({ email: trimmed }),
      })
      pendingEmail.value = trimmed
      devCode.value = sent.dev_code ?? ''
      step.value = 'code'
      notice.value = sent.message
      return true
    } catch (e) {
      fail(e)
      return false
    } finally {
      loading.value = false
    }
  }

  // submitCode is the request on its own, so the recovery in verify() can make
  // it twice without repeating what a success means.
  async function submitCode(code: string) {
    const session = await api<AuthSession>('/api/auth/session', {
      method: 'POST',
      body: JSON.stringify({ email: pendingEmail.value, code }),
    })
    email.value = session.email ?? ''
    step.value = 'done'
    devCode.value = ''
    return true
  }

  async function verify(rawCode: string) {
    reset()
    loading.value = true
    const code = rawCode.trim()
    try {
      return await submitCode(code)
    } catch (e) {
      // already_signed_in is a dead end unless it is handled here.
      //
      // A browser that has lost its session cookie -- it expired, or a sign-out
      // cleared it back when sign-out left the player cookie in place -- still
      // has a player row attached to the old account. The probe reports signed
      // out, so the app shows this form, and the server refuses the sign-in
      // with "sign out first". The button it means is rendered only when the
      // app thinks you are signed in, so it is not on the screen. The player is
      // told to press something that does not exist.
      //
      // Signing out here is precisely what the message asks for, and it is
      // safe: it sheds the stale player without detaching it, so the previous
      // account keeps every run it played on this browser.
      //
      // The code is replayed rather than re-requested because the server checks
      // the browser before it consumes the code, so the one just typed is still
      // live. Asking for another would spend the per-address rate limit on
      // recovering from our own dead end.
      if (codeOf(e) === 'already_signed_in') {
        try {
          await api<void>('/api/auth/session', { method: 'DELETE' })
          return await submitCode(code)
        } catch (retry) {
          fail(retry)
          return false
        }
      }
      // Stay on the code step so the player can retype rather than starting the
      // whole flow again.
      fail(e)
      return false
    } finally {
      loading.value = false
    }
  }

  // changeEmail goes back a step, for a mistyped address.
  function changeEmail() {
    reset()
    step.value = 'email'
    devCode.value = ''
  }

  async function signOut() {
    reset()
    loading.value = true
    try {
      await api<void>('/api/auth/session', { method: 'DELETE' })
      email.value = ''
      pendingEmail.value = ''
      step.value = 'email'
      return true
    } catch (e) {
      fail(e)
      return false
    } finally {
      loading.value = false
    }
  }

  return {
    available, email, step, pendingEmail, devCode, loading, error, notice,
    signedIn, probe, requestCode, verify, changeEmail, signOut,
  }
})
