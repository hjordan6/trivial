<script setup lang="ts">
import { onBeforeMount } from 'vue'
import { useAdminStore } from '../stores/admin'
import AdminLogin from '../components/AdminLogin.vue'
import AdminUpcoming from '../components/AdminUpcoming.vue'
import AdminTopics from '../components/AdminTopics.vue'
import AdminQuestions from '../components/AdminQuestions.vue'
import AdminImport from '../components/AdminImport.vue'
import AdminExport from '../components/AdminExport.vue'

const store=useAdminStore()

onBeforeMount(async () => {
  await store.probe()
  if (store.authed) await store.refresh()
})
</script>

<template>
  <div v-if="store.authed === null" class="loading">Checking…</div>
  <AdminLogin v-else-if="!store.authed" />
  <main v-else class="admin">
    <header class="admin__head">
      <p class="eyebrow">Trivial · admin</p>
      <div class="admin__head-actions">
        <a href="/">← Back to the game</a>
        <button class="give-up" @click="store.logout">Sign out</button>
      </div>
    </header>

    <p v-if="store.error" class="admin-error">{{store.error}}</p>
    <p v-if="store.notice" class="admin-notice">{{store.notice}}</p>

    <AdminUpcoming />
    <AdminTopics />
    <AdminQuestions />
    <AdminExport />
    <AdminImport />
  </main>
</template>
