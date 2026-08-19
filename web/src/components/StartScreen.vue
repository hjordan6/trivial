<script setup lang="ts">
import type { Puzzle } from '../types'
defineProps<{puzzle:Puzzle}>();defineEmits<{start:[]}>()
</script>
<template><main class="start"><p class="eyebrow">Daily trivia · {{puzzle.date}}</p><h1>Nine questions.<br><em>One honest clock.</em></h1><p class="lede">Type the answer for a star, or reveal six choices and play for a circle. You have {{puzzle.time_limit_seconds}} seconds once you begin.</p><div class="topics"><span v-for="q in puzzle.questions" :key="q.topic_slug">{{q.topic_name}}</span></div><button class="primary" @click="$emit('start')">Start today’s game</button><p class="fine">The timer keeps running if you close this tab.</p></main></template>
