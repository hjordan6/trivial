import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { MAX_POINTS, topicEmoji, useRunStore } from './run'
import type { Outcome, Question } from '../types'

beforeEach(()=>{setActivePinia(createPinia());vi.stubGlobal('window',{setInterval:vi.fn()})})

// Nine questions laid out as three topics x easy/medium/hard, dealt in board
// order. Tests hand back outcomes in that same order.
const board:Question[]=Array.from({length:9},(_,i)=>({question_id:i+1,topic_slug:`t${Math.floor(i/3)}`,topic_name:`T${Math.floor(i/3)}`,topic_position:Math.floor(i/3),difficulty:(['easy','medium','hard'][i%3] as any),prompt:'?'}))

function runWith(outcomes:(Outcome|undefined)[], questions:Question[]=board){
  const store=useRunStore()
  store.puzzle={date:'2026-08-19',time_limit_seconds:135,questions}
  store.run={id:'r',puzzle:store.puzzle,started_at:'2026-08-19T18:00:00Z',completed_at:'2026-08-19T18:02:15Z',expires_at:'2026-08-19T18:02:15Z',answers:questions.map((q,i)=>({question_id:q.question_id,stage:'free_text',outcome:outcomes[i]}))}
  return store
}

const MIXED:Outcome[]=['star','circle','expired','circle','miss','expired','star','star','miss']

describe('run store',()=>{
  it('uses server time to compute the remaining clock',()=>{
    vi.setSystemTime(new Date('2026-08-19T18:00:00Z'))
    const store=useRunStore()
    store.puzzle={date:'2026-08-19',time_limit_seconds:135,questions:[]}
    store.run={id:'r',puzzle:store.puzzle,started_at:'2026-08-19T18:00:00Z',expires_at:'2026-08-19T18:02:15Z',answers:[]}
    expect(store.remainingMs).toBe(135000)
  })

  it('orders rows by topic then difficulty, whatever order the board was dealt in',()=>{
    const shuffled=[board[5],board[0],board[8],board[3],board[7],board[1],board[6],board[2],board[4]]
    const store=runWith([],shuffled)
    expect(store.orderedRows().map(row=>row.map(q=>q.question_id))).toEqual([[1,2,3],[4,5,6],[7,8,9]])
  })

  it('counts stars and circles alike for the raw score, and stars alone for the star count',()=>{
    const store=runWith(MIXED)
    expect(store.score).toBe(5)
    expect(store.stars).toBe(3)
  })

  it('scores a typed answer at its difficulty plus the bonus, and a chosen one at base',()=>{
    // easy star 3+2, medium circle 4, hard expired 0,
    // easy circle 3, medium miss 0, hard expired 0,
    // easy star 3+2, medium star 4+2, hard miss 0
    expect(runWith(MIXED).points).toBe(5+4+0 + 3+0+0 + 5+6+0)
  })

  it('gives a perfect typed run the maximum and an all-choice run the base total',()=>{
    expect(runWith(Array(9).fill('star')).points).toBe(MAX_POINTS)
    expect(runWith(Array(9).fill('star')).points).toBe(54)
    expect(runWith(Array(9).fill('circle')).points).toBe(36)
    expect(runWith(Array(9).fill('miss')).points).toBe(0)
    expect(runWith(Array(9).fill(undefined)).points).toBe(0)
  })

  it('builds share text with the grid, both scores, and the bare link it was given',()=>{
    const store=runWith(MIXED)
    expect(store.shareText('https://example.test')).toBe('Trivial 2026-08-19\n❓ ⭐🟢⏰\n❓ 🟢🔴⏰\n❓ ⭐⭐🔴\n23/54 pts · 5/9 in 2:15\nhttps://example.test')
  })

  it('labels each share row with its topic emoji',()=>{
    const topical=board.map((q,i)=>({...q,
      topic_slug:['world-geography','film-and-television','pub-quiz-cuisine'][Math.floor(i/3)],
      topic_name:['World Geography','Film & Television','Pub Quiz Cuisine'][Math.floor(i/3)]}))
    const store=runWith(MIXED,topical)
    expect(store.shareText('https://example.test').split('\n').slice(1,4)).toEqual(['🌍 ⭐🟢⏰','🎬 🟢🔴⏰','🍽️ ⭐⭐🔴'])
  })

  // Every topic that ships in the question dump, so a new board never shares a
  // row of question marks. Distinct emoji throughout except where the topics
  // really are the same subject split two ways.
  it('gives every seeded topic its own emoji',()=>{
    const seeded:[string,string,string][]=[
      ['geography','Geography','🌍'],
      ['science-nature','Science & Nature','🔬'],
      ['movies-tv','Movies & TV','🎬'],
      ['music','Music','🎵'],
      ['sports','Sports','⚽'],
      ['u-s-history','U.S. History','🗽'],
      ['world-history','World History','🏛️'],
      ['art-culture','Art & Culture','🎨'],
      ['literature-language','Literature & Language','📚'],
      ['technology-internet','Technology & Internet','💻'],
      ['modern-pop-culture','Modern Pop Culture','✨'],
    ]
    for(const [slug,name,emoji] of seeded) expect([slug,topicEmoji(slug,name)]).toEqual([slug,emoji])
  })

  it('places an unseen topic by name, and marks the ones it cannot',()=>{
    expect(topicEmoji('food-and-drink','Food & Drink')).toBe('🍽️')
    expect(topicEmoji('ancient-mythology','Ancient Mythology')).toBe('🏛️')
    expect(topicEmoji('mystery-box','Mystery Box')).toBe('❓')
  })
})
