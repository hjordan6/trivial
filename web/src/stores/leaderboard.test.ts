import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useLeaderboardStore } from './leaderboard'
import type { FriendToday } from '../types'

const mocks = vi.hoisted(() => ({ api: vi.fn() }))
vi.mock('../api', () => ({ api: mocks.api }))

beforeEach(() => {
  setActivePinia(createPinia())
  mocks.api.mockReset()
})

function person(user_id:number, nickname:string, over:Partial<FriendToday> = {}):FriendToday {
  return {user_id, nickname, played:true, correct:0, typed:0, points:0, answers:[], you:false, ...over}
}

describe('leaderboard store', () => {
  it('keeps the server’s ranking rather than re-sorting it', async () => {
    // The server ranks; re-sorting here is how the two would drift apart on a
    // tiebreak. The store must hand back exactly what arrived.
    const friends = [person(2, 'ahead', {points:18}), person(3, 'behind', {points:12}), person(4, 'idle', {played:false})]
    mocks.api.mockResolvedValue({date:'2026-09-11', you:person(1, 'me', {points:14}), friends})
    const store = useLeaderboardStore()

    await store.load()

    expect(store.friends.map(f => f.nickname)).toEqual(['ahead', 'behind', 'idle'])
    expect(store.you?.nickname).toBe('me')
    expect(store.date).toBe('2026-09-11')
    expect(store.hasFriends).toBe(true)
  })

  it('maps a person’s outcomes by question id for the side-by-side', () => {
    const store = useLeaderboardStore()
    const friend = person(2, 'ahead', {answers:[
      {question_id:11, outcome:'star'},
      {question_id:12, outcome:'miss'},
    ]})

    const got = store.outcomes(friend)

    expect(got.get(11)).toBe('star')
    expect(got.get(12)).toBe('miss')
    // A question they never resolved is absent, so the grid falls back to the
    // unanswered square rather than rendering nothing.
    expect(got.get(13)).toBeUndefined()
  })

  it('treats a person with no answers, and no person at all, as an empty board', () => {
    const store = useLeaderboardStore()
    expect(store.outcomes(person(2, 'idle', {played:false})).size).toBe(0)
    expect(store.outcomes(undefined).size).toBe(0)
  })

  it('opens and closes one friend at a time', async () => {
    mocks.api.mockResolvedValue({date:'2026-09-11', you:person(1, 'me'), friends:[person(2, 'ahead'), person(3, 'behind')]})
    const store = useLeaderboardStore()
    await store.load()

    store.open(3)
    expect(store.selected?.nickname).toBe('behind')
    store.open(2)
    expect(store.selected?.nickname).toBe('ahead')
    store.close()
    expect(store.openFriend).toBeNull()
    expect(store.selected).toBeUndefined()
  })

  it('reports a signed-out viewer as a state, not an error', async () => {
    mocks.api.mockRejectedValue({code:'not_signed_in', message:'Sign in first.'})
    const store = useLeaderboardStore()

    await store.load()

    expect(store.signedOut).toBe(true)
    expect(store.error).toBe('')
    expect(store.loading).toBe(false)
  })

  it('reports any other failure as an error', async () => {
    mocks.api.mockRejectedValue({code:'boom', message:'Server fell over.'})
    const store = useLeaderboardStore()

    await store.load()

    expect(store.signedOut).toBe(false)
    expect(store.error).toBe('Server fell over.')
  })
})

describe('all-time tab', () => {
  const board = {
    minimum_days: 3, minimum_questions: 9, days_played: 4, qualified: true,
    friends: [], global: {ranked: true, rank: 2, of: 9, best_average: 24.5}, topics: [],
  }

  it('fetches nothing until the tab is opened, then only once', async () => {
    mocks.api.mockResolvedValue(board)
    const store = useLeaderboardStore()

    expect(store.tab).toBe('today')
    expect(mocks.api).not.toHaveBeenCalled()

    store.showTab('all-time')
    await store.loadAllTime()
    store.showTab('today')
    store.showTab('all-time')
    await store.loadAllTime()

    // Walking every finished run is worth doing once a visit, not once a tap.
    expect(mocks.api).toHaveBeenCalledTimes(1)
    expect(mocks.api).toHaveBeenCalledWith('/api/friends/all-time')
    expect(store.allTime?.global.rank).toBe(2)
  })

  it('closes an open comparison when the tab changes', async () => {
    mocks.api.mockResolvedValue({date:'2026-09-13', you:person(1,'me'), friends:[person(2,'ahead')]})
    const store = useLeaderboardStore()
    await store.load()
    store.open(2)
    expect(store.selected?.nickname).toBe('ahead')

    store.showTab('all-time')

    // Otherwise it reappears on the way back, over a board nobody is looking at.
    expect(store.openFriend).toBeNull()
  })

  it('reports a failed all-time load without touching today’s board', async () => {
    mocks.api.mockRejectedValue({code:'boom', message:'Server fell over.'})
    const store = useLeaderboardStore()

    store.showTab('all-time')
    await store.loadAllTime()

    expect(store.allTimeError).toBe('Server fell over.')
    expect(store.error).toBe('')
    expect(store.allTimeLoading).toBe(false)
  })
})

describe('the viewer inside the ranked list', () => {
  it('does not count itself as a friend', async () => {
    // The list always carries the viewer, so its length cannot answer whether
    // they actually know anybody on it.
    mocks.api.mockResolvedValue({date:'2026-09-14', you:person(1,'me',{you:true}), friends:[person(1,'me',{you:true})]})
    const store = useLeaderboardStore()

    await store.load()

    expect(store.friends).toHaveLength(1)
    expect(store.hasFriends).toBe(false)
  })

  it('counts a real friend beside the viewer', async () => {
    mocks.api.mockResolvedValue({
      date:'2026-09-14', you:person(1,'me',{you:true}),
      friends:[person(2,'ahead',{points:30}), person(1,'me',{points:10,you:true})],
    })
    const store = useLeaderboardStore()

    await store.load()

    expect(store.hasFriends).toBe(true)
    // Server order is kept: the friend outscored the viewer, so they are first.
    expect(store.friends.map(f => f.nickname)).toEqual(['ahead', 'me'])
  })
})
