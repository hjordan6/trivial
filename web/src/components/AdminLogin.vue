<script setup lang="ts">
import { ref } from 'vue'
import { useAdminStore } from '../stores/admin'

const store=useAdminStore()
const password=ref('')
</script>

<template>
  <main class="start admin-login">
    <p class="eyebrow">Trivial · admin</p>
    <h1>Sign in</h1>
    <form @submit.prevent="store.login(password)">
      <label class="field">
        <span>Admin password</span>
        <input v-model="password" type="password" autocomplete="current-password" autofocus>
      </label>
      <button class="primary" type="submit" :disabled="store.loading || !password">
        {{store.loading?'Checking…':'Sign in'}}
      </button>
    </form>
    <p v-if="store.error" class="admin-error">{{store.error}}</p>
    <p class="fine">
      A wrong password and a server with no admin password configured look the
      same from here.
    </p>
  </main>
</template>
