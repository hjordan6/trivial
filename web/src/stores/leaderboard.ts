import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { api } from '../api'
import type { AllTime, APIError, FriendToday, FriendsToday, Outcome } from '../types'

export const useLeaderboardStore = defineStore('leaderboard', () => {
  const date = ref('')
  const you = ref<FriendToday>()
  const friends = ref<FriendToday[]>([])
  const loading = ref(true)
  // Separated from `error` because they are different pages: one is a broken
  // request to retry, the other is the ordinary state of somebody who has not
  // added anybody yet and needs the invite link rather than an error box.
  const signedOut = ref(false)
  const error = ref('')
  // The friend whose board is open beside yours, by user id. null is the list.
  const openFriend = ref<number | null>(null)

  // Which of the two boards is showing. Today is the default because it is the
  // one that changes daily; all-time is the same numbers whenever you look.
  const tab = ref<'today' | 'all-time'>('today')
  const allTime = ref<AllTime>()
  const allTimeLoading = ref(false)
  const allTimeError = ref('')

  const hasFriends = computed(() => friends.value.length > 0)
  const selected = computed(() => friends.value.find(f => f.user_id === openFriend.value))

  // Outcomes by question id, which is how the side-by-side looks a person up
  // per cell. A question a person never resolved is absent rather than present
  // and empty, so the grid renders it the same way the results screen renders
  // an unanswered cell.
  function outcomes(person?: FriendToday) {
    return new Map<number, Outcome>((person?.answers ?? []).map(a => [a.question_id, a.outcome]))
  }

  async function load() {
    loading.value = true
    error.value = ''
    signedOut.value = false
    try {
      const got = await api<FriendsToday>('/api/friends/today')
      date.value = got.date
      you.value = got.you
      friends.value = got.friends
    } catch (e) {
      // Not signed in is the expected answer for a browser that has never had
      // an account, not a failure worth reporting as one.
      if ((e as APIError)?.code === 'not_signed_in') signedOut.value = true
      else error.value = (e as APIError)?.message || 'Could not load today’s leaderboard.'
    } finally {
      loading.value = false
    }
  }

  function open(userID: number) { openFriend.value = userID }
  function close() { openFriend.value = null }

  // Fetched on the first visit to the tab rather than up front: it walks every
  // finished run in the database, and most people open this page to see today.
  async function loadAllTime() {
    if (allTime.value || allTimeLoading.value) return
    allTimeLoading.value = true
    allTimeError.value = ''
    try {
      allTime.value = await api<AllTime>('/api/friends/all-time')
    } catch (e) {
      allTimeError.value = (e as APIError)?.message || 'Could not load the all-time board.'
    } finally {
      allTimeLoading.value = false
    }
  }

  function showTab(which: 'today' | 'all-time') {
    tab.value = which
    // Leaving a comparison open behind the tab would have it reappear on the
    // way back, over a board the viewer has since stopped looking at.
    openFriend.value = null
    if (which === 'all-time') loadAllTime()
  }

  return {
    date, you, friends, loading, error, signedOut, openFriend,
    tab, allTime, allTimeLoading, allTimeError,
    hasFriends, selected, outcomes, load, open, close, loadAllTime, showTab,
  }
})
