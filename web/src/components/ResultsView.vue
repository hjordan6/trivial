<script setup lang="ts">
import { ref } from 'vue'
import { MAX_POINTS, useRunStore } from '../stores/run'
import { useAccountStore } from '../stores/account'
import SignIn from './SignIn.vue'
import SendFriendRequest from './SendFriendRequest.vue'

const store=useRunStore()
const account=useAccountStore()
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
    <!-- Above the grid while there is no account, because that is the only
         place a phone will show it: the grid, the share button and the three
         stat numbers run past the fold, and an offer nobody scrolls to may as
         well not be on the page. Once signed in it drops back below the stats,
         where it is a status line rather than something being offered. -->
    <SignIn v-if="!account.signedIn" class="account--promoted" />
    <!-- Each row is one topic asked easy/medium/hard, so the row wrapper carries
         the topic name for narrow screens, where there is no room to repeat it in
         all three cells. On wide screens the wrapper is `display: contents` and
         the cells sit directly in the 3x3 grid, as before. -->
    <div class="result-grid">
      <div v-for="(row,position) in store.orderedRows()" :key="position" class="result-row">
        <h2 v-if="row.length" class="result-row__topic">{{row[0].topic_name}}</h2>
        <div v-for="q in row" :key="q.question_id" class="result-cell">
          <b>{{store.symbol(store.answerMap.get(q.question_id)?.outcome)}}</b>
          <small><span class="result-cell__topic">{{q.topic_name}}</span>{{q.difficulty}}</small>
          <span class="result-cell__answer">{{store.answerMap.get(q.question_id)?.canonical_answer}}</span>
        </div>
      </div>
    </div>
    <button class="primary" @click="share">{{copied?'Shared!':'Share result'}}</button>
    <SendFriendRequest />
    <button v-if="isLocal" class="dev-reset results-reset" @click="reset">↻ Play again locally</button>
    <!-- The way back out of today: the stats below are three numbers, the
         history page is the whole record. -->
    <RouterLink class="history-link" to="/history">See my history →</RouterLink>
    <section v-if="store.stats" class="stats">
      <div><b>{{store.stats.days_played}}</b><span>played</span></div>
      <div><b>{{store.stats.current_streak}}</b><span>current streak</span></div>
      <div><b>{{store.stats.longest_streak}}</b><span>best streak</span></div>
    </section>
    <!-- Directly under the three numbers, because they are the argument for
         signing in: these, on every device you play on. -->
    <SignIn v-if="account.signedIn" />
  </main>
</template>
