<script setup lang="ts">
import { useAdminStore } from '../stores/admin'

const store=useAdminStore()

// The board needs three topics, so the last three cannot be switched off. The
// server refuses it too; disabling the control just explains why up front.
function canDeactivate(){ return store.topics.filter(t=>t.active).length>3 }
</script>

<template>
  <section class="admin-section">
    <h2 class="eyebrow">Topics</h2>
    <p class="fine">Weight steers the automatic picks: a topic at 3 is drawn three times as often as one at 1. Pinned dates ignore both of these.</p>
    <div class="admin-topics">
      <div v-for="topic in store.topics" :key="topic.slug" class="admin-topic" :class="{'admin-topic--off':!topic.active}">
        <b>{{topic.name}}</b>
        <label class="field field--inline">
          <span>Weight</span>
          <input
            type="number" min="1" max="1000" :value="topic.selection_weight"
            @change="store.updateTopic(topic.slug, {selection_weight: Number(($event.target as HTMLInputElement).value)})">
        </label>
        <label class="field field--check">
          <input
            type="checkbox" :checked="topic.active"
            :disabled="topic.active && !canDeactivate()"
            @change="store.updateTopic(topic.slug, {active: ($event.target as HTMLInputElement).checked})">
          <span>Active</span>
        </label>
      </div>
    </div>
  </section>
</template>
