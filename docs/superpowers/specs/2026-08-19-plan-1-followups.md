# Plan 1 follow-ups

Findings raised during Plan 1's execution that were consciously deferred or parked rather than
fixed, each with the reasoning recorded at the time. None blocks a merge. This is input to Plan 2's
design, not a bug backlog to burn down first.

## Worth doing early in Plan 2

- **`Normalize` apostrophe coverage.** Folds ASCII `'`, curly `’`, and modifier `ʼ`. Other
  apostrophe-shaped runes (`‘` U+2018, `′` U+2032, fullwidth `＇`) become a space, so `O‘Brien`
  normalizes to `o brien`. Phone keyboards standardise on U+2019 so this is unlikely in practice,
  but Plan 2 accepts live player input where it becomes reachable.
- **Numeric answers have no word-form convention.** `7`/`Seven`, `Six`/`6`, `Eleven`/`11`,
  `Fifteen`/`15` all carry both forms; `79` and `147` carry only the digits. The seed file has a
  habit, not a rule. Decide the rule and apply it to the class.
- **The Roman-numeral tolerance rule zeroes the whole alias.** `henry viii` correctly requires an
  exact numeral, but the strictness also applies to the earlier words, so `Czar Nicholas II` needed
  an explicit alias to be accepted alongside `Tsar`. A rule that requires the numeral token to
  match exactly while still forgiving typos in the rest of the alias would be strictly better.
- **`grading.Grade`'s near-miss log has no consumer.** `Result.NearMiss` is computed and returned
  but nothing records it. Plan 2's answer endpoint is where it should be logged — it is the raw
  material for curating aliases from real misses.

## Lower priority

- **`Date.Scan` has no `[]byte` case.** pgx v5's `DateCodec` always yields `time.Time` for a `date`
  column, verified, so the path is currently dead.
- **`Date.Equal` (struct comparison) can diverge from `Before`** (which normalizes through
  `time.Date`) for a hand-built out-of-range `Date` literal. Unreachable via `ParseDate`,
  `PuzzleDateAt`, or `AddDays`.
- **`Migrate`'s minimum-connection check** guards against `MaxConns < 2` but not against other pool
  consumers holding connections at call time.
- **The advisory lock id** `4815162342` is a named local const but still a magic number.
- **`ReplaceAliases`/`ReplaceDistractors` delete-then-insert** is not concurrency-safe outside a
  shared transaction. Current usage is single-writer-per-transaction, enforced by the CLI.
- **`ReplaceDistractors` dedupes by raw string, not normalized form**, so case and whitespace
  variants both survive as visible options.
- **`ParseSeed`'s fold table matches precomposed special bases only.** A combining mark stacked on
  one (`Ǿ`, U+01FE) bypasses it.
- **`transform.String`'s error is silently swallowed** in `Normalize`. Appears unreachable with the
  current `x/text`.
- **`array_position` difficulty ordering** in `puzzle.Get` is redundant with the enum's own
  declaration order today. Kept deliberately as a guard against a future enum reorder, but the
  reasoning is not in a comment.
- **`topicSummary`** hand-rolls a comma join instead of `strings.Join`.
- **`QUESTION_COOLDOWN_DAYS=""`** (explicitly empty) silently takes the default rather than
  erroring. Inherent to `os.Getenv`.

## Content calibration

The 54-question starter library is scaffolding to exercise the generator; the real library is to be
authored later by import, hand-writing, and AI generation. Two known soft spots, both fine to fix by
replacing rows:

- Several "hard" questions are easy or medium recall — `79` (atomic number of gold) most clearly.
  The three tiers will not feel distinct in play.
- `sport-medium-2` (perfect game) has three distractors that are technically true statements about
  the described game; the "what is the term for" phrasing rescues it but the set is weak.

## Test coverage the suite does not have

Verified by mutation during the final review — these guarantees ARE pinned: determinism,
idempotency, the cooldown query at both boundaries in both directions, the cooldown as a hard
constraint, alias normalization at rest, the grading digit rule, the 9-character tolerance
boundary, and candidate ordering.

Not pinned, and worth knowing:

- **No test asserts that a plausible wrong answer never grades correct.** The seed sweep covers
  distractors, which is what is enumerable. Near-neighbour terms that are not distractors — the
  `Isotopes`/`Isotones` shape — are caught by human review only. This is not fixable by a test;
  it is a content-review discipline.
- **Spec §9 promises the normalizer and grader "the heaviest table-driven coverage in the
  project."** They are in fact the lightest by line count. The tolerance table and normalization
  cases are correct but thin on adversarial near-misses.

## Dropped from the spec without being recorded

- **The lazy puzzle-generation fallback** (spec §7: "a lazy fallback generates the current day under
  a Postgres advisory lock if it is somehow missing") is in the spec but was never in Plan 1 and is
  not in the code. It is unreachable without the HTTP layer, so deferring is right — but it was
  dropped silently. It belongs on Plan 2's list. `db.Migrate` already has the advisory-lock pattern
  to copy.
