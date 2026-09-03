import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { api } from '../api'
import type { APIError, History, HistoryDay, HistoryTopic } from '../types'

// Recent form is the last five days played, not the last five calendar days: a
// week away from the game should not read as a slump.
export const RECENT_DAYS = 5

// How many categories each of the two lists shows.
export const RANKED_TOPICS = 3

// A category with its two derived rates. accuracy is what "best category"
// means to a player; points per question is the tie-breaker, because it knows
// the difference between typing an answer and picking it off the list.
export interface RankedTopic extends HistoryTopic {
  accuracy:number
  pointsPerQuestion:number
}

function average(values:number[]):number|null {
  if (!values.length) return null
  return values.reduce((total, value) => total + value, 0) / values.length
}

export const useHistoryStore = defineStore('history', () => {
  const days = ref<HistoryDay[]>([])
  const topics = ref<HistoryTopic[]>([])
  const loading = ref(false)
  // loaded stays false until a response lands, so the page can tell "no days
  // yet" from "not asked yet" and avoid flashing an empty state.
  const loaded = ref(false)
  const error = ref('')

  async function load() {
    loading.value = true
    error.value = ''
    try {
      const history = await api<History>('/api/history')
      days.value = history.days ?? []
      topics.value = history.topics ?? []
      loaded.value = true
    } catch (e:unknown) {
      error.value = (e as APIError)?.message || 'Could not load your history.'
    } finally {
      loading.value = false
    }
  }

  // The server sends days oldest first; a history is read from today backwards.
  const recentFirst = computed(() => [...days.value].reverse())
  const daysPlayed = computed(() => days.value.length)
  const averagePoints = computed(() => average(days.value.map(d => d.points)))
  const recentPoints = computed(() => recentFirst.value.slice(0, RECENT_DAYS))
  const recentAverage = computed(() => average(recentPoints.value.map(d => d.points)))
  // Ties go to the more recent day: it is the one a player remembers, and
  // recentFirst already has them in that order.
  const best = computed<HistoryDay|undefined>(() =>
    recentFirst.value.reduce<HistoryDay|undefined>((top, day) => !top || day.points > top.points ? day : top, undefined))

  const ranked = computed<RankedTopic[]>(() => topics.value
    .filter(topic => topic.asked > 0)
    .map(topic => ({...topic, accuracy: topic.correct / topic.asked, pointsPerQuestion: topic.points / topic.asked}))
    .sort((a, b) =>
      b.accuracy - a.accuracy ||
      b.pointsPerQuestion - a.pointsPerQuestion ||
      b.asked - a.asked ||
      a.name.localeCompare(b.name)))

  const strongest = computed(() => ranked.value.slice(0, RANKED_TOPICS))
  // With three categories or fewer the weakest list is just the strongest list
  // backwards, which tells a player nothing. Empty means "don't show it".
  const weakest = computed(() => ranked.value.length > RANKED_TOPICS
    ? ranked.value.slice(-RANKED_TOPICS).reverse()
    : [])

  return {
    days, topics, loading, loaded, error, load,
    recentFirst, daysPlayed, averagePoints, recentPoints, recentAverage, best,
    ranked, strongest, weakest,
  }
})
