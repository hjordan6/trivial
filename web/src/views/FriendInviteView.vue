<script setup lang="ts">
import { onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useAccountStore } from '../stores/account'
import { useFriendsStore } from '../stores/friends'
import SignIn from '../components/SignIn.vue'

const route = useRoute()
const account = useAccountStore()
const friends = useFriendsStore()
const token = String(route.params.token ?? '')

// Both requests go out together: neither answer depends on the other, and the
// page cannot render a decisive state until it has both.
onMounted(async () => {
  await Promise.all([friends.load(token), account.probe()])
})

// Case B ends here. accounts.VerifyCode upserts the user, so "they already had
// an account" and "an account is created for them" are the same call -- this
// page just waits for the flag to flip and then offers Accept.
watch(() => account.signedIn, (isIn) => { if (isIn) friends.error = '' })
</script>

<template>
  <main class="invite">
    <template v-if="friends.status === 'loading'">
      <p class="invite__lede">Checking that link…</p>
    </template>

    <template v-else-if="friends.status === 'dead'">
      <p class="eyebrow">Friend request</p>
      <h1>This link doesn’t work any more.</h1>
      <p class="invite__lede">Ask for a new one, or just come and play.</p>
      <RouterLink class="primary" to="/">Play today’s puzzle</RouterLink>
    </template>

    <template v-else-if="friends.status === 'accepted'">
      <p class="eyebrow">Friend request</p>
      <h1>You and <span class="invite__name">{{ friends.senderName }}</span> are now friends.</h1>
      <RouterLink class="primary" to="/">Play today’s puzzle</RouterLink>
    </template>

    <template v-else-if="friends.status === 'self'">
      <p class="eyebrow">Friend request</p>
      <h1>This is your own friend link.</h1>
      <p class="invite__lede">Send it to someone else and they can add you.</p>
      <RouterLink class="primary" to="/">Play today’s puzzle</RouterLink>
    </template>

    <template v-else>
      <p class="eyebrow">Friend request</p>
      <h1><span class="invite__name">{{ friends.senderName }}</span> wants to be your friend.</h1>

      <template v-if="account.signedIn">
        <button class="primary" type="button" :disabled="friends.loading" @click="friends.accept(token)">
          {{ friends.loading ? 'Adding…' : 'Accept' }}
        </button>
      </template>
      <template v-else>
        <p class="invite__lede">
          Add your email to accept. It’s how a friendship follows you to any
          device — no password, and you can play without one.
        </p>
        <SignIn />
      </template>
    </template>

    <p v-if="friends.error" class="form-error">{{ friends.error }}</p>
  </main>
</template>
