<script setup lang="ts">
import { computed } from 'vue'
import type { Puzzle } from '../types'
import { DIFFICULTY_POINTS, FREE_TEXT_BONUS, MAX_POINTS, useRunStore } from '../stores/run'
import SignIn from './SignIn.vue'
import SendFriendRequest from './SendFriendRequest.vue'

const props=defineProps<{puzzle:Puzzle}>()
defineEmits<{start:[]}>()

const store=useRunStore()
// One pill per topic. Before the clock starts the server usually sends just the
// three topics, but a returning player gets all nine questions back, which
// would otherwise render each topic three times.
const topics=computed(()=>[...new Map(props.puzzle.questions.map(q=>[q.topic_slug,q])).values()])
// The limit is configurable, so say "4 minutes" when it divides evenly and fall
// back to seconds when it doesn't.
const timeLimit=computed(()=>{
  const seconds=props.puzzle.time_limit_seconds
  if(seconds%60!==0) return `${seconds} seconds`
  const minutes=seconds/60
  return `${minutes} minute${minutes===1?'':'s'}`
})
const tiers=[
  {difficulty:'easy' as const,label:'Easy'},
  {difficulty:'medium' as const,label:'Medium'},
  {difficulty:'hard' as const,label:'Hard'},
]
const legend=[
  {outcome:'star' as const,title:'Typed it',detail:`Right on your own. Base points plus ${FREE_TEXT_BONUS}.`},
  {outcome:'circle' as const,title:'Picked it',detail:'Right off the list. Base points only.'},
  {outcome:'miss' as const,title:'Missed it',detail:'Wrong choice. No points.'},
  {outcome:'expired' as const,title:'Ran out',detail:'The clock beat you to it. No points.'},
]
</script>

<template>
  <main class="start">
    <p class="eyebrow">Daily trivia · {{puzzle.date}}</p>
    <h1>Trivial</h1>
    <p class="lede">Nine questions: three topics, each asked easy, medium and hard.</p>

    <!-- The four things a first-timer has to know, loud and above the fold. The
         finer print lives in the collapsed section underneath. -->
    <section class="howto">
      <h2>How to play</h2>
      <ol class="rules">
        <li>
          <div>
            <b>You have {{timeLimit}} for all nine questions.</b>
            <p>One clock for the whole board. It starts the moment you press start, and there is no pausing it.</p>
          </div>
        </li>
        <li>
          <div>
            <b>Type your answer, or show the choices.</b>
            <p>Every question gives you one typed attempt. Get it wrong and the multiple choices appear anyway, so a guess costs you nothing but time. Spelling is forgiving — close enough counts.</p>
          </div>
        </li>
        <li>
          <div>
            <b>Typing it yourself scores more.</b>
            <p>An answer you typed is worth <b class="pop">+{{FREE_TEXT_BONUS}} points</b> on top. Picking it off the list earns the base points only.</p>
          </div>
        </li>
        <li>
          <div>
            <b>Harder questions are worth more.</b>
            <p>Easy, medium and hard pay {{DIFFICULTY_POINTS.easy}}, {{DIFFICULTY_POINTS.medium}} and {{DIFFICULTY_POINTS.hard}} points. A flawless day — all nine typed — is {{MAX_POINTS}}.</p>
            <div class="points-table">
              <div v-for="tier in tiers" :key="tier.difficulty">
                <b>{{DIFFICULTY_POINTS[tier.difficulty]}}</b>
                <span>{{tier.label}}</span>
              </div>
              <div class="points-bonus">
                <b>+{{FREE_TEXT_BONUS}}</b>
                <span>Typed</span>
              </div>
            </div>
          </div>
        </li>
      </ol>
    </section>

    <p class="eyebrow">Today’s topics</p>
    <div class="topics"><span v-for="topic in topics" :key="topic.topic_slug">{{topic.topic_name}}</span></div>
    <button class="primary" @click="$emit('start')">Start today’s game</button>
    <p class="fine">The timer keeps running if you close this tab.</p>

    <details class="howto-more">
      <summary>What the shared squares mean</summary>
      <p>Your result shares as a grid: one row per topic, read left to right as easy, medium, hard. Everyone gets the same topics in the same order, so the grids line up.</p>
      <ul class="legend">
        <li v-for="item in legend" :key="item.outcome">
          <b>{{store.symbol(item.outcome)}}</b>
          <span><strong>{{item.title}}</strong> {{item.detail}}</span>
        </li>
      </ul>
    </details>

    <!-- Deliberately reachable before playing: someone whose browser lost its
         cookie needs to sign in first, or today attaches to a new browser and
         breaks the streak they came back for. -->
    <SignIn compact />

    <!-- Also here, not only on the results screen. A player who never finishes
         today's puzzle would otherwise have no route to the feature at all, and
         the invite link is the thing that brings the second player in. -->
    <SendFriendRequest compact />
  </main>
</template>
