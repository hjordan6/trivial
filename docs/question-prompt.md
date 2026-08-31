# Question-writing prompt

Paste the block below into Claude, ChatGPT, or any other assistant, edit the
last line to say what you want, and paste the JSON it returns into the admin
panel's import box at `/admin`.

The importer is forgiving about the things assistants get wrong — curly
quotes, a ```` ```json ```` fence around the answer — so a straight copy out of
a chat window works. It is strict about content: a question that could never
reach a board is refused, and so is one whose answer is already used in that
topic and difficulty.

---

````text
You are writing questions for a daily trivia game. Players type a free-text
answer first; if they miss, they get four multiple-choice options.

Return ONLY a JSON object, no commentary, in exactly this shape:

{
  "questions": [
    {
      "question": "This Mexican painter turned self-portraits, physical pain, and indigenous imagery into one of the most recognizable bodies of 20th-century art.",
      "category": "Art & Culture",
      "difficulty": 4,
      "answer": "Frida Kahlo",
      "acceptedAnswers": ["Kahlo"],
      "multipleChoiceOptions": ["Frida Kahlo", "Diego Rivera", "Remedios Varo", "Leonora Carrington"]
    }
  ]
}

CATEGORY must be exactly one of these eleven strings, copied character for
character (the punctuation and the ampersands matter):

  U.S. History
  World History
  Geography
  Science & Nature
  Sports
  Movies & TV
  Music
  Literature & Language
  Art & Culture
  Technology & Internet
  Modern Pop Culture

DIFFICULTY is a whole number from 1 to 10. It is grouped into bands, and the
band is what a player sees:
  1-4   easy    - most people who follow the subject at all will know it
  5-7   medium  - a regular reader of the subject gets it
  8-10  hard    - rewards genuine depth, but is not a trick

QUESTION is a statement, not a question sentence. Write "This Dutch painter
sold almost nothing in his lifetime..." rather than "Which painter...? " Do not
end with "Who is he?", "What novel is it?", or any similar trailing question —
the game supplies that framing itself. Keep it to one or two sentences.

ANSWER is what a player has to type. Keep it short — a name, a title, a place,
usually one to four words. Never a sentence, a date range, or something with
several equally natural phrasings.

ACCEPTEDANSWERS are other spellings a player might reasonably type. Leave it
as [] when there are none. Do NOT list variants that differ only by:
  - capitalisation            (grading is case-insensitive)
  - accents or diacritics     ("Brontë" and "Bronte" already match)
  - punctuation, hyphens, apostrophes  ("Iran-Contra" and "Iran Contra" match)
  - a leading "The", "A" or "An"       ("The OED" and "OED" match)
Those are handled already and just add noise. DO list genuine alternatives:
short forms ("OED"), stage names ("Childish Gambino"), surnames on their own
("Kahlo"), alternative titles.

MULTIPLECHOICEOPTIONS must contain exactly four strings, and one of them must
be the answer, spelled identically. The other three must be plausible to
someone who half-knows the subject and clearly wrong to someone who knows it —
same category, same era or register. None of them may be a correct answer by
any reading, including anything listed in acceptedAnswers.

Also:
  - One fact per question. Do not write two questions with the same answer.
  - Every question must have exactly one defensible answer. If a knowledgeable
    person could argue for a different answer, rewrite it.
  - Prefer concrete, checkable detail over vague description. A good hard
    question names two or three specific facts that only fit one subject.
  - No questions about events after your knowledge cutoff, and nothing whose
    answer changes over time ("the current champion", "the tallest building").

Write 10 hard questions for Science & Nature.
````

---

## Using it

Change the final line to whatever you need — `Write 8 easy questions for
Geography`, or `Write 15 questions for Music, a mix of difficulties`.

A few things worth asking for explicitly when you need them:

- **Avoiding what you already have.** The assistant cannot see your library, so
  it will happily write another Frida Kahlo question. Either paste a list of
  answers you already hold in that topic and difficulty and say "do not use any
  of these answers", or let the importer catch it — it refuses the whole paste
  and names the clash.
- **Filling a specific gap.** The library needs roughly 540 questions per
  difficulty to sustain a year at the 180-day cooldown, so ask by band:
  `Write 20 medium questions for World History`.
- **Batch size.** Ten to twenty at a time keeps quality up. Past that,
  assistants start repeating themselves and reaching for the same handful of
  famous subjects.

## What the importer will reject

- Fewer than three genuinely distinct wrong options.
- A wrong option that also grades as correct.
- A `difficulty` outside 1-10, or a missing `answer`, `question`, or `category`.
- An answer already used in that topic and difficulty band, whether by an
  existing question or by another question in the same paste. The message names
  the answer and the question already holding it.

Nothing is written unless the whole paste is good, so a rejected import leaves
the library exactly as it was.
