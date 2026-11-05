#!/usr/bin/env python3
"""Try Jev as an answer judge on answers players actually typed.

Each case is a real question, an answer a player typed (or plausibly would),
and whether a fair trivia host should accept it. The script asks Jev the same
yes/no question the game would, and prints how often its verdict agrees.

Read-only: it reads questions from the production database and calls Jev. It
writes nothing anywhere. Costs a fraction of a cent.

    python3 scripts/jev-judge-test.py
    python3 scripts/jev-judge-test.py --model jev-1.13.0 --yes 0.9

TYPESAFE_API_KEY comes from .env; it is never printed.
"""

import argparse
import json
import pathlib
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parent.parent
ENDPOINT = "https://api.typesafe.ai/v1/systemone"

# (question id, what the player typed, should it be accepted?)
CASES = [
    # Should be accepted. Most are real misses from run_answers.
    (947, "28", True),
    (3, "civil rights", True),
    (131, "doyle", True),
    (574, "brown vs the board of education", True),
    (843, "red spot", True),
    (232, "suez", True),
    (766, "mariana", True),
    (763, "ozone", True),
    (334, "seuss", True),
    (1232, "stamp collecting", True),
    (818, "stanley", True),
    (1003, "phonetic alphabet", True),
    (477, "star wars a new hope", True),
    (229, "30 years war", True),
    (636, "twain", True),
    # Should be rejected: less specific than the answer, a different thing the
    # clues also fit, or simply wrong. These are the costly mistakes -- an
    # approval here would be saved and accepted for every later player.
    (57, "whale", False),
    (261, "shark", False),
    (712, "voyager", False),
    (246, "suez", False),
    (1312, "lungfish", False),
    (23, "berlin", False),
    (1, "washington", False),
    (5, "16th", False),
    (270, "pulsar", False),
    (958, "and", False),
    (1123, "east india company", False),
    (1002, "public", False),
    (843, "the eye", False),
    (1090, "miracle", False),
    (1220, "mary", False),
]

INSTRUCTIONS = (
    "A player in a trivia game typed player_answer for this question. "
    "Would a fair trivia host accept it as correct?"
)
CRITERIA = {
    "true": (
        "player_answer names the same specific thing as correct_answer: a "
        "shortened form, surname, nickname, alternate name, abbreviation, "
        "number written as digits or words, or a spelling variant that could "
        "not reasonably mean something else given the question."
    ),
    "false": (
        "player_answer names a different thing, is less specific than "
        "correct_answer in a way the clues would also fit (such as 'bear' for "
        "'polar bear' or 'Gemini' for 'Gemini 4'), matches one of "
        "wrong_options, or is not an answer at all."
    ),
}

# A second, narrower question asked in the same request. "Berlin" for the
# Berlin Wall passes the question above -- in context it can only mean the
# wall -- but on its own it names a city. Answers like that ("Suez", "Mariana",
# "Ozone") are a judgment call for a person, not something to save
# automatically, so this flags them for review instead of letting them through.
OTHER_INSTRUCTIONS = (
    "Ignore the question. Read player_answer completely on its own. Does it "
    "name a real, well-known thing that is different from correct_answer, "
    "such as a city, country, person, substance, island, or another event or "
    "work?"
)
OTHER_CRITERIA = {
    "true": (
        "On its own, player_answer is the ordinary name of something other "
        "than correct_answer, e.g. 'Panama' (a country) for 'The Panama Canal', "
        "or 'Hydrogen' (a gas) for 'The hydrogen bomb'."
    ),
    "false": (
        "On its own, player_answer still refers to correct_answer itself, e.g. "
        "a surname such as 'Hemingway' for 'Ernest Hemingway', a shortened "
        "title, an abbreviation, or a number written differently."
    ),
}


# A third question for the opposite failure. "Mary" for "Typhoid Mary" passes
# both questions above -- it isn't wrong and doesn't name something else -- but
# it keeps the common half of the answer and drops the half that identifies
# it. A surname ("Doyle") or the distinctive words ("Red Spot") are the other
# way round and should pass.
VAGUE_INSTRUCTIONS = (
    "Compare player_answer with correct_answer. Did the player keep only a "
    "generic or common part of correct_answer -- a common first name, or a "
    "general word -- and leave out the part that actually identifies it?"
)
VAGUE_CRITERIA = {
    "true": (
        "player_answer is only the generic part: 'Bill' for 'Buffalo Bill', "
        "'Bridge' for 'The Golden Gate Bridge', 'Telescope' for 'The Hubble "
        "Space Telescope', 'Apollo' for 'Apollo 13'."
    ),
    "false": (
        "player_answer keeps the identifying part, even if it drops a generic "
        "word: 'Hemingway' for 'Ernest Hemingway', 'Golden Gate' for 'The "
        "Golden Gate Bridge', 'Women's suffrage' for 'The women's suffrage "
        "movement'; or it is a full alternate name rather than a fragment."
    ),
}


# The thresholds internal/judge applies (judge.DefaultThresholds). Keep them in
# step: these scripts are how a change to either is measured first.
MIN_ACCEPTABLE = 0.65
MAX_NAMES_OTHER_THING = 0.35
MAX_TOO_VAGUE = 0.45


def verdict(answers, yes=MIN_ACCEPTABLE, other=MAX_NAMES_OTHER_THING, vague=MAX_TOO_VAGUE):
    """Accept or reject, as the game would.

    Accepting needs all three: a high enough "acceptable", and a low enough
    "names something else" and "too vague". Anything else is a rejection --
    there is no review queue, so an answer that is not clearly right stays
    wrong.
    """
    if (answers["acceptable"] >= yes and answers["names_other_thing"] <= other
            and answers["too_vague"] <= vague):
        return "accept"
    return "reject"


def api_key():
    for line in (ROOT / ".env").read_text().splitlines():
        if line.startswith("TYPESAFE_API_KEY="):
            key = line.split("=", 1)[1].strip().strip("'\"")
            if key:
                return key
    sys.exit("TYPESAFE_API_KEY is missing or empty in .env")


def load_questions(ids):
    sql = f"""
        SELECT json_object_agg(q.id, json_build_object(
            'question', q.prompt,
            'correct_answer', q.canonical_answer,
            'already_accepted', COALESCE((SELECT json_agg(a.alias ORDER BY a.id)
                FROM question_aliases a WHERE a.question_id = q.id), '[]'),
            'wrong_options', COALESCE((SELECT json_agg(d.option_text ORDER BY d.id)
                FROM question_distractors d WHERE d.question_id = q.id), '[]')))
        FROM questions q WHERE q.id IN ({",".join(str(int(i)) for i in ids)})"""
    out = subprocess.run(
        ["docker", "exec", "-i", "trivial-db-1",
         "psql", "-U", "trivial", "-d", "trivial", "-X", "-At", "-c", sql],
        check=True, capture_output=True, text=True,
    ).stdout.strip()
    # json_object_agg over no rows is NULL, which psql prints as nothing.
    return {int(k): v for k, v in json.loads(out or "{}").items()}


def judge(key, model, question, player_answer):
    state = dict(question, player_answer=player_answer)
    body = json.dumps({
        "model": model,
        "state": state,
        "questions": {
            "acceptable": {
                "type": "noul", "instructions": INSTRUCTIONS, "criteria": CRITERIA,
            },
            "names_other_thing": {
                "type": "noul", "instructions": OTHER_INSTRUCTIONS, "criteria": OTHER_CRITERIA,
            },
            "too_vague": {
                "type": "noul", "instructions": VAGUE_INSTRUCTIONS, "criteria": VAGUE_CRITERIA,
            },
        },
    }).encode()
    req = urllib.request.Request(ENDPOINT, data=body, headers={
        "Authorization": f"Bearer {key}",
        "Content-Type": "application/json",
    })
    for attempt in range(4):
        started = time.monotonic()
        try:
            with urllib.request.urlopen(req, timeout=15) as resp:
                data = json.load(resp)
            ms = (time.monotonic() - started) * 1000
            answers = {name: a["noul"] for name, a in data["answers"].items()}
            return answers, ms, data
        except urllib.error.HTTPError as e:
            if e.code in (429, 529) and attempt < 3:
                time.sleep(2 ** attempt)
                continue
            raise SystemExit(f"Jev returned HTTP {e.code}: {e.read().decode()[:500]}")


def add_threshold_args(p):
    p.add_argument("--yes", type=float, default=MIN_ACCEPTABLE,
                   help=f"accept only if acceptable >= this (default {MIN_ACCEPTABLE})")
    p.add_argument("--other", type=float, default=MAX_NAMES_OTHER_THING,
                   help=f"...and names_other_thing <= this (default {MAX_NAMES_OTHER_THING})")
    p.add_argument("--vague", type=float, default=MAX_TOO_VAGUE,
                   help=f"...and too_vague <= this (default {MAX_TOO_VAGUE})")


def main():
    p = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    p.add_argument("--model", default="jev-1.14.0")
    add_threshold_args(p)
    args = p.parse_args()

    key = api_key()
    questions = load_questions({qid for qid, _, _ in CASES})
    missing = sorted({qid for qid, _, _ in CASES} - questions.keys())
    if missing:
        sys.exit(f"questions not found: {missing}")

    right = wrong_yes = wrong_no = 0
    tokens = 0
    latencies = []
    model_seen = None
    print(f"{'id':>5}  {'player typed':<34} {'answer':<30} {'want':<6} "
          f"{'ok':>5} {'other':>5} {'vague':>5}  verdict")
    for qid, typed, want in CASES:
        q = questions[qid]
        answers, ms, data = judge(key, args.model, q, typed)
        latencies.append(ms)
        tokens += data.get("usage", {}).get("input_tokens", 0)
        model_seen = data.get("model", model_seen)
        v = verdict(answers, args.yes, args.other, args.vague)
        if (v == "accept") == want:
            right += 1
            mark = "ok"
        elif v == "accept":
            wrong_yes += 1
            mark = "WRONG YES"
        else:
            wrong_no += 1
            mark = "wrong no"
        print(f"{qid:>5}  {typed[:34]:<34} {q['correct_answer'][:30]:<30} "
              f"{'yes' if want else 'no':<6} {answers['acceptable']:5.2f} "
              f"{answers['names_other_thing']:5.2f} {answers['too_vague']:5.2f}  {v:<7} {mark}")

    latencies.sort()
    n = len(CASES)
    print()
    print(f"model {model_seen}, thresholds acceptable>={args.yes} "
          f"names_other_thing<={args.other} too_vague<={args.vague}")
    print(f"{right}/{n} right")
    print(f"wrong approvals: {wrong_yes}   <- the mistake that matters: it would be saved for everyone")
    print(f"wrong rejections: {wrong_no}")
    print(f"latency: median {latencies[n // 2]:.0f} ms, slowest {latencies[-1]:.0f} ms")
    print(f"input tokens: {tokens} (~${tokens * 0.042 / 1e6:.5f})")


if __name__ == "__main__":
    main()
