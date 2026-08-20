import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useRunStore } from './run'

beforeEach(()=>{setActivePinia(createPinia());vi.stubGlobal('window',{setInterval:vi.fn()})})

describe('run store',()=>{
  it('uses server time to compute the remaining clock',()=>{
    vi.setSystemTime(new Date('2026-08-19T18:00:00Z'))
    const store=useRunStore()
    store.puzzle={date:'2026-08-19',time_limit_seconds:135,questions:[]}
    store.run={id:'r',puzzle:store.puzzle,started_at:'2026-08-19T18:00:00Z',expires_at:'2026-08-19T18:02:15Z',answers:[]}
    expect(store.remainingMs).toBe(135000)
  })

  it('constructs rows in topic order and scores stars plus circles',()=>{
    const store=useRunStore();const questions=Array.from({length:9},(_,i)=>({question_id:i+1,topic_slug:`t${Math.floor(i/3)}`,topic_name:`T${Math.floor(i/3)}`,topic_position:Math.floor(i/3),difficulty:(['easy','medium','hard'][i%3] as any),prompt:'?'}))
    store.puzzle={date:'2026-08-19',time_limit_seconds:135,questions}
    store.run={id:'r',puzzle:store.puzzle,started_at:'2026-08-19T18:00:00Z',completed_at:'2026-08-19T18:02:15Z',expires_at:'2026-08-19T18:02:15Z',answers:questions.map((q,i)=>({question_id:q.question_id,stage:'free_text',outcome:(['star','circle','expired','circle','miss','expired','star','star','miss'][i] as any)}))}
    expect(store.shareText('https://example.test/c/x')).toBe('Trivial 2026-08-19\n⭐🟢⏰\n🟢🔴⏰\n⭐⭐🔴\n5/9 in 2:15\nhttps://example.test/c/x')
  })
})
