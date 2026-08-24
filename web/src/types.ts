export type Difficulty = 'easy' | 'medium' | 'hard'
export type Stage = 'free_text' | 'multiple_choice'
export type Outcome = 'star' | 'circle' | 'miss' | 'expired'

export interface Question { question_id:number; topic_slug:string; topic_name:string; topic_position:number; difficulty:Difficulty; prompt:string }
export interface Puzzle { date:string; time_limit_seconds:number; questions:Question[] }
export interface Answer { question_id:number; stage:Stage; free_text_submission?:string; chosen_option?:string; outcome?:Outcome; canonical_answer?:string }
export interface Run { id:string; puzzle:Puzzle; started_at:string; expires_at:string; completed_at?:string; answers:Answer[] }
export interface RunEnvelope { server_time:string; puzzle:Puzzle; run?:Run }
export interface Stats { days_played:number; score_distribution:number[]; current_streak:number; longest_streak:number }
export interface APIError { code:string; message:string }

export interface AdminSlot { position:number; pinned:boolean; topic_slug:string|null; topic_name:string|null }
export interface AdminDay { date:string; generated:boolean; has_runs:boolean; editable:boolean; slots:AdminSlot[] }
export interface AdminTopic { slug:string; name:string; active:boolean; selection_weight:number }
export interface AdminGenerateResult { generated:string[]; skipped:string[]; failed:{date:string; message:string}[] }
export interface AdminQuestion {
  id:number; topic_slug:string; topic_name:string
  difficulty:Difficulty; difficulty_rating:number
  prompt:string; answer:string
  aliases:string[]; distractors:string[]
  status:string; used_count:number; last_used:string|null
}
export interface AdminQuestionPage { questions:AdminQuestion[]; total:number; limit:number; offset:number }
export interface AdminImportResult { topics:number; questions:number }
export interface AdminExportField { name:string; by_default:boolean; multi_value:boolean }
export interface AdminExportFields { fields:AdminExportField[]; separator:string }
export interface AdminQuestionInput {
  topic_slug:string; prompt:string; answer:string; difficulty_rating:number
  aliases:string[]; distractors:string[]; status:string
}
