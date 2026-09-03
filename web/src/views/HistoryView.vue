<script setup lang="ts">
import { onBeforeMount } from 'vue'
import { RECENT_DAYS, useHistoryStore } from '../stores/history'
import { useAccountStore } from '../stores/account'
import { MAX_POINTS, topicEmoji } from '../stores/run'
import SignIn from '../components/SignIn.vue'

const history=useHistoryStore()
const account=useAccountStore()

onBeforeMount(()=>{history.load();account.probe()})

// A puzzle date is a calendar date, not an instant. `new Date('2026-08-31')`
// would read it as UTC midnight and render the day before in any western
// timezone, so the parts are handed to the local constructor instead.
const formatter=new Intl.DateTimeFormat(undefined,{weekday:'short',month:'short',day:'numeric'})
function formatDay(iso:string):string {
  const [year,month,day]=iso.split('-').map(Number)
  return formatter.format(new Date(year,month-1,day))
}
function formatYear(iso:string):string { return iso.slice(0,4) }
// Averages get one decimal; a whole number keeps none, so 30 does not read as
// a suspiciously precise 30.0.
function round(value:number|null):string {
  if(value===null) return '—'
  return Number.isInteger(value)?String(value):value.toFixed(1)
}
function percent(rate:number):string { return `${Math.round(rate*100)}%` }
</script>

<template>
  <main class="history">
    <p class="eyebrow">Your record</p>
    <h1>History</h1>

    <p v-if="history.loading&&!history.loaded" class="history-note">Adding up your days…</p>
    <p v-else-if="history.error" class="history-note">{{history.error}}</p>
    <template v-else-if="history.daysPlayed">
      <section class="history-figures">
        <div>
          <b>{{round(history.averagePoints)}}</b>
          <span>Average points</span>
        </div>
        <div class="history-figures__best">
          <b>{{history.best?.points}}</b>
          <span>Best day<template v-if="history.best"> · {{formatDay(history.best.date)}}</template></span>
        </div>
        <div>
          <b>{{round(history.recentAverage)}}</b>
          <span>Last {{Math.min(RECENT_DAYS,history.daysPlayed)}} average</span>
        </div>
        <div>
          <b>{{history.daysPlayed}}</b>
          <span>Days played</span>
        </div>
      </section>

      <div v-if="history.ranked.length" class="history-topics">
        <section>
          <p class="eyebrow">Strongest categories</p>
          <ol class="topic-list">
            <li v-for="topic in history.strongest" :key="topic.slug">
              <b>{{topicEmoji(topic.slug,topic.name)}}</b>
              <span class="topic-list__name">{{topic.name}}</span>
              <span class="topic-list__rate">{{percent(topic.accuracy)}}</span>
              <small>{{topic.correct}}/{{topic.asked}}</small>
            </li>
          </ol>
        </section>
        <!-- Hidden until there are more than three categories on record, where
             the weakest list would only be the strongest one backwards. -->
        <section v-if="history.weakest.length">
          <p class="eyebrow">Toughest categories</p>
          <ol class="topic-list topic-list--weak">
            <li v-for="topic in history.weakest" :key="topic.slug">
              <b>{{topicEmoji(topic.slug,topic.name)}}</b>
              <span class="topic-list__name">{{topic.name}}</span>
              <span class="topic-list__rate">{{percent(topic.accuracy)}}</span>
              <small>{{topic.correct}}/{{topic.asked}}</small>
            </li>
          </ol>
        </section>
      </div>

      <section class="history-days">
        <p class="eyebrow">Every day · newest first</p>
        <ol class="day-list">
          <li v-for="day in history.recentFirst" :key="day.date">
            <div class="day-list__date">
              <b>{{formatDay(day.date)}}</b>
              <small>{{formatYear(day.date)}}</small>
            </div>
            <div class="day-list__bar" aria-hidden="true">
              <span :style="{width:`${Math.round(day.points/MAX_POINTS*100)}%`}" />
            </div>
            <div class="day-list__score">
              <b>{{day.points}}</b>
              <small>{{day.correct}}/9 · {{day.typed}}⭐</small>
            </div>
          </li>
        </ol>
      </section>
    </template>
    <p v-else class="history-note">
      No finished days yet. Play today’s board and it will show up here.
    </p>

    <RouterLink class="history-back" to="/">← Back to today</RouterLink>
    <!-- A history belongs to the account, not the browser: without one, this
         page only knows the days played in this browser. -->
    <SignIn v-if="!account.signedIn" />
  </main>
</template>
