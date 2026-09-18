<script setup lang="ts">
import { onMounted, ref } from 'vue'
import SignIn from './SignIn.vue'

// The one moment the game asks for an email: the last answer is in, the score
// is computed, and the player wants to see it. That is the only point in the
// day when an account is worth something concrete to them -- everywhere else
// the ask competes with the game itself and gets dismissed.
//
// It is a gate, not a wall. "Continue as guest" is always visible, never
// delayed, and never styled to be hunted for; the parent remembers the choice
// for the rest of the day so a refresh does not put this back in front of the
// same player.
const emit = defineEmits<{guest:[]}>()

const dialog = ref<HTMLDialogElement>()
// showModal rather than an `open` attribute, because only the modal form gives
// us the focus trap, the inert background and the backdrop for free.
onMounted(() => dialog.value?.showModal())
</script>

<template>
  <!-- Escape resolves to the same thing the button does. Cancelling is
       prevented because the parent unmounts this component in response to the
       event; letting the browser also close the dialog would race that, and a
       closed-but-mounted dialog is invisible with no way back. -->
  <dialog ref="dialog" class="gate" aria-labelledby="gate-title" @cancel.prevent="emit('guest')">
    <div class="gate__card">
      <p class="eyebrow gate__eyebrow">Your score is ready</p>
      <h2 id="gate-title" class="gate__title">Keep this one?</h2>
      <p class="gate__lede">
        Nine questions, done. Add an email and today’s score — and every streak
        after it — follows you to any device, and survives this browser
        forgetting who you are.
      </p>
      <ul class="gate__points">
        <li>Your streak lives on your account, not in this browser</li>
        <li>Play from your phone and your laptop as the same person</li>
        <li>No password — a six-digit code, once</li>
      </ul>

      <SignIn gate />

      <!-- Below the form, where it reads as the way past rather than the
           alternative on offer, but full width and in plain language: a guest
           escape hatch that has to be deciphered is a dark pattern. -->
      <button class="gate__guest" type="button" @click="emit('guest')">
        Continue as guest →
      </button>
      <p class="fine gate__fine">Either way, your result is on the next screen.</p>
    </div>
  </dialog>
</template>
