import type { AdminQuestionInput, AdminTopic } from './types'

// slugify mirrors the server's rule in internal/content/seed.go: lowercase,
// letters and digits kept, every other run collapsed to one dash, no dash at
// either end. It only has to agree well enough to produce a stable
// external_id, but keeping the rules identical means a slug generated here
// looks like the ones the importer generates for itself.
export function slugify(value:string):string {
  let out=''
  let dash=false
  for (const ch of value.trim().toLowerCase()) {
    if (/[\p{L}\p{N}]/u.test(ch)) { out+=ch; dash=false }
    else if (!dash && out.length>0) { out+='-'; dash=true }
  }
  return out.replace(/-+$/,'')
}

// externalID derives a stable id from the question itself, so copying the same
// question twice produces the same id and a re-import updates that question
// rather than adding a second copy of it.
export function externalID(topicSlug:string, prompt:string):string {
  const tail=slugify(prompt).slice(0,60).replace(/-+$/,'')
  return tail ? `${topicSlug}-${tail}` : topicSlug
}

// seedJSON renders one question as a complete seed payload -- topic wrapper and
// all -- so what comes off the clipboard can be pasted straight into the import
// box or dropped into a seed file without being reshaped first.
export function seedJSON(input:AdminQuestionInput, topic?:AdminTopic):string {
  return JSON.stringify({
    topics: [{
      slug: input.topic_slug,
      name: topic?.name ?? input.topic_slug,
      // The topic's real weight is carried through: ParseSeed treats a missing
      // weight as 1, so leaving it out would quietly reset a tuned topic the
      // moment this payload was imported.
      weight: topic?.selection_weight ?? 1,
      questions: [{
        external_id: externalID(input.topic_slug, input.prompt),
        difficulty: input.difficulty_rating,
        prompt: input.prompt,
        answer: input.answer,
        aliases: input.aliases,
        distractors: input.distractors,
      }],
    }],
  }, null, 2)
}
