#!/usr/bin/env python3
"""Ask Jev whether one typed answer should be accepted for one question.

    python3 scripts/jev-check.py 947 "28"
    python3 scripts/jev-check.py 57 whale --show-request

Uses the same request as scripts/jev-judge-test.py, so what you see here is
what the batch test measures. Read-only; TYPESAFE_API_KEY comes from .env.
"""

import argparse
import importlib.util
import json
import pathlib
import sys

# Share the request, prompt wording and key loading with the batch test rather
# than keeping a second copy that could drift from it.
_spec = importlib.util.spec_from_file_location(
    "jev_judge_test", pathlib.Path(__file__).resolve().parent / "jev-judge-test.py")
judge_test = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(judge_test)


def main():
    p = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    p.add_argument("question_id", type=int)
    p.add_argument("answer", help="what the player typed (quote it if it has spaces)")
    p.add_argument("--model", default="jev-1.14.0")
    judge_test.add_threshold_args(p)
    p.add_argument("--show-request", action="store_true", help="print the state sent to Jev")
    args = p.parse_args()

    key = judge_test.api_key()
    question = judge_test.load_questions({args.question_id}).get(args.question_id)
    if question is None:
        sys.exit(f"no question with id {args.question_id}")

    print(f"Q{args.question_id}: {question['question']}")
    print(f"  answer:   {question['correct_answer']}")
    print(f"  accepted: {', '.join(question['already_accepted']) or '-'}")
    print(f"  wrong:    {', '.join(question['wrong_options']) or '-'}")
    print(f"  typed:    {args.answer}")
    if args.show_request:
        print(json.dumps(dict(question, player_answer=args.answer), indent=2))

    answers, ms, data = judge_test.judge(key, args.model, question, args.answer)
    label = judge_test.verdict(answers, args.yes, args.other, args.vague).upper()
    print()
    print(f"{label}  acceptable={answers['acceptable']:.2f}  "
          f"names_other_thing={answers['names_other_thing']:.2f}  "
          f"too_vague={answers['too_vague']:.2f}  {ms:.0f} ms  {data.get('model')}")


if __name__ == "__main__":
    main()
