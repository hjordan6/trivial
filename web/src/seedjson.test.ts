import { describe, expect, it } from 'vitest'
import { externalID, seedJSON, slugify } from './seedjson'
import type { AdminQuestionInput, AdminTopic } from './types'

const input:AdminQuestionInput = {
  topic_slug:'world-geography', prompt:'What is the capital city of Italy?', answer:'Rome',
  difficulty_rating:3, aliases:['Roma'], distractors:['Milan','Naples','Turin'], status:'active',
}
const topic:AdminTopic = {slug:'world-geography', name:'World Geography', active:true, selection_weight:4}

describe('slugify',()=>{
  it('matches the server rule: lowercase, single dashes, none at the ends',()=>{
    expect(slugify('  What is the CAPITAL city of Italy?  ')).toBe('what-is-the-capital-city-of-italy')
    expect(slugify('Who won -- in 1969?')).toBe('who-won-in-1969')
    expect(slugify('???')).toBe('')
  })
})

describe('externalID',()=>{
  it('is stable for the same question, so a re-import updates rather than duplicates',()=>{
    expect(externalID('t','Same prompt')).toBe(externalID('t','Same prompt'))
  })
  it('falls back to the topic when the prompt has nothing sluggable',()=>{
    expect(externalID('geography','???')).toBe('geography')
  })
  it('does not leave a trailing dash when a long prompt is clipped',()=>{
    const id=externalID('t','a'.repeat(20)+' '+'b'.repeat(80))
    expect(id.endsWith('-')).toBe(false)
  })
})

describe('seedJSON',()=>{
  it('renders a complete payload the importer already accepts',()=>{
    const parsed=JSON.parse(seedJSON(input, topic))
    expect(parsed.topics).toHaveLength(1)
    expect(parsed.topics[0].slug).toBe('world-geography')
    expect(parsed.topics[0].name).toBe('World Geography')
    const q=parsed.topics[0].questions[0]
    expect(q).toEqual({
      external_id:'world-geography-what-is-the-capital-city-of-italy',
      difficulty:3,
      prompt:'What is the capital city of Italy?',
      answer:'Rome',
      aliases:['Roma'],
      distractors:['Milan','Naples','Turin'],
    })
  })

  // A missing weight imports as 1, so omitting it would silently flatten a
  // topic that had been tuned.
  it('carries the topic weight through rather than defaulting it',()=>{
    expect(JSON.parse(seedJSON(input, topic)).topics[0].weight).toBe(4)
  })

  it('falls back to the slug and weight 1 when the topic is not loaded',()=>{
    const parsed=JSON.parse(seedJSON(input, undefined))
    expect(parsed.topics[0].name).toBe('world-geography')
    expect(parsed.topics[0].weight).toBe(1)
  })

  it('is indented, since it exists to be read and pasted by a person',()=>{
    expect(seedJSON(input, topic)).toContain('\n  "topics"')
  })
})
