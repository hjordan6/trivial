<script setup lang="ts">
import { computed } from 'vue'
import type { Puzzle } from '../types'
import { DIFFICULTY_POINTS, FREE_TEXT_BONUS, MAX_POINTS, useRunStore } from '../stores/run'

const props=defineProps<{puzzle:Puzzle}>()
defineEmits<{start:[]}>()

const store=useRunStore()
// One pill per topic. Before the clock starts the server usually sends just the
// three topics, but a returning player gets all nine questions back, which
// would otherwise render each topic three times.
const topics=computed(()=>[...new Map(props.puzzle.questions.map(q=>[q.topic_slug,q])).values()])
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
    <h1>Nine questions.<br><em>One honest clock.</em></h1>
    <p class="lede">Three topics, each asked easy, medium and hard. Type the answer for the bonus, or reveal the choices and play it safe. You have {{puzzle.time_limit_seconds}} seconds once you begin.</p>

    <section class="howto">
      <h2>How to play</h2>

      <div class="howto-step">
        <b>Type your answer first.</b>
        <p>You get one free-text attempt per question, and it’s worth the most. Spelling is forgiving — close enough counts.</p>
      </div>

      <div class="howto-step">
        <b>Miss it and you still get a second shot.</b>
        <p>A wrong typed answer doesn’t end the question. The choices appear and you can pick from them for the base points. Jumping straight to <em>Show choices</em> works too, but gives up the bonus.</p>
      </div>

      <div class="howto-step">
        <b>Harder questions pay more.</b>
        <p>Add {{FREE_TEXT_BONUS}} points to any answer you typed yourself. A flawless day is {{MAX_POINTS}}.</p>
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

      <div class="howto-step">
        <b>What the squares mean.</b>
        <p>Your result shares as a grid: one row per topic, read left to right as easy, medium, hard. Everyone gets the same topics in the same order, so the grids line up.</p>
        <ul class="legend">
          <li v-for="item in legend" :key="item.outcome">
            <b>{{store.symbol(item.outcome)}}</b>
            <span><strong>{{item.title}}</strong> {{item.detail}}</span>
          </li>
        </ul>
      </div>
    </section>

    <p class="eyebrow">Today’s topics</p>
    <div class="topics"><span v-for="topic in topics" :key="topic.topic_slug">{{topic.topic_name}}</span></div>
    <button class="primary" @click="$emit('start')">Start today’s game</button>
    <p class="fine">The timer keeps running if you close this tab.</p>
  </main>
</template>
