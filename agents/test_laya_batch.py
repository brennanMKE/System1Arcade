"""Tests for the answer cache in laya_batch.py. They use a fake model, so they
run in a second without loading Laya (torch and laya must be importable):

    .venv/bin/python -m unittest discover agents
"""
import sys
import unittest

sys.path.insert(0, __file__.rsplit("/", 1)[0])
from laya_batch import AnswerCache, answer_cached, cache_key  # noqa: E402

SAFE = {"type": "choice", "instructions": "What kind of place is it?",
        "criteria": {"a safe place": "", "a deadly place": ""}}
BOMB = {"type": "noul", "instructions": "Is a bomb about to hit the cannon?"}


class FakeModel:
    """Answers with the state's length and records every call."""

    def __init__(self):
        self.calls = []

    def __call__(self, prompts):
        self.calls.append(prompts)
        return {k: {q: {"type": "noul", "noul": len(p["state"]) / 100} for q in p["questions"]}
                for k, p in prompts.items()}


class AnswerCacheTest(unittest.TestCase):
    def test_only_misses_reach_the_model_as_one_batch(self):
        model, cache = FakeModel(), AnswerCache(100)
        answer_cached({"up": {"state": "Hopping up is safe.", "questions": {"q": SAFE}}}, model, cache)
        got = answer_cached({
            "up": {"state": "Hopping up is safe.", "questions": {"q": SAFE}},
            "left": {"state": "Hopping left is deadly.", "questions": {"q": SAFE}},
            "bomb": {"state": "No bomb is falling.", "questions": {"threat": BOMB}},
        }, model, cache)
        self.assertEqual(len(model.calls), 2)
        self.assertEqual(set(model.calls[1]), {"left", "bomb"})  # both misses in one call
        self.assertEqual(list(got), ["up", "left", "bomb"])
        self.assertEqual(got["up"]["q"]["noul"], 0.19)
        self.assertEqual((cache.hits, cache.misses), (1, 3))

    def test_all_hits_skip_the_model(self):
        model, cache = FakeModel(), AnswerCache(100)
        req = {"a": {"state": "No bomb is falling.", "questions": {"threat": BOMB}}}
        first = answer_cached(req, model, cache)
        self.assertEqual(answer_cached(req, model, cache), first)
        self.assertEqual(len(model.calls), 1)
        self.assertEqual(cache.stats()["skipped_calls"], 1)

    def test_repeats_within_a_request_are_asked_once(self):
        model, cache = FakeModel(), AnswerCache(100)
        got = answer_cached({
            "a": {"state": "Same sentence.", "questions": {"x": BOMB}},
            "b": {"state": "Same sentence.", "questions": {"y": BOMB}},
        }, model, cache)
        self.assertEqual(model.calls, [{"a": {"state": "Same sentence.", "questions": {"x": BOMB}}}])
        self.assertEqual(got["a"]["x"], got["b"]["y"])

    def test_key_uses_exact_state_and_question_content(self):
        self.assertEqual(cache_key("s", SAFE), cache_key("s", dict(reversed(list(SAFE.items())))))
        self.assertNotEqual(cache_key("s", SAFE), cache_key("s ", SAFE))
        self.assertNotEqual(cache_key("s", SAFE), cache_key("s", {**SAFE, "instructions": "Is it safe?"}))
        swapped = {**SAFE, "criteria": {"a deadly place": "", "a safe place": ""}}
        self.assertNotEqual(cache_key("s", SAFE), cache_key("s", swapped))  # option order matters
        self.assertEqual(cache_key("s", {**SAFE, "criteria": ["a safe place", "a deadly place"]}),
                         cache_key("s", {**SAFE, "criteria": ["a safe place", "a deadly place"]}))

    def test_least_recently_used_answer_is_dropped(self):
        model, cache = FakeModel(), AnswerCache(2)
        for s in ["one", "two", "one", "three"]:  # "two" is least recently used when "three" arrives
            answer_cached({"k": {"state": s, "questions": {"q": BOMB}}}, model, cache)
        self.assertEqual(len(cache.entries), 2)
        self.assertIsNone(cache.get(cache_key("two", BOMB)))
        self.assertIsNotNone(cache.get(cache_key("one", BOMB)))

    def test_size_zero_turns_the_cache_off(self):
        model, cache = FakeModel(), AnswerCache(0)
        req = {"a": {"state": "No bomb is falling.", "questions": {"threat": BOMB}}}
        answer_cached(req, model, cache)
        answer_cached(req, model, cache)
        self.assertEqual(len(model.calls), 2)
        self.assertEqual(cache.stats()["hits"], 0)


if __name__ == "__main__":
    unittest.main()
