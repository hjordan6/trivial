<script setup lang="ts">
import { ref } from 'vue'
import { MAX_POINTS, useRunStore } from '../stores/run'

const store=useRunStore()
const copied=ref(false)
const isLocal=['localhost','127.0.0.1','::1'].includes(location.hostname)

// Share the plain site link. Challenge links (/c/{token}) aren't served yet, so
// pointing people at one would hand them a 404.
async function share(){
  const text=store.shareText(location.origin)
  if(navigator.share) await navigator.share({text})
  else await navigator.clipboard.writeText(text)
  copied.value=true
}
async function reset(){
  if(confirm('Reset today’s local run and play again?')) await store.resetForDevelopment()
}
</script>

<template>
  <main class="results">
    <p class="eyebrow">Today’s result</p>
    <h1>{{store.points}} <span>/ {{MAX_POINTS}} pts</span></h1>
    <p class="subscore">{{store.score}} of 9 correct · {{store.stars}}⭐ typed</p>
    <div class="result-grid">
      <template v-for="(row,position) in store.orderedRows()" :key="position">
        <div v-for="q in row" :key="q.question_id" class="result-cell">
          <b>{{store.symbol(store.answerMap.get(q.question_id)?.outcome)}}</b>
          <small>{{q.topic_name}} · {{q.difficulty}}</small>
          <span>{{store.answerMap.get(q.question_id)?.canonical_answer}}</span>
        </div>
      </template>
    </div>
    <button class="primary" @click="share">{{copied?'Shared!':'Share result'}}</button>
    <button v-if="isLocal" class="dev-reset results-reset" @click="reset">↻ Play again locally</button>
    <section v-if="store.stats" class="stats">
      <div><b>{{store.stats.days_played}}</b><span>played</span></div>
      <div><b>{{store.stats.current_streak}}</b><span>current streak</span></div>
      <div><b>{{store.stats.longest_streak}}</b><span>best streak</span></div>
    </section>
  </main>
</template>
