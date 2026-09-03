<script setup lang="ts">
import { ref, watch } from 'vue'
import { useAccountStore } from '../stores/account'
import { useFriendsStore } from '../stores/friends'
import SignIn from './SignIn.vue'

const account = useAccountStore()
const friends = useFriendsStore()
const open = ref(false)
const name = ref('')
const shared = ref(false)

// Opening the panel mints the link, so the Share button has a URL in hand
// before it is pressed. That is not an optimisation: navigator.share() needs
// transient user activation, and an awaited fetch inside the click handler
// consumes it in Safari, so the share sheet silently never opens.
async function begin() {
  open.value = true
  if (!account.signedIn) return
  name.value = friends.defaultNickname(account.email)
  if (!friends.invite) await friends.mint(name.value)
}

// A player who signs in from inside this panel continues into it, rather than
// being dropped back where they started.
watch(() => account.signedIn, async (isIn) => {
  if (isIn && open.value) {
    name.value = friends.defaultNickname(account.email)
    if (!friends.invite) await friends.mint(name.value)
  }
})

// share() calls navigator.share FIRST and synchronously, then saves the name.
// The token is stable and does not depend on the nickname, so the link is
// correct either way; a save that fails only means it carries the previous name.
function share() {
  const url = friends.shareUrl
  if (!url) return
  void friends.saveNickname(name.value)
  if (navigator.share) {
    void navigator.share({ url, text: `${name.value} wants to be your friend on Trivial` })
  } else {
    void navigator.clipboard.writeText(url)
  }
  shared.value = true
}
</script>

<template>
  <!-- Nothing at all when this server has no sign-in: a friendship needs two
       accounts, and there are none without a mailer. -->
  <section v-if="account.available" class="friends">
    <button v-if="!open" class="primary primary--small" type="button" @click="begin">
      Send friend request
    </button>

    <template v-else>
      <template v-if="!account.signedIn">
        <p class="friends__lede">
          Friends attach to an email rather than to this browser, so they follow
          you to any device. Sign in and your link is ready.
        </p>
        <SignIn />
      </template>

      <template v-else>
        <p class="friends__lede">
          Pick the name your friend will see, then send them the link.
        </p>
        <label class="field">
          <span>Your name</span>
          <input v-model="name" type="text" maxlength="40" placeholder="Sam">
        </label>
        <button
          class="primary primary--small" type="button"
          :disabled="friends.loading || !friends.invite || !name.trim()"
          @click="share">
          {{ shared ? 'Link sent!' : 'Share friend link' }}
        </button>
        <p v-if="friends.invite" class="fine friends__url">{{ friends.shareUrl }}</p>
      </template>
    </template>

    <p v-if="friends.error" class="form-error">{{ friends.error }}</p>
  </section>
</template>
