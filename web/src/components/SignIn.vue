<script setup lang="ts">
import { ref } from 'vue'
import { useAccountStore } from '../stores/account'
import { useRunStore } from '../stores/run'

// compact renders the collapsed, one-line form used on the start screen, where
// signing in is a recovery path rather than the thing being offered.
const props = defineProps<{compact?:boolean}>()

const account = useAccountStore()
const run = useRunStore()
const address = ref('')
const code = ref('')
// On the start screen the form stays folded away until asked for; on the
// results screen it is the point of the section, so it is open already.
const open = ref(!props.compact)

async function submitEmail() {
  if (await account.requestCode(address.value)) code.value = ''
}
async function submitCode() {
  // Reloading stats here rather than inside the account store keeps the two
  // stores independent: the account store knows nothing about runs.
  if (await account.verify(code.value)) await run.loadStats()
}
async function signOut() {
  if (!confirm('Sign out? Your scores stay saved to your email.')) return
  await account.signOut()
  await run.loadStats()
}
</script>

<template>
  <!-- Nothing at all when this server has no sign-in configured. -->
  <section v-if="account.available" class="account" :class="{'account--compact':compact}">
    <template v-if="account.signedIn">
      <p class="account__status">
        Saving your scores to <b>{{account.email}}</b>.
        <button class="give-up account__signout" type="button" @click="signOut">Sign out</button>
      </p>
    </template>

    <template v-else-if="!open">
      <p class="fine account__invite">
        Have an account, or want one?
        <button class="account__link" type="button" @click="open = true">Sign in or sign up</button>
      </p>
    </template>

    <template v-else>
      <template v-if="account.step === 'email'">
        <p v-if="!compact" class="eyebrow">Keep your streak</p>
        <p class="account__lede">
          Add an email and your scores follow you to any device — and come back if
          this browser forgets you. No password, and it stays optional.
        </p>
        <form @submit.prevent="submitEmail">
          <label class="field">
            <span>Email</span>
            <input v-model="address" type="email" inputmode="email" autocomplete="email" placeholder="you@example.com">
          </label>
          <button class="primary primary--small" type="submit" :disabled="account.loading || !address">
            {{account.loading ? 'Sending…' : 'Email me a code'}}
          </button>
        </form>
      </template>

      <template v-else-if="account.step === 'code'">
        <!-- The spam hint is temporary. The sending domain is new, so Gmail
             still files some codes as spam while its reputation builds; this
             line comes out once delivery settles. -->
        <p class="account__lede">
          We sent a six-digit code to <b>{{account.pendingEmail}}</b>. It works once.
          If it is not in your inbox, check your spam folder.
        </p>
        <form @submit.prevent="submitCode">
          <label class="field account__code">
            <span>Code</span>
            <!-- one-time-code is what makes iOS and Android offer the code
                 straight from the notification, so the player never leaves this
                 tab. That is the whole reason this is a code and not a link. -->
            <input
              v-model="code" inputmode="numeric" autocomplete="one-time-code"
              maxlength="6" pattern="\d*" placeholder="000000" autofocus>
          </label>
          <button class="primary primary--small" type="submit" :disabled="account.loading || code.length < 6">
            {{account.loading ? 'Checking…' : 'Sign in'}}
          </button>
        </form>
        <p class="fine">
          <button class="account__link" type="button" @click="account.changeEmail()">Wrong address?</button>
          ·
          <button class="account__link" type="button" @click="account.requestCode(account.pendingEmail)">Send another</button>
        </p>
        <!-- Set only by a development server that lists this address in
             DEV_CODE_EMAILS, so staging can be signed into without an inbox. -->
        <p v-if="account.devCode" class="account__devcode">Development server — your code is {{account.devCode}}</p>
      </template>
    </template>

    <p v-if="account.error" class="form-error">{{account.error}}</p>
  </section>
</template>
