import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { RECENT_DAYS, useHistoryStore } from './history'
import type { HistoryDay, HistoryTopic } from '../types'

beforeEach(() => { setActivePinia(createPinia()) })

// Days as the server sends them: oldest first.
function day(date:string, points:number, correct = 0, typed = 0):HistoryDay {
  return {date, points, correct, typed}
}
function topic(slug:string, asked:number, correct:number, points:number):HistoryTopic {
  return {slug, name:slug.toUpperCase(), asked, correct, points}
}

function withDays(...days:HistoryDay[]) {
  const store = useHistoryStore()
  store.days = days
  return store
}

describe('history store', () => {
  it('reads the day list newest first', () => {
    const store = withDays(day('2026-08-01', 10), day('2026-08-02', 20), day('2026-08-03', 30))
    expect(store.recentFirst.map(d => d.date)).toEqual(['2026-08-03', '2026-08-02', '2026-08-01'])
  })

  it('averages every day played', () => {
    const store = withDays(day('2026-08-01', 10), day('2026-08-02', 21))
    expect(store.averagePoints).toBe(15.5)
    expect(store.daysPlayed).toBe(2)
  })

  it('averages recent form over the last five days played, however far apart they are', () => {
    // Seven days with a gap. Only the most recent five count, and the two
    // oldest must not pull the number down.
    const store = withDays(
      day('2026-07-01', 0), day('2026-07-02', 0),
      day('2026-08-01', 10), day('2026-08-02', 20), day('2026-08-03', 30), day('2026-08-04', 40), day('2026-08-05', 50),
    )
    expect(store.recentPoints).toHaveLength(RECENT_DAYS)
    expect(store.recentAverage).toBe(30)
    // The all-time average still sees everything.
    expect(store.averagePoints).toBeCloseTo(150 / 7)
  })

  it('averages what it has when fewer than five days have been played', () => {
    const store = withDays(day('2026-08-01', 12), day('2026-08-02', 18))
    expect(store.recentAverage).toBe(15)
  })

  it('has no averages and no best day before anything is played', () => {
    const store = withDays()
    expect(store.averagePoints).toBeNull()
    expect(store.recentAverage).toBeNull()
    expect(store.best).toBeUndefined()
  })

  it('picks the highest scoring day, breaking a tie towards the more recent one', () => {
    const store = withDays(day('2026-08-01', 40), day('2026-08-02', 52), day('2026-08-03', 52), day('2026-08-04', 7))
    expect(store.best?.date).toBe('2026-08-03')
    expect(store.best?.points).toBe(52)
  })

  it('ranks categories by accuracy, then by points per question', () => {
    const store = useHistoryStore()
    store.topics = [
      topic('music', 6, 3, 15),
      // Same accuracy as music, but more of it typed, so it ranks above.
      topic('sport', 6, 3, 21),
      topic('geography', 9, 8, 40),
      topic('history', 3, 0, 0),
    ]
    expect(store.ranked.map(t => t.slug)).toEqual(['geography', 'sport', 'music', 'history'])
    expect(store.ranked[0].accuracy).toBeCloseTo(8 / 9)
    expect(store.ranked[1].pointsPerQuestion).toBe(3.5)
  })

  it('shows a weakest list only once it would differ from the strongest one', () => {
    const store = useHistoryStore()
    store.topics = [topic('a', 3, 3, 15), topic('b', 3, 2, 10), topic('c', 3, 1, 5)]
    expect(store.strongest.map(t => t.slug)).toEqual(['a', 'b', 'c'])
    expect(store.weakest).toEqual([])

    store.topics = [...store.topics, topic('d', 3, 0, 0)]
    expect(store.strongest.map(t => t.slug)).toEqual(['a', 'b', 'c'])
    expect(store.weakest.map(t => t.slug)).toEqual(['d', 'c', 'b'])
  })

  it('ignores a category it has never actually been asked', () => {
    const store = useHistoryStore()
    store.topics = [topic('asked', 3, 1, 5), topic('unasked', 0, 0, 0)]
    expect(store.ranked.map(t => t.slug)).toEqual(['asked'])
  })
})
